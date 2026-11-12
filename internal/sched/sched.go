// Package sched schedules jobs onto slices of an accelerator pod.
//
// Each scheduling pass orders the queue (priority, then fair share, then arrival), places jobs
// with a topo.Strategy, and when the job at the head of the queue cannot be placed it may:
//   - preempt lower-priority jobs, choosing the slice whose eviction loses the least work, or
//   - reserve the earliest slice it can get once running jobs end, and backfill smaller jobs
//     into the gaps as long as they cannot delay that reservation (EASY backfilling).
package sched

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/yasaswinivardhireddy-png/slicewise/internal/topo"
)

// MaxDuration is the longest run time a job may request.
const MaxDuration = 7 * 24 * time.Hour

// Priority orders jobs: higher priorities run first and may preempt lower ones.
type Priority int

const (
	BestEffort Priority = iota
	Batch
	Production
)

var priorityNames = [...]string{"best-effort", "batch", "production"}

func (p Priority) String() string {
	if p < BestEffort || p > Production {
		return fmt.Sprintf("priority(%d)", int(p))
	}
	return priorityNames[p]
}

// ParsePriority parses "production", "batch" or "best-effort".
func ParsePriority(s string) (Priority, error) {
	for i, n := range priorityNames {
		if s == n {
			return Priority(i), nil
		}
	}
	return 0, fmt.Errorf("unknown priority %q", s)
}

// State is where a job is in its lifecycle.
type State int

const (
	Queued State = iota
	Running
	Succeeded
	Cancelled
)

var stateNames = [...]string{"queued", "running", "succeeded", "cancelled"}

func (s State) String() string {
	if s < Queued || s > Cancelled {
		return fmt.Sprintf("state(%d)", int(s))
	}
	return stateNames[s]
}

// Done reports whether a job in this state has left the scheduler for good.
func (s State) Done() bool { return s == Succeeded || s == Cancelled }

// Tenant is a team with a chip quota; quotas set each team's fair share of the pod.
type Tenant struct {
	Name  string `json:"name"`
	Quota int    `json:"quota"`
}

// Spec describes a job to run.
type Spec struct {
	Name     string
	Tenant   string
	Priority Priority
	Shape    topo.Dims
	// Duration is the job's run time. A job completes after running this long without being
	// preempted; a preempted job restarts from the beginning (no checkpoints).
	Duration time.Duration
}

// Transition is one entry in a job's history.
type Transition struct {
	At     time.Duration
	State  State
	Reason string
}

// Job is a submitted job. Callers must treat it as read-only.
type Job struct {
	Spec
	ID          string
	Seq         int
	Submitted   time.Duration
	State       State
	Box         topo.Box      // placement while running, and the last placement afterwards
	Started     time.Duration // start of the current or last run
	FirstStart  time.Duration // valid when HasStarted
	HasStarted  bool
	Ended       time.Duration // when the job succeeded or was cancelled
	Preemptions int
	History     []Transition
	slot        int32
	fragBlocked bool
}

// Chips returns the number of chips the job needs.
func (j *Job) Chips() int { return j.Shape.Volume() }

// EndsAt returns when a running job completes.
func (j *Job) EndsAt() time.Duration { return j.Started + j.Duration }

// Wait returns how long the job queued before it first started, or so far if it hasn't.
func (j *Job) Wait(now time.Duration) time.Duration {
	switch {
	case j.HasStarted:
		return j.FirstStart - j.Submitted
	case j.State == Cancelled:
		return j.Ended - j.Submitted
	default:
		return now - j.Submitted
	}
}

// EventType names a change in a job's state.
type EventType string

const (
	EventSubmitted EventType = "submitted"
	EventStarted   EventType = "started"
	EventPreempted EventType = "preempted"
	EventSucceeded EventType = "succeeded"
	EventCancelled EventType = "cancelled"
)

// Event records a state change, in the order it happened.
type Event struct {
	At     time.Duration
	Type   EventType
	JobID  string
	Name   string
	Detail string
}

// Policy selects the scheduling features to use.
type Policy struct {
	Name       string
	Placement  topo.Strategy
	Priorities bool // order the queue by priority (otherwise by arrival only)
	FairShare  bool // within a priority, favor tenants using the smallest share of their quota
	Backfill   bool // let later jobs run ahead of a blocked head job if they cannot delay it
	Preemption bool // let a blocked job evict lower-priority jobs
}

// Default uses every feature.
var Default = Policy{
	Name: "best fit + backfill + preemption", Placement: topo.BestFit,
	Priorities: true, FairShare: true, Backfill: true, Preemption: true,
}

