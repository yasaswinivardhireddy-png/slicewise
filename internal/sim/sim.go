// Package sim replays a synthetic workload through the scheduler under different policies and
// measures utilization, queue waits, preemptions and fragmentation.
package sim

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yasaswinivardhireddy-png/slicewise/internal/sched"
	"github.com/yasaswinivardhireddy-png/slicewise/internal/topo"
)

// Shape is a slice shape and its weight in the workload mix.
type Shape struct {
	Dims   topo.Dims
	Weight int
}

// DefaultShapes is the slice mix: many small jobs and a few very large ones.
var DefaultShapes = []Shape{
	{topo.Dims{X: 2, Y: 2, Z: 1}, 26},
	{topo.Dims{X: 2, Y: 2, Z: 2}, 24},
	{topo.Dims{X: 2, Y: 2, Z: 4}, 18},
	{topo.Dims{X: 2, Y: 4, Z: 4}, 14},
	{topo.Dims{X: 4, Y: 4, Z: 4}, 10},
	{topo.Dims{X: 4, Y: 4, Z: 8}, 5},
	{topo.Dims{X: 4, Y: 8, Z: 8}, 3},
}

// Config describes a synthetic workload.
type Config struct {
	Seed   uint64
	Jobs   int
	Load   float64 // requested chip-time divided by the pod's chip-time while jobs arrive
	Pod    topo.Dims
	Shapes []Shape // nil means DefaultShapes; shapes that do not fit the pod are dropped
	MinRun time.Duration
	MaxRun time.Duration
}

// Arrival is a job submitted at a point in time.
type Arrival struct {
	At   time.Duration
	Spec sched.Spec
}

// Tenants are the teams of the synthetic workload; their quotas split the pod 40/30/20/10.
func Tenants(pod topo.Dims) []sched.Tenant {
	n := pod.Volume()
	return []sched.Tenant{
		{Name: "research", Quota: n * 4 / 10},
		{Name: "serving", Quota: n * 3 / 10},
		{Name: "ads", Quota: n * 2 / 10},
		{Name: "cloud", Quota: max(n/10, 1)},
	}
}

var jobKinds = []string{"train", "finetune", "eval", "embed", "distill", "sweep"}

// Generate builds a workload: shapes from the mix, run times log-uniform between MinRun and MaxRun
// (default 15 minutes to 8 hours), 15% production, 60% batch and 25% best-effort jobs, tenants in
// proportion to their quotas, and Poisson arrivals at the rate that offers the requested load.
func Generate(c Config) ([]Arrival, error) {
	if c.Jobs < 1 || c.Load <= 0 {
		return nil, errors.New("sim: need at least one job and a positive load")
	}
	minRun, maxRun := cmp.Or(c.MinRun, 15*time.Minute), cmp.Or(c.MaxRun, 8*time.Hour)
	mix := c.Shapes
	if mix == nil {
		mix = DefaultShapes
	}
	var shapes []Shape
	total := 0
	for _, s := range mix {
		if s.Dims.Fits(c.Pod) && s.Weight > 0 {
			shapes = append(shapes, s)
			total += s.Weight
		}
	}
	if len(shapes) == 0 {
		return nil, fmt.Errorf("sim: no shape in the mix fits the %s pod", c.Pod)
	}
	tenants := Tenants(c.Pod)
	quotaSum := 0
	for _, t := range tenants {
		quotaSum += t.Quota
	}

	r := rand.New(rand.NewPCG(c.Seed, 0x5eed))
	out := make([]Arrival, c.Jobs)
	work := 0.0
	for i := range out {
		pick := r.IntN(total)
		shape := shapes[0].Dims
		for _, s := range shapes {
			if pick < s.Weight {
				shape = s.Dims
				break
			}
			pick -= s.Weight
		}
		run := math.Exp(math.Log(minRun.Seconds()) + r.Float64()*(math.Log(maxRun.Seconds())-math.Log(minRun.Seconds())))
		duration := time.Duration(run) * time.Second
		priority := sched.BestEffort
		switch p := r.IntN(100); {
		case p < 15:
			priority = sched.Production
		case p < 75:
			priority = sched.Batch
		}
		tenant, q := tenants[len(tenants)-1].Name, r.IntN(quotaSum)
		for _, t := range tenants {
			if q < t.Quota {
				tenant = t.Name
				break
			}
			q -= t.Quota
		}
		out[i].Spec = sched.Spec{
			Name: fmt.Sprintf("%s-%d", jobKinds[r.IntN(len(jobKinds))], i+1), Tenant: tenant,
			Priority: priority, Shape: shape, Duration: duration,
		}
		work += float64(shape.Volume()) * duration.Seconds()
	}
	meanGap := work / float64(c.Jobs) / (c.Load * float64(c.Pod.Volume()))
	t := 0.0
	for i := range out {
		t += r.ExpFloat64() * meanGap
		out[i].At = time.Duration(t) * time.Second
	}
	return out, nil
}

// Result summarizes one policy's run over a workload.
type Result struct {
	Policy string
	// Utilization is busy chip-time divided by pod chip-time while jobs were arriving.
	Utilization          float64
	MeanWait             time.Duration
	P95Wait              time.Duration
	ProductionP95Wait    time.Duration
	Makespan             time.Duration
	Backfilled           int
	Preemptions          int
	LostChipHours        float64
	FragmentationDelayed int
}

// Policies are the configurations Compare runs, from the simplest to Slicewise's default.
var Policies = []sched.Policy{
	{Name: "FIFO, first fit", Placement: topo.FirstFit},
	{Name: "Priorities, first fit", Placement: topo.FirstFit, Priorities: true},
	{Name: "Priorities, best fit", Placement: topo.BestFit, Priorities: true},
	{Name: "Priorities, best fit, backfill", Placement: topo.BestFit, Priorities: true, Backfill: true},
	{Name: "All: adds fair share and preemption", Placement: topo.BestFit, Priorities: true, Backfill: true, FairShare: true, Preemption: true},
}

