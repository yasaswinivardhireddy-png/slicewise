package topo

import (
	"math/rand/v2"
	"testing"
)

func TestParseDims(t *testing.T) {
	for in, want := range map[string]Dims{"2x2x4": {2, 2, 4}, " 4X4X8 ": {4, 4, 8}, "1x1x1": {1, 1, 1}} {
		got, err := ParseDims(in)
		if err != nil || got != want {
			t.Errorf("ParseDims(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "2x2", "2x2x2x2", "0x2x2", "2x-1x2", "axbxc", "2x2x65", "2.5x2x2"} {
		if _, err := ParseDims(in); err == nil {
			t.Errorf("ParseDims(%q) succeeded; want an error", in)
		}
	}
}

func TestOrientations(t *testing.T) {
	pod := Dims{8, 8, 4}
	if got := (Dims{2, 2, 2}).Orientations(pod); len(got) != 1 {
		t.Errorf("a cube has one orientation, got %v", got)
	}
	if got := (Dims{2, 4, 8}).Orientations(pod); len(got) != 4 {
		// 8 cannot lie along Z (size 4), which rules out 2 of the 6 permutations.
		t.Errorf("2x4x8 in 8x8x4: got %v, want 4 orientations", got)
	}
	if (Dims{8, 8, 8}).Fits(pod) {
		t.Error("8x8x8 must not fit in 8x8x4")
	}
	if got := (Dims{4, 2, 1}).Canonical(); got != (Dims{1, 2, 4}) {
		t.Errorf("Canonical = %v", got)
	}
}

// ringOverlap must agree with intersecting the two arcs as sets.
func TestRingOverlapMatchesSets(t *testing.T) {
	for n := 1; n <= 6; n++ {
		for a := 0; a < n; a++ {
			for la := 1; la <= n; la++ {
				for b := 0; b < n; b++ {
					for lb := 1; lb <= n; lb++ {
						in := make([]bool, n)
						for i := 0; i < la; i++ {
							in[(a+i)%n] = true
						}
						want := false
						for i := 0; i < lb; i++ {
							want = want || in[(b+i)%n]
						}
						if got := ringOverlap(a, la, b, lb, n); got != want {
							t.Fatalf("ringOverlap(%d,%d,%d,%d,%d) = %v, want %v", a, la, b, lb, n, got, want)
						}
					}
				}
			}
		}
	}
}

func randomBox(r *rand.Rand, pod Dims) Box {
	return Box{
		Origin: Point{r.IntN(3*pod.X) - pod.X, r.IntN(3*pod.Y) - pod.Y, r.IntN(3*pod.Z) - pod.Z},
		Size:   Dims{1 + r.IntN(pod.X), 1 + r.IntN(pod.Y), 1 + r.IntN(pod.Z)},
	}
}

// The prefix-sum Counter must match a brute-force count for boxes that wrap around every edge.
func TestCounterMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for _, pod := range []Dims{{3, 4, 5}, {1, 1, 7}, {4, 4, 4}, {8, 2, 3}} {
		marked := make([]bool, pod.Volume())
		for i := range marked {
			marked[i] = r.IntN(3) == 0
		}
		c := NewCounter(pod, func(i int) bool { return marked[i] })
		g := NewGrid(pod)
		for range 2000 {
			b := randomBox(r, pod)
			want := 0
			g.Each(b, func(i int) bool {
				if marked[i] {
					want++
				}
				return true
			})
			if got := c.Count(b); got != want {
				t.Fatalf("pod %s, box %s: Count = %d, brute force = %d", pod, b, got, want)
			}
		}
	}
}

func TestAssignReleaseAndFind(t *testing.T) {
	g := NewGrid(Dims{4, 4, 4})
	b, ok := g.Find(Dims{2, 2, 4}, FirstFit, nil)
	if !ok || b.Size.Volume() != 16 || !g.IsFree(b) {
		t.Fatalf("Find = %v, %v", b, ok)
	}
	g.Assign(b, 7)
	if g.FreeChips() != 48 || g.IsFree(b) || g.OwnerAt(g.Index(b.Origin)) != 7 {
		t.Fatalf("after Assign: free %d, owner %d", g.FreeChips(), g.OwnerAt(g.Index(b.Origin)))
	}
	// Three more 2x2x4 columns fill the pod exactly; a fifth must not fit.
	for owner := int32(8); owner < 11; owner++ {
		next, ok := g.Find(Dims{2, 2, 4}, BestFit, nil)
		if !ok {
			t.Fatalf("slice %d did not fit", owner)
		}
		g.Assign(next, owner)
	}
	if _, ok := g.Find(Dims{1, 1, 1}, BestFit, nil); ok || g.FreeChips() != 0 {
		t.Fatalf("pod should be full, free = %d", g.FreeChips())
	}
	g.Release(b, 7)
	if g.FreeChips() != 16 || !g.IsFree(b) {
		t.Fatalf("after Release: free %d", g.FreeChips())
	}
}

func TestReleaseByWrongOwnerPanics(t *testing.T) {
	g := NewGrid(Dims{2, 2, 2})
	b := Box{Point{0, 0, 0}, Dims{1, 1, 1}}
	g.Assign(b, 1)
	defer func() {
		if recover() == nil {
			t.Error("Release by the wrong owner did not panic")
		}
	}()
	g.Release(b, 2)
}

// The only free 4-chip run on this ring crosses the edge (chips 6, 7, 0, 1), so the slice must wrap.
func TestFindWrapsAroundTheTorus(t *testing.T) {
	g := NewGrid(Dims{8, 1, 1})
	g.Assign(Box{Point{2, 0, 0}, Dims{4, 1, 1}}, 1)
	b, ok := g.Find(Dims{4, 1, 1}, FirstFit, nil)
	if !ok || b.Origin.X != 6 {
		t.Fatalf("Find = %v, %v; want the wrapped slice starting at x=6", b, ok)
	}
	g.Assign(b, 2)
	for _, x := range []int{6, 7, 0, 1} {
		if got := g.OwnerAt(g.Index(Point{x, 0, 0})); got != 2 {
			t.Errorf("chip %d owner = %d, want 2", x, got)
		}
	}
}

// First fit drops a 1-chip job into the big hole and splits it; best fit uses the 1-chip gap, which
// keeps room for a 5-chip job.
func TestBestFitAvoidsFragmentation(t *testing.T) {
	g := NewGrid(Dims{8, 1, 1})
	g.Assign(Box{Point{5, 0, 0}, Dims{1, 1, 1}}, 1)
	g.Assign(Box{Point{7, 0, 0}, Dims{1, 1, 1}}, 2)

	first, best := g.Clone(), g.Clone()
	fb, _ := first.Find(Dims{1, 1, 1}, FirstFit, nil)
	bb, _ := best.Find(Dims{1, 1, 1}, BestFit, nil)
	if fb.Origin.X != 0 || bb.Origin.X != 6 {
		t.Fatalf("first fit chose x=%d (want 0), best fit chose x=%d (want 6)", fb.Origin.X, bb.Origin.X)
	}
	first.Assign(fb, 3)
	best.Assign(bb, 3)
	if _, ok := first.Find(Dims{5, 1, 1}, FirstFit, nil); ok {
		t.Error("after first fit, a 5-chip slice should no longer fit")
	}
	if _, ok := best.Find(Dims{5, 1, 1}, BestFit, nil); !ok {
		t.Error("after best fit, a 5-chip slice should still fit")
	}
}

func TestFindAvoidsReservedBox(t *testing.T) {
	g := NewGrid(Dims{4, 1, 1})
	avoid := Box{Point{0, 0, 0}, Dims{2, 1, 1}}
	b, ok := g.Find(Dims{2, 1, 1}, BestFit, &avoid)
	if !ok || b.Overlaps(avoid, g.Pod()) {
		t.Fatalf("Find = %v, %v; must not overlap %v", b, ok, avoid)
	}
	if _, ok := g.Find(Dims{3, 1, 1}, BestFit, &avoid); ok {
		t.Error("a 3-chip slice cannot avoid a 2-chip reservation on a 4-chip ring")
	}
}

func TestFullAxisPlacementsAreNotRepeated(t *testing.T) {
	g := NewGrid(Dims{4, 4, 4})
	n := 0
	g.Placements(Dims{4, 4, 1}, func(Box) bool { n++; return true })
	// Orientations 4x4x1, 4x1x4 and 1x4x4, each with 4 distinct positions along its short axis.
	if n != 12 {
		t.Errorf("got %d placements, want 12", n)
	}
}

func BenchmarkFindBestFit(b *testing.B) {
	for _, pod := range []Dims{{8, 8, 8}, {16, 16, 16}} {
		b.Run(pod.String(), func(b *testing.B) {
			g := NewGrid(pod)
			r := rand.New(rand.NewPCG(3, 4))
			// Fill about half of the pod with small slices at random positions.
			for owner := int32(0); g.FreeChips() > g.Chips()/2; {
				box := Box{Point{r.IntN(pod.X), r.IntN(pod.Y), r.IntN(pod.Z)}, Dims{1 + r.IntN(2), 1 + r.IntN(2), 1 + r.IntN(4)}}
				if g.IsFree(box) {
					g.Assign(box, owner)
					owner++
				}
			}
			b.ResetTimer()
			for range b.N {
				g.Find(Dims{2, 2, 4}, BestFit, nil)
			}
		})
	}
}