// Reservation is the slice and time at which the blocked head of the queue will start at the
// latest, if running jobs end on time. Backfilled jobs never delay it.
type Reservation struct {
	JobID string
	At    time.Duration
	Box   topo.Box
}

// Stats are cumulative counters.
type Stats struct {
	Passes          int
	Started         int
	Backfilled      int
	Preemptions     int
	LostChipSeconds int64 // work thrown away by preemption, in chip-seconds
	// FragmentationDelayed counts jobs that were blocked at the head of the queue while enough
	// chips were free, because no free slice of the right shape existed.
	FragmentationDelayed int
}

// TenantUsage is a tenant's quota and current use.
type TenantUsage struct {
	Tenant
	Used    int `json:"used"`
	Running int `json:"running"`
	Queued  int `json:"queued"`
}

var (
	// ErrNotFound is returned for an unknown job ID.
	ErrNotFound = errors.New("job not found")
	// ErrFinished is returned when cancelling a job that already finished.
	ErrFinished = errors.New("job already finished")
)

// ValidationError maps the fields of a Spec to what is wrong with them.
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string {
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	msgs := make([]string, len(keys))
	for i, k := range keys {
		msgs[i] = k + ": " + e.Fields[k]
	}
	return "invalid job: " + strings.Join(msgs, "; ")
}

type tenantState struct {
	Tenant
	used int
}

// Scheduler places jobs on one pod. It is not safe for concurrent use.
type Scheduler struct {
	grid        *topo.Grid
	policy      Policy
	tenants     map[string]*tenantState
	tenantOrder []string
	jobs        map[string]*Job
	all         []*Job
	queue       []*Job
	slots       []*Job // running job per grid owner slot
	freeSlots   []int32
	seq         int
	events      []Event
	stats       Stats
	reservation *Reservation
	dirty       bool
}

// New returns a scheduler for an idle pod.
func New(pod topo.Dims, tenants []Tenant, p Policy) *Scheduler {
	s := &Scheduler{grid: topo.NewGrid(pod), policy: p, tenants: map[string]*tenantState{}, jobs: map[string]*Job{}}
	for _, t := range tenants {
		s.tenants[t.Name] = &tenantState{Tenant: t}
		s.tenantOrder = append(s.tenantOrder, t.Name)
	}
	return s
}

// Validate checks a spec against the pod and the tenants. It maps field names to problems.
func (s *Scheduler) Validate(spec Spec) map[string]string {
	problems := map[string]string{}
	if spec.Name == "" || len(spec.Name) > 64 {
		problems["name"] = "Enter a job name of 1 to 64 characters."
	}
	if _, ok := s.tenants[spec.Tenant]; !ok {
		problems["tenant"] = "Choose one of the teams."
	}
	if spec.Priority < BestEffort || spec.Priority > Production {
		problems["priority"] = "Choose a priority."
	}
	if d := spec.Shape; d.X < 1 || d.Y < 1 || d.Z < 1 || !d.Fits(s.grid.Pod()) {
		problems["shape"] = fmt.Sprintf("Choose a slice shape that fits the %s pod.", s.grid.Pod())
	}
	if spec.Duration < time.Minute || spec.Duration > MaxDuration {
		problems["duration"] = "Enter a run time from 1 minute to 7 days."
	}
	return problems
}

// Submit queues a job. Call Schedule (or Advance) to place it.
func (s *Scheduler) Submit(spec Spec, now time.Duration) (*Job, error) {
	if problems := s.Validate(spec); len(problems) > 0 {
		return nil, &ValidationError{Fields: problems}
	}
	s.seq++
	j := &Job{Spec: spec, ID: fmt.Sprintf("j-%d", s.seq), Seq: s.seq, Submitted: now, State: Queued, slot: topo.Free}
	j.History = append(j.History, Transition{now, Queued, "submitted"})
	s.jobs[j.ID] = j
	s.all = append(s.all, j)
	s.queue = append(s.queue, j)
	s.emit(now, EventSubmitted, j, fmt.Sprintf("%s submitted: %s slice, %d chips, %s priority", j.Name, j.Shape, j.Chips(), j.Priority))
	s.dirty = true
	return j, nil
}

// Cancel stops a queued or running job.
func (s *Scheduler) Cancel(id string, now time.Duration) (*Job, error) {
	j, ok := s.jobs[id]
	if !ok {
		return nil, ErrNotFound
	}
	switch j.State {
	case Queued:
		s.removeQueued(j)
	case Running:
		s.release(j)
	default:
		return j, ErrFinished
	}
	j.State, j.Ended = Cancelled, now
	j.History = append(j.History, Transition{now, Cancelled, "cancelled"})
	s.emit(now, EventCancelled, j, j.Name+" cancelled")
	s.dirty = true
	return j, nil
}

