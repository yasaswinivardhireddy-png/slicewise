package sched

import (
	"errors"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/yasaswinivardhireddy-png/slicewise/internal/topo"
)

var twoTeams = []Tenant{{Name: "a", Quota: 2}, {Name: "b", Quota: 2}}

func dims(x, y, z int) topo.Dims { return topo.Dims{X: x, Y: y, Z: z} }

func spec(name, tenant string, p Priority, chips int, d time.Duration) Spec {
	return Spec{Name: name, Tenant: tenant, Priority: p, Shape: topo.Dims{X: chips, Y: 1, Z: 1}, Duration: d}
}

func mustSubmit(t *testing.T, s *Scheduler, sp Spec, now time.Duration) *Job {
	t.Helper()
	j, err := s.Submit(sp, now)
	if err != nil {
		t.Fatalf("Submit(%s): %v", sp.Name, err)
	}
	return j
}

func check(t *testing.T, s *Scheduler) {
	t.Helper()
	if err := s.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

func TestPrioritiesReorderTheQueue(t *testing.T) {
	for _, tc := range []struct {
		priorities bool
		wantFirst  string
	}{{false, "low"}, {true, "high"}} {
		s := New(topo.Dims{X: 2, Y: 1, Z: 1}, twoTeams, Policy{Placement: topo.BestFit, Priorities: tc.priorities})
		mustSubmit(t, s, spec("busy", "a", Batch, 2, time.Hour), 0)
		s.Schedule(0)
		low := mustSubmit(t, s, spec("low", "a", BestEffort, 1, time.Hour), time.Minute)
		high := mustSubmit(t, s, spec("high", "a", Production, 2, time.Hour), 2*time.Minute)
		s.Advance(time.Hour)
		check(t, s)
		first := map[bool]string{true: "low", false: "high"}[low.State == Running]
		if first != tc.wantFirst || (low.State == Running) == (high.State == Running) {
			t.Errorf("priorities=%v: low %s, high %s; want %s to start first", tc.priorities, low.State, high.State, tc.wantFirst)
		}
	}
}

func TestFairShareFavorsTheTeamUsingLess(t *testing.T) {
	for _, tc := range []struct {
		fair bool
		want string
	}{{false, "a2"}, {true, "b1"}} {
		s := New(topo.Dims{X: 2, Y: 1, Z: 1}, []Tenant{{"a", 1}, {"b", 1}}, Policy{Placement: topo.BestFit, Priorities: true, FairShare: tc.fair})
		mustSubmit(t, s, spec("a1", "a", Batch, 1, time.Hour), 0)
		s.Schedule(0)
		a2 := mustSubmit(t, s, spec("a2", "a", Batch, 1, time.Hour), time.Minute)
		b1 := mustSubmit(t, s, spec("b1", "b", Batch, 1, time.Hour), 2*time.Minute)
		s.Schedule(2 * time.Minute)
		check(t, s)
		got := map[bool]string{true: "a2", false: "b1"}[a2.State == Running]
		if got != tc.want || (a2.State == Running) == (b1.State == Running) {
			t.Errorf("fair share %v: a2 %s, b1 %s; want %s to start", tc.fair, a2.State, b1.State, tc.want)
		}
	}
}

func TestPreemptionEvictsTheJobThatLosesLeastWork(t *testing.T) {
	s := New(topo.Dims{X: 4, Y: 1, Z: 1}, twoTeams, Default)
	old := mustSubmit(t, s, spec("old", "a", Batch, 2, 10*time.Hour), 0)
	s.Schedule(0)
	young := mustSubmit(t, s, spec("young", "b", BestEffort, 2, 10*time.Hour), 30*time.Minute)
	s.Schedule(30 * time.Minute)
	urgent := mustSubmit(t, s, spec("urgent", "a", Production, 2, time.Hour), time.Hour)
	s.Advance(time.Hour)
	check(t, s)
	if urgent.State != Running || young.State != Queued || old.State != Running {
		t.Fatalf("urgent %s, young %s, old %s; want young preempted", urgent.State, young.State, old.State)
	}
	if young.Preemptions != 1 || s.Stats().LostChipSeconds != 2*1800 {
		t.Errorf("preemptions %d, lost %d chip-seconds; want 1 and 3600", young.Preemptions, s.Stats().LostChipSeconds)
	}
	if !urgent.Box.Overlaps(young.Box, s.Grid().Pod()) {
		t.Errorf("urgent runs on %s, not on young's former slice %s", urgent.Box, young.Box)
	}
}

func TestPreemptionNeverEvictsEqualPriority(t *testing.T) {
	s := New(topo.Dims{X: 2, Y: 1, Z: 1}, twoTeams, Default)
	mustSubmit(t, s, spec("first", "a", Production, 2, time.Hour), 0)
	s.Schedule(0)
	second := mustSubmit(t, s, spec("second", "b", Production, 1, time.Hour), time.Minute)
	s.Schedule(time.Minute)
	check(t, s)
	if second.State != Queued || s.Stats().Preemptions != 0 {
		t.Errorf("second is %s after %d preemptions; want it queued", second.State, s.Stats().Preemptions)
	}
}

func TestBackfillOnlyWhenItCannotDelayTheHeadJob(t *testing.T) {
	s := New(topo.Dims{X: 4, Y: 1, Z: 1}, twoTeams, Policy{Placement: topo.BestFit, Backfill: true})
	mustSubmit(t, s, spec("running", "a", Batch, 3, time.Hour), 0)
	s.Schedule(0)
	whole := mustSubmit(t, s, spec("whole-pod", "a", Batch, 4, time.Hour), time.Minute)
	short := mustSubmit(t, s, spec("short", "b", Batch, 1, 30*time.Minute), 2*time.Minute)
	long := mustSubmit(t, s, spec("long", "b", Batch, 1, 2*time.Hour), 3*time.Minute)
	s.Schedule(3 * time.Minute)
	check(t, s)
	res, ok := s.Reservation()
	if !ok || res.JobID != whole.ID || res.At != time.Hour {
		t.Fatalf("reservation = %+v, %v; want whole-pod at 1h", res, ok)
	}
	if short.State != Running || long.State != Queued {
		t.Fatalf("short %s, long %s; want only short backfilled", short.State, long.State)
	}
	s.Advance(time.Hour)
	check(t, s)
	if whole.State != Running || whole.FirstStart != time.Hour {
		t.Errorf("whole-pod %s, started at %v; want running from 1h", whole.State, whole.FirstStart)
	}
}

func TestWithoutBackfillTheQueueWaits(t *testing.T) {
	s := New(topo.Dims{X: 4, Y: 1, Z: 1}, twoTeams, Policy{Placement: topo.FirstFit})
	mustSubmit(t, s, spec("running", "a", Batch, 3, time.Hour), 0)
	mustSubmit(t, s, spec("whole-pod", "a", Batch, 4, time.Hour), time.Minute)
	small := mustSubmit(t, s, spec("small", "b", Batch, 1, time.Minute), 2*time.Minute)
	s.Schedule(2 * time.Minute)
	if small.State != Queued {
		t.Errorf("small is %s; FIFO without backfill must not let it pass", small.State)
	}
}

func TestCancel(t *testing.T) {
	s := New(topo.Dims{X: 2, Y: 1, Z: 1}, twoTeams, Default)
	run := mustSubmit(t, s, spec("run", "a", Batch, 2, time.Hour), 0)
	wait := mustSubmit(t, s, spec("wait", "a", Batch, 2, time.Hour), 0)
	s.Schedule(0)
	if _, err := s.Cancel(wait.ID, time.Minute); err != nil || wait.State != Cancelled {
		t.Fatalf("cancel queued: %v, %s", err, wait.State)
	}
	if _, err := s.Cancel(run.ID, 2*time.Minute); err != nil || run.State != Cancelled || s.Grid().FreeChips() != 2 {
		t.Fatalf("cancel running: %v, %s, %d free", err, run.State, s.Grid().FreeChips())
	}
	if _, err := s.Cancel(run.ID, 3*time.Minute); !errors.Is(err, ErrFinished) {
		t.Errorf("second cancel: %v, want ErrFinished", err)
	}
	if _, err := s.Cancel("j-404", 3*time.Minute); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown job: %v, want ErrNotFound", err)
	}
	check(t, s)
}

