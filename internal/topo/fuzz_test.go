package topo

import (
	"math/rand/v2"
	"testing"
)

func FuzzParseDims(f *testing.F) {
	for _, s := range []string{"2x2x4", "1x1x1", "64x64x64", "0x1x1", "x", "2x2x2x2", "+3x2x1"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d, err := ParseDims(s)
		if err != nil {
			return
		}
		if d.X < 1 || d.Y < 1 || d.Z < 1 || d.X > MaxSide || d.Y > MaxSide || d.Z > MaxSide {
			t.Fatalf("ParseDims(%q) accepted out-of-range %v", s, d)
		}
		if again, err := ParseDims(d.String()); err != nil || again != d {
			t.Fatalf("round trip of %v gave %v, %v", d, again, err)
		}
	})
}

// FuzzGrid applies random assign and release sequences and checks the grid against brute force.
func FuzzGrid(f *testing.F) {
	f.Add(uint64(1), uint8(4), uint8(4), uint8(4), []byte{1, 2, 3, 200, 17, 99})
	f.Add(uint64(7), uint8(8), uint8(1), uint8(3), []byte{255, 0, 128, 64})
	f.Fuzz(func(t *testing.T, seed uint64, x, y, z uint8, ops []byte) {
		pod := Dims{1 + int(x)%8, 1 + int(y)%8, 1 + int(z)%8}
		g := NewGrid(pod)
		r := rand.New(rand.NewPCG(seed, uint64(len(ops))))
		held := map[int32]Box{}
		var order []int32
		for i, op := range ops {
			owner := int32(i)
			if op%3 == 0 && len(order) > 0 {
				o := order[0]
				order = order[1:]
				g.Release(held[o], o)
				delete(held, o)
			} else {
				size := Dims{1 + r.IntN(pod.X), 1 + r.IntN(pod.Y), 1 + r.IntN(pod.Z)}
				strategy := Strategy(op % 2)
				b, ok := g.Find(size, strategy, nil)
				if ok {
					if b.Size.Canonical() != size.Canonical() || !g.IsFree(b) {
						t.Fatalf("Find(%v) returned %v, which is the wrong shape or busy", size, b)
					}
					g.Assign(b, owner)
					held[owner] = b
					order = append(order, owner)
				} else if bruteFits(g, size) {
					t.Fatalf("Find(%v, %v) found nothing, but a free placement exists", size, strategy)
				}
			}
			busy := 0
			for c := 0; c < g.Chips(); c++ {
				if g.OwnerAt(c) != Free {
					busy++
				}
			}
			if busy+g.FreeChips() != g.Chips() {
				t.Fatalf("busy %d + free %d != chips %d", busy, g.FreeChips(), g.Chips())
			}
		}
	})
}

func bruteFits(g *Grid, size Dims) bool {
	for _, o := range size.Orientations(g.Pod()) {
		for z := 0; z < g.Pod().Z; z++ {
			for y := 0; y < g.Pod().Y; y++ {
				for x := 0; x < g.Pod().X; x++ {
					free := true
					g.Each(Box{Point{x, y, z}, o}, func(i int) bool {
						free = g.OwnerAt(i) == Free
						return free
					})
					if free {
						return true
					}
				}
			}
		}
	}
	return false
}