// Advance completes every running job that ends by now, scheduling at each completion so freed
// chips are reused the moment they free up, and then schedules at now.
func (s *Scheduler) Advance(now time.Duration) {
	for {
		t, ok := s.NextCompletion()
		if !ok || t > now {
			break
		}
		for _, j := range s.slots {
			if j != nil && j.EndsAt() == t {
				s.complete(j, t)
			}
		}
		s.Schedule(t)
	}
	s.Schedule(now)
}

// NextCompletion returns when the next running job ends.
func (s *Scheduler) NextCompletion() (time.Duration, bool) {
	var next time.Duration
	found := false
	for _, j := range s.slots {
		if j != nil && (!found || j.EndsAt() < next) {
			next, found = j.EndsAt(), true
		}
	}
	return next, found
}

// Schedule places queued jobs. It does nothing if nothing changed since the last call.
func (s *Scheduler) Schedule(now time.Duration) {
	// Preemption requeues its victims, which may fit elsewhere, so passes repeat until stable.
	// Victims always have lower priority than the job that evicted them, so this terminates.
	for s.dirty {
		s.dirty = false
		s.pass(now)
	}
}

func (s *Scheduler) pass(now time.Duration) {
	s.stats.Passes++
	s.reservation = nil
	// Within a pass the pod only fills up (until a preemption), so a shape that found no free slice
	// cannot find one later in the same pass.
	noFit := map[topo.Dims]bool{}
	var res *Reservation
	for _, j := range s.ordered() {
		key := j.Shape.Canonical()
		if res == nil {
			if !noFit[key] {
				if box, ok := s.grid.Find(j.Shape, s.policy.Placement, nil); ok {
					s.start(j, box, now, "")
					continue
				}
				noFit[key] = true
			}
			if !j.fragBlocked && s.grid.FreeChips() >= j.Chips() {
				j.fragBlocked = true
				s.stats.FragmentationDelayed++
			}
			if s.policy.Preemption && s.preempt(j, now) {
				clear(noFit)
				continue
			}
			res = s.reserve(j, now)
			s.reservation = res
			if res == nil || !s.policy.Backfill {
				return
			}
			continue
		}
		// Backfill: start j now only if it cannot delay the reservation, meaning it either ends
		// before the reservation starts or stays out of the reserved slice.
		if noFit[key] {
			continue
		}
		box, ok := s.grid.Find(j.Shape, s.policy.Placement, nil)
		if !ok {
			noFit[key] = true
			continue
		}
		if now+j.Duration > res.At && box.Overlaps(res.Box, s.grid.Pod()) {
			if box, ok = s.grid.Find(j.Shape, s.policy.Placement, &res.Box); !ok {
				continue
			}
		}
		s.start(j, box, now, "backfilled ahead of "+s.jobs[res.JobID].Name)
		s.stats.Backfilled++
	}
}

// ordered returns the queue in scheduling order.
func (s *Scheduler) ordered() []*Job {
	order := slices.Clone(s.queue)
	share := map[string]float64{}
	if s.policy.FairShare {
		for name, t := range s.tenants {
			share[name] = float64(t.used) / float64(max(t.Quota, 1))
		}
	}
	slices.SortStableFunc(order, func(a, b *Job) int {
		if s.policy.Priorities && a.Priority != b.Priority {
			return cmp.Compare(b.Priority, a.Priority)
		}
		if c := cmp.Compare(share[a.Tenant], share[b.Tenant]); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Submitted, b.Submitted); c != 0 {
			return c
		}
		return cmp.Compare(a.Seq, b.Seq)
	})
	return order
}