// Run replays arrivals through a scheduler with policy p.
func Run(pod topo.Dims, p sched.Policy, arrivals []Arrival) (Result, error) {
	return run(pod, p, arrivals, nil)
}

func run(pod topo.Dims, p sched.Policy, arrivals []Arrival, step func(*sched.Scheduler) error) (Result, error) {
	if len(arrivals) == 0 {
		return Result{}, errors.New("sim: empty workload")
	}
	s := sched.New(pod, Tenants(pod), p)
	window := arrivals[len(arrivals)-1].At
	busyArea, busy, prev := 0.0, 0, time.Duration(0)
	account := func(t time.Duration) {
		if prev < window {
			busyArea += float64(busy) * (min(t, window) - prev).Seconds()
		}
		prev = t
	}
	for i := 0; i < len(arrivals) || s.QueueLen() > 0 || len(s.Running()) > 0; {
		next, ok := s.NextCompletion()
		switch {
		case i < len(arrivals) && (!ok || arrivals[i].At < next):
			t := arrivals[i].At
			account(t)
			for ; i < len(arrivals) && arrivals[i].At == t; i++ {
				if _, err := s.Submit(arrivals[i].Spec, t); err != nil {
					return Result{}, err
				}
			}
			s.Schedule(t)
		case ok:
			account(next)
			s.Advance(next)
		default:
			return Result{}, fmt.Errorf("sim: %d jobs are queued but nothing is running", s.QueueLen())
		}
		busy = pod.Volume() - s.Grid().FreeChips()
		s.Drain()
		if step != nil {
			if err := step(s); err != nil {
				return Result{}, err
			}
		}
	}

	var waits, production []time.Duration
	var last time.Duration
	for _, j := range s.Jobs() {
		w := j.FirstStart - j.Submitted
		waits = append(waits, w)
		if j.Priority == sched.Production {
			production = append(production, w)
		}
		last = max(last, j.Ended)
	}
	st := s.Stats()
	res := Result{
		Policy:               p.Name,
		MeanWait:             mean(waits),
		P95Wait:              percentile(waits, 0.95),
		ProductionP95Wait:    percentile(production, 0.95),
		Makespan:             last - arrivals[0].At,
		Backfilled:           st.Backfilled,
		Preemptions:          st.Preemptions,
		LostChipHours:        float64(st.LostChipSeconds) / 3600,
		FragmentationDelayed: st.FragmentationDelayed,
	}
	if window > 0 {
		res.Utilization = busyArea / (float64(pod.Volume()) * window.Seconds())
	}
	return res, nil
}

// Compare generates one workload and runs every policy in Policies on it, in parallel.
func Compare(c Config) ([]Result, error) {
	arrivals, err := Generate(c)
	if err != nil {
		return nil, err
	}
	results := make([]Result, len(Policies))
	errs := make([]error, len(Policies))
	var wg sync.WaitGroup
	for i, p := range Policies {
		wg.Go(func() { results[i], errs[i] = Run(c.Pod, p, arrivals) })
	}
	wg.Wait()
	return results, errors.Join(errs...)
}

// Markdown renders results as a table.
func Markdown(results []Result) string {
	var b strings.Builder
	b.WriteString("| Policy | Utilization | Mean wait | p95 wait | Production p95 wait | Backfilled | Preemptions | Work lost to preemption | Jobs delayed by fragmentation |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, r := range results {
		fmt.Fprintf(&b, "| %s | %.1f%% | %s | %s | %s | %d | %d | %.0f chip-hours | %d |\n",
			r.Policy, 100*r.Utilization, Hours(r.MeanWait), Hours(r.P95Wait), Hours(r.ProductionP95Wait),
			r.Backfilled, r.Preemptions, r.LostChipHours, r.FragmentationDelayed)
	}
	return b.String()
}

// Hours formats a duration as hours with one decimal place.
func Hours(d time.Duration) string { return fmt.Sprintf("%.1f h", d.Hours()) }

// Average combines runs of the same policies (one slice per workload) into their means.
func Average(runs [][]Result) []Result {
	if len(runs) == 0 {
		return nil
	}
	n := len(runs)
	out := make([]Result, len(runs[0]))
	for i := range out {
		out[i].Policy = runs[0][i].Policy
		for _, run := range runs {
			r := run[i]
			out[i].Utilization += r.Utilization / float64(n)
			out[i].MeanWait += r.MeanWait / time.Duration(n)
			out[i].P95Wait += r.P95Wait / time.Duration(n)
			out[i].ProductionP95Wait += r.ProductionP95Wait / time.Duration(n)
			out[i].Makespan += r.Makespan / time.Duration(n)
			out[i].LostChipHours += r.LostChipHours / float64(n)
			out[i].Backfilled += r.Backfilled
			out[i].Preemptions += r.Preemptions
			out[i].FragmentationDelayed += r.FragmentationDelayed
		}
		out[i].Backfilled = (out[i].Backfilled + n/2) / n
		out[i].Preemptions = (out[i].Preemptions + n/2) / n
		out[i].FragmentationDelayed = (out[i].FragmentationDelayed + n/2) / n
	}
	return out
}

func mean(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	var sum time.Duration
	for _, d := range ds {
		sum += d
	}
	return sum / time.Duration(len(ds))
}

func percentile(ds []time.Duration, q float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[max(int(math.Ceil(q*float64(len(s))))-1, 0)]
}
