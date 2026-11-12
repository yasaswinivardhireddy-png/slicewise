package sim

import (
	"math"
	"testing"

	"github.com/yasaswinivardhireddy-png/slicewise/internal/sched"
	"github.com/yasaswinivardhireddy-png/slicewise/internal/topo"
)

var testConfig = Config{Seed: 7, Jobs: 400, Load: 0.9, Pod: topo.Dims{X: 8, Y: 8, Z: 4}}

func TestGenerateIsDeterministicAndOffersTheRequestedLoad(t *testing.T) {
	a, err := Generate(testConfig)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Generate(testConfig)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("arrival %d differs between runs with the same seed: %+v vs %+v", i, a[i], b[i])
		}
	}
	work := 0.0
	for i, x := range a {
		if i > 0 && x.At < a[i-1].At {
			t.Fatalf("arrivals out of order at %d", i)
		}
		if !x.Spec.Shape.Fits(testConfig.Pod) {
			t.Fatalf("%s does not fit the pod", x.Spec.Shape)
		}
		work += float64(x.Spec.Shape.Volume()) * x.Spec.Duration.Seconds()
	}
	offered := work / (float64(testConfig.Pod.Volume()) * a[len(a)-1].At.Seconds())
	if math.Abs(offered-testConfig.Load) > 0.15 {
		t.Errorf("offered load %.2f, want about %.2f", offered, testConfig.Load)
	}
}

// Every policy must run every job to completion while the scheduler stays consistent.
func TestEveryPolicyCompletesTheWorkload(t *testing.T) {
	arrivals, err := Generate(testConfig)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range Policies {
		var finished int
		res, err := run(testConfig.Pod, p, arrivals, func(s *sched.Scheduler) error {
			finished = 0
			for _, j := range s.Jobs() {
				if j.State == sched.Succeeded {
					finished++
				}
			}
			return s.CheckInvariants()
		})
		if err != nil {
			t.Fatalf("%s: %v", p.Name, err)
		}
		if finished != len(arrivals) {
			t.Fatalf("%s: %d of %d jobs finished", p.Name, finished, len(arrivals))
		}
		if res.Utilization <= 0 || res.Utilization > 1 || res.P95Wait < res.MeanWait/10 {
			t.Errorf("%s: implausible result %+v", p.Name, res)
		}
		if !p.Preemption && res.Preemptions != 0 || !p.Backfill && res.Backfilled != 0 {
			t.Errorf("%s: preempted %d and backfilled %d jobs with those features off", p.Name, res.Preemptions, res.Backfilled)
		}
	}
}

func TestCompareIsDeterministic(t *testing.T) {
	c := testConfig
	c.Jobs = 150
	a, err := Compare(c)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Compare(c)
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("policy %s: %+v vs %+v", a[i].Policy, a[i], b[i])
		}
	}
	if md := Markdown(a); len(md) == 0 {
		t.Error("empty table")
	}
}

func TestGenerateRejectsBadConfigs(t *testing.T) {
	for _, c := range []Config{
		{Jobs: 0, Load: 1, Pod: topo.Dims{X: 4, Y: 4, Z: 4}},
		{Jobs: 10, Load: 0, Pod: topo.Dims{X: 4, Y: 4, Z: 4}},
		{Jobs: 10, Load: 1, Pod: topo.Dims{X: 1, Y: 1, Z: 1}},
	} {
		if _, err := Generate(c); err == nil {
			t.Errorf("Generate(%+v) succeeded", c)
		}
	}
}

func BenchmarkCompare(b *testing.B) {
	c := Config{Seed: 1, Jobs: 1000, Load: 0.9, Pod: topo.Dims{X: 8, Y: 8, Z: 8}}
	for range b.N {
		if _, err := Compare(c); err != nil {
			b.Fatal(err)
		}
	}
}