// preempt frees a slice for j by evicting lower-priority jobs. Among the slices it could free, it
// picks the one whose victims lose the least work (chips x time since they started), then the one
// with the fewest victims. It reports whether j started.
func (s *Scheduler) preempt(j *Job, now time.Duration) bool {
	lower := 0
	for _, r := range s.slots {
		if r != nil && r.Priority < j.Priority {
			lower += r.Chips()
		}
	}
	if lower == 0 || lower+s.grid.FreeChips() < j.Chips() {
		return false
	}
	protected := topo.NewCounter(s.grid.Pod(), func(i int) bool {
		o := s.grid.OwnerAt(i)
		return o != topo.Free && s.slots[o].Priority >= j.Priority
	})
	lost := func(v *Job) int64 { return int64(v.Chips()) * int64((now-v.Started)/time.Second) }
	var (
		best              topo.Box
		bestCost, bestNum int64
		found             bool
	)
	victims := map[int32]bool{}
	s.grid.Placements(j.Shape, func(b topo.Box) bool {
		if protected.Count(b) != 0 {
			return true
		}
		clear(victims)
		s.grid.Each(b, func(i int) bool {
			if o := s.grid.OwnerAt(i); o != topo.Free {
				victims[o] = true
			}
			return true
		})
		var cost int64
		for o := range victims {
			cost += lost(s.slots[o])
		}
		if n := int64(len(victims)); !found || cost < bestCost || (cost == bestCost && n < bestNum) {
			best, bestCost, bestNum, found = b, cost, n, true
		}
		return true
	})
	if !found {
		return false
	}
	var evict []*Job
	s.grid.Each(best, func(i int) bool {
		if o := s.grid.OwnerAt(i); o != topo.Free && !slices.Contains(evict, s.slots[o]) {
			evict = append(evict, s.slots[o])
		}
		return true
	})
	slices.SortFunc(evict, func(a, b *Job) int { return cmp.Compare(a.Seq, b.Seq) })
	for _, v := range evict {
		s.stats.Preemptions++
		s.stats.LostChipSeconds += lost(v)
		s.release(v)
		v.State = Queued
		v.Preemptions++
		v.History = append(v.History, Transition{now, Queued, "preempted by " + j.Name})
		s.queue = append(s.queue, v)
		s.emit(now, EventPreempted, v, fmt.Sprintf("%s preempted by %s (%s priority)", v.Name, j.Name, j.Priority))
	}
	s.start(j, best, now, fmt.Sprintf("preempted %d lower-priority %s", len(evict), plural(len(evict), "job")))
	s.dirty = true
	return true
}

// reserve finds the earliest time, and the slice, at which j could start if no other job started
// and running jobs ended on time.
func (s *Scheduler) reserve(j *Job, now time.Duration) *Reservation {
	running := s.Running()
	slices.SortFunc(running, func(a, b *Job) int {
		if c := cmp.Compare(a.EndsAt(), b.EndsAt()); c != 0 {
			return c
		}
		return cmp.Compare(a.Seq, b.Seq)
	})
	g := s.grid.Clone()
	for i := 0; i < len(running); {
		t := running[i].EndsAt()
		for ; i < len(running) && running[i].EndsAt() == t; i++ {
			g.Release(running[i].Box, running[i].slot)
		}
		if g.FreeChips() < j.Chips() {
			continue
		}
		if box, ok := g.Find(j.Shape, s.policy.Placement, nil); ok {
			return &Reservation{JobID: j.ID, At: max(t, now), Box: box}
		}
	}
	return nil
}

func (s *Scheduler) start(j *Job, box topo.Box, now time.Duration, reason string) {
	var slot int32
	if n := len(s.freeSlots); n > 0 {
		slot, s.freeSlots = s.freeSlots[n-1], s.freeSlots[:n-1]
		s.slots[slot] = j
	} else {
		slot = int32(len(s.slots))
		s.slots = append(s.slots, j)
	}
	s.grid.Assign(box, slot)
	s.removeQueued(j)
	j.State, j.Box, j.Started, j.slot = Running, box, now, slot
	if !j.HasStarted {
		j.HasStarted, j.FirstStart = true, now
	}
	s.tenants[j.Tenant].used += j.Chips()
	s.stats.Started++
	detail := fmt.Sprintf("%s started on %d chips: %s", j.Name, j.Chips(), box)
	why := "placed on " + box.String()
	if reason != "" {
		detail += "; " + reason
		why = reason
	}
	j.History = append(j.History, Transition{now, Running, why})
	s.emit(now, EventStarted, j, detail)
}

func (s *Scheduler) complete(j *Job, now time.Duration) {
	s.release(j)
	j.State, j.Ended = Succeeded, now
	j.History = append(j.History, Transition{now, Succeeded, "finished"})
	s.emit(now, EventSucceeded, j, fmt.Sprintf("%s finished and freed %d chips", j.Name, j.Chips()))
	s.dirty = true
}

func (s *Scheduler) release(j *Job) {
	s.grid.Release(j.Box, j.slot)
	s.slots[j.slot] = nil
	s.freeSlots = append(s.freeSlots, j.slot)
	j.slot = topo.Free
	s.tenants[j.Tenant].used -= j.Chips()
	s.dirty = true
}