func TestValidate(t *testing.T) {
	s := New(topo.Dims{X: 4, Y: 4, Z: 2}, twoTeams, Default)
	_, err := s.Submit(Spec{Name: "", Tenant: "nobody", Priority: 7, Shape: topo.Dims{X: 8, Y: 1, Z: 1}, Duration: 0}, 0)
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v, want a ValidationError", err)
	}
	for _, field := range []string{"name", "tenant", "priority", "shape", "duration"} {
		if ve.Fields[field] == "" {
			t.Errorf("no problem reported for %s: %v", field, ve.Fields)
		}
	}
	// 1x4x4 fits a 4x4x2 pod once rotated.
	if _, err := s.Submit(Spec{Name: "ok", Tenant: "a", Priority: Batch, Shape: topo.Dims{X: 1, Y: 4, Z: 4}, Duration: time.Hour}, 0); err != nil {
		t.Errorf("rotatable shape rejected: %v", err)
	}
}

// randomRun drives a scheduler with a random workload and calls observe after every step.
func randomRun(t *testing.T, seed uint64, p Policy, observe func(s *Scheduler, now time.Duration)) *Scheduler {
	t.Helper()
	r := rand.New(rand.NewPCG(seed, 99))
	pod := topo.Dims{X: 4, Y: 4, Z: 4}
	s := New(pod, []Tenant{{"a", 32}, {"b", 16}, {"c", 16}}, p)
	shapes := []topo.Dims{dims(1, 1, 1), dims(2, 1, 1), dims(2, 2, 1), dims(2, 2, 2), dims(4, 2, 2), dims(4, 4, 2), dims(4, 4, 4)}
	now := time.Duration(0)
	for i := 0; i < 300; i++ {
		now += time.Duration(r.IntN(20)) * time.Minute
		if next, ok := s.NextCompletion(); ok && next < now && r.IntN(2) == 0 {
			now = next
		}
		s.Advance(now)
		observe(s, now)
		sp := Spec{
			Name: "job", Tenant: []string{"a", "b", "c"}[r.IntN(3)], Priority: Priority(r.IntN(3)),
			Shape: shapes[r.IntN(len(shapes))], Duration: time.Duration(1+r.IntN(240)) * time.Minute,
		}
		if !p.Priorities {
			sp.Priority = Batch
		}
		mustSubmit(t, s, sp, now)
		s.Schedule(now)
		observe(s, now)
	}
	for {
		next, ok := s.NextCompletion()
		if !ok {
			break
		}
		s.Advance(next)
		observe(s, next)
	}
	return s
}