func (s *Scheduler) removeQueued(j *Job) {
	if i := slices.Index(s.queue, j); i >= 0 {
		s.queue = slices.Delete(s.queue, i, i+1)
	}
}

func (s *Scheduler) emit(at time.Duration, t EventType, j *Job, detail string) {
	s.events = append(s.events, Event{At: at, Type: t, JobID: j.ID, Name: j.Name, Detail: detail})
}

// Drain returns and clears the events recorded since the last call.
func (s *Scheduler) Drain() []Event {
	ev := s.events
	s.events = nil
	return ev
}

// Job returns a job by ID.
func (s *Scheduler) Job(id string) (*Job, bool) {
	j, ok := s.jobs[id]
	return j, ok
}

// Jobs returns every job in submission order.
func (s *Scheduler) Jobs() []*Job { return s.all }

// Running returns the running jobs.
func (s *Scheduler) Running() []*Job {
	out := make([]*Job, 0, len(s.slots))
	for _, j := range s.slots {
		if j != nil {
			out = append(out, j)
		}
	}
	return out
}

// QueueLen returns the number of queued jobs.
func (s *Scheduler) QueueLen() int { return len(s.queue) }

// QueuePosition returns a queued job's 1-based position in scheduling order, or 0.
func (s *Scheduler) QueuePosition(id string) int {
	for i, j := range s.ordered() {
		if j.ID == id {
			return i + 1
		}
	}
	return 0
}

// Reservation returns the reservation made by the last scheduling pass, if any.
func (s *Scheduler) Reservation() (Reservation, bool) {
	if s.reservation == nil {
		return Reservation{}, false
	}
	return *s.reservation, true
}

// Grid returns the pod's chip map. Callers must not modify it.
func (s *Scheduler) Grid() *topo.Grid { return s.grid }

// JobAt returns the job running on the chip with the given linear index, or nil.
func (s *Scheduler) JobAt(i int) *Job {
	if o := s.grid.OwnerAt(i); o != topo.Free {
		return s.slots[o]
	}
	return nil
}

// Tenants returns each tenant's quota and current use, in the order they were configured.
func (s *Scheduler) Tenants() []TenantUsage {
	out := make([]TenantUsage, 0, len(s.tenantOrder))
	for _, name := range s.tenantOrder {
		u := TenantUsage{Tenant: s.tenants[name].Tenant, Used: s.tenants[name].used}
		for _, j := range s.all {
			if j.Tenant != name {
				continue
			}
			switch j.State {
			case Running:
				u.Running++
			case Queued:
				u.Queued++
			}
		}
		out = append(out, u)
	}
	return out
}

// Stats returns cumulative counters.
func (s *Scheduler) Stats() Stats { return s.stats }

// Policy returns the scheduling policy.
func (s *Scheduler) Policy() Policy { return s.policy }

// CheckInvariants verifies the scheduler's internal consistency. Tests and fuzzers call it.
func (s *Scheduler) CheckInvariants() error {
	used := map[string]int{}
	busy := 0
	for slot, j := range s.slots {
		if j == nil {
			continue
		}
		if j.State != Running || j.slot != int32(slot) {
			return fmt.Errorf("slot %d holds %s in state %s with slot %d", slot, j.ID, j.State, j.slot)
		}
		var err error
		s.grid.Each(j.Box, func(i int) bool {
			if o := s.grid.OwnerAt(i); o != j.slot {
				err = fmt.Errorf("chip %d of %s (%s) is held by slot %d", i, j.ID, j.Box, o)
			}
			return err == nil
		})
		if err != nil {
			return err
		}
		busy += j.Chips()
		used[j.Tenant] += j.Chips()
	}
	if busy+s.grid.FreeChips() != s.grid.Chips() {
		return fmt.Errorf("running jobs hold %d chips and %d are free, but the pod has %d", busy, s.grid.FreeChips(), s.grid.Chips())
	}
	for name, t := range s.tenants {
		if t.used != used[name] {
			return fmt.Errorf("tenant %s uses %d chips by its counter but %d by its running jobs", name, t.used, used[name])
		}
	}
	queued := 0
	for _, j := range s.all {
		switch j.State {
		case Queued:
			queued++
			if !slices.Contains(s.queue, j) {
				return fmt.Errorf("%s is queued but not in the queue", j.ID)
			}
		case Running:
			if j.slot == topo.Free || s.slots[j.slot] != j {
				return fmt.Errorf("%s is running but holds no slot", j.ID)
			}
		}
	}
	if queued != len(s.queue) {
		return fmt.Errorf("%d jobs are queued but the queue has %d", queued, len(s.queue))
	}
	return nil
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