// EASY backfilling guarantees that the head of the queue starts no later than its reservation.
func TestBackfillNeverDelaysAReservation(t *testing.T) {
	for _, placement := range []topo.Strategy{topo.FirstFit, topo.BestFit} {
		for seed := uint64(1); seed <= 12; seed++ {
			reserved := map[string]time.Duration{}
			p := Policy{Placement: placement, Backfill: true}
			s := randomRun(t, seed, p, func(s *Scheduler, now time.Duration) {
				if err := s.CheckInvariants(); err != nil {
					t.Fatal(err)
				}
				if res, ok := s.Reservation(); ok {
					if at, seen := reserved[res.JobID]; !seen || res.At < at {
						reserved[res.JobID] = res.At
					}
				}
			})
			for id, at := range reserved {
				j, _ := s.Job(id)
				if !j.HasStarted || j.FirstStart > at {
					t.Fatalf("%v seed %d: %s reserved for %v but started at %v (started=%v)", placement, seed, id, at, j.FirstStart, j.HasStarted)
				}
			}
			if len(s.queue) != 0 {
				t.Fatalf("seed %d: %d jobs never ran", seed, len(s.queue))
			}
		}
	}
}

func TestEveryPolicyDrainsTheQueue(t *testing.T) {
	policies := []Policy{
		{Placement: topo.FirstFit},
		{Placement: topo.BestFit, Priorities: true},
		{Placement: topo.BestFit, Priorities: true, Backfill: true},
		Default,
	}
	for i, p := range policies {
		s := randomRun(t, uint64(100+i), p, func(s *Scheduler, _ time.Duration) {
			if err := s.CheckInvariants(); err != nil {
				t.Fatal(err)
			}
		})
		for _, j := range s.Jobs() {
			if j.State != Succeeded {
				t.Fatalf("policy %d: %s ended %s", i, j.ID, j.State)
			}
		}
	}
}

func FuzzScheduler(f *testing.F) {
	f.Add(uint64(1), []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9})
	f.Add(uint64(42), []byte{9, 9, 9, 200, 13, 250, 3})
	f.Fuzz(func(t *testing.T, seed uint64, ops []byte) {
		r := rand.New(rand.NewPCG(seed, 7))
		s := New(topo.Dims{X: 4, Y: 2, Z: 2}, []Tenant{{"a", 8}, {"b", 8}}, Default)
		now := time.Duration(0)
		for _, op := range ops {
			switch op % 4 {
			case 0, 1:
				sp := Spec{
					Name: "f", Tenant: []string{"a", "b"}[r.IntN(2)], Priority: Priority(r.IntN(3)),
					Shape:    topo.Dims{X: 1 + r.IntN(4), Y: 1 + r.IntN(2), Z: 1 + r.IntN(2)},
					Duration: time.Duration(1+r.IntN(120)) * time.Minute,
				}
				if _, err := s.Submit(sp, now); err != nil {
					t.Fatal(err)
				}
				s.Schedule(now)
			case 2:
				if jobs := s.Jobs(); len(jobs) > 0 {
					_, _ = s.Cancel(jobs[r.IntN(len(jobs))].ID, now)
					s.Schedule(now)
				}
			case 3:
				now += time.Duration(int(op)) * time.Minute
				s.Advance(now)
			}
			if err := s.CheckInvariants(); err != nil {
				t.Fatal(err)
			}
		}
	})
}
