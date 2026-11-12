package topo

import (
	"fmt"
	"slices"
)

// Counter answers "how many marked chips are in this box?" in constant time.
//
// It keeps 3D prefix sums over the pod tiled twice along every axis. Any box that fits in the pod,
// including one that wraps around an edge, is then a plain contiguous range of the tiled array,
// so its count takes eight lookups (inclusion-exclusion over the corners).
type Counter struct {
	pod    Dims
	tx, ty int
	sums   []int32
}

// NewCounter builds a Counter for the chips (by linear index) for which marked returns true.
func NewCounter(pod Dims, marked func(i int) bool) *Counter {
	mark := make([]int32, pod.Volume())
	for i := range mark {
		if marked(i) {
			mark[i] = 1
		}
	}
	tx, ty, tz := 2*pod.X+1, 2*pod.Y+1, 2*pod.Z+1
	s := make([]int32, tx*ty*tz)
	at := func(i, j, k int) int { return (k*ty+j)*tx + i }
	for k := 1; k < tz; k++ {
		z := (k - 1) % pod.Z
		for j := 1; j < ty; j++ {
			row := (z*pod.Y + (j-1)%pod.Y) * pod.X
			for i := 1; i < tx; i++ {
				s[at(i, j, k)] = mark[row+(i-1)%pod.X] +
					s[at(i-1, j, k)] + s[at(i, j-1, k)] + s[at(i, j, k-1)] -
					s[at(i-1, j-1, k)] - s[at(i-1, j, k-1)] - s[at(i, j-1, k-1)] +
					s[at(i-1, j-1, k-1)]
			}
		}
	}
	return &Counter{pod: pod, tx: tx, ty: ty, sums: s}
}

// Count returns the number of marked chips in b. Each side of b must be at most the pod's side.
func (c *Counter) Count(b Box) int {
	x0, y0, z0 := mod(b.Origin.X, c.pod.X), mod(b.Origin.Y, c.pod.Y), mod(b.Origin.Z, c.pod.Z)
	x1, y1, z1 := x0+b.Size.X, y0+b.Size.Y, z0+b.Size.Z
	at := func(i, j, k int) int32 { return c.sums[(k*c.ty+j)*c.tx+i] }
	return int(at(x1, y1, z1) - at(x0, y1, z1) - at(x1, y0, z1) - at(x1, y1, z0) +
		at(x0, y0, z1) + at(x0, y1, z0) + at(x1, y0, z0) - at(x0, y0, z0))
}

// Free marks an idle chip in a Grid.
const Free int32 = -1

// Grid records which owner (a job slot) holds each chip of a pod.
type Grid struct {
	pod   Dims
	owner []int32
	free  int
	occ   *Counter // busy-chip counts; nil after a change, rebuilt on demand
}

// NewGrid returns an idle pod of the given dimensions.
func NewGrid(pod Dims) *Grid {
	if pod.X < 1 || pod.Y < 1 || pod.Z < 1 || pod.X > MaxSide || pod.Y > MaxSide || pod.Z > MaxSide {
		panic(fmt.Sprintf("topo: invalid pod %s", pod))
	}
	g := &Grid{pod: pod, owner: make([]int32, pod.Volume()), free: pod.Volume()}
	for i := range g.owner {
		g.owner[i] = Free
	}
	return g
}

// Pod returns the pod's dimensions.
func (g *Grid) Pod() Dims { return g.pod }

// Chips returns the number of chips in the pod.
func (g *Grid) Chips() int { return len(g.owner) }

// FreeChips returns the number of idle chips.
func (g *Grid) FreeChips() int { return g.free }

// Index returns the linear index of a chip; coordinates wrap.
func (g *Grid) Index(p Point) int {
	return (mod(p.Z, g.pod.Z)*g.pod.Y+mod(p.Y, g.pod.Y))*g.pod.X + mod(p.X, g.pod.X)
}

// OwnerAt returns the owner of the chip with linear index i, or Free.
func (g *Grid) OwnerAt(i int) int32 { return g.owner[i] }

// Each calls fn with the linear index of every chip in b, stopping early if fn returns false.
func (g *Grid) Each(b Box, fn func(i int) bool) {
	for dz := 0; dz < b.Size.Z; dz++ {
		z := mod(b.Origin.Z+dz, g.pod.Z)
		for dy := 0; dy < b.Size.Y; dy++ {
			row := (z*g.pod.Y + mod(b.Origin.Y+dy, g.pod.Y)) * g.pod.X
			for dx := 0; dx < b.Size.X; dx++ {
				if !fn(row + mod(b.Origin.X+dx, g.pod.X)) {
					return
				}
			}
		}
	}
}

// Assign gives every chip in b to owner. It panics if a chip is busy; callers check first.
func (g *Grid) Assign(b Box, owner int32) {
	g.mustFit(b)
	g.Each(b, func(i int) bool {
		if g.owner[i] != Free {
			panic(fmt.Sprintf("topo: chip %d of %s is already held by %d", i, b, g.owner[i]))
		}
		g.owner[i] = owner
		return true
	})
	g.free -= b.Size.Volume()
	g.occ = nil
}

// Release frees every chip in b. It panics if a chip is not held by owner.
func (g *Grid) Release(b Box, owner int32) {
	g.mustFit(b)
	g.Each(b, func(i int) bool {
		if g.owner[i] != owner {
			panic(fmt.Sprintf("topo: chip %d of %s is held by %d, not %d", i, b, g.owner[i], owner))
		}
		g.owner[i] = Free
		return true
	})
	g.free += b.Size.Volume()
	g.occ = nil
}

func (g *Grid) mustFit(b Box) {
	s := b.Size
	if s.X < 1 || s.Y < 1 || s.Z < 1 || s.X > g.pod.X || s.Y > g.pod.Y || s.Z > g.pod.Z {
		panic(fmt.Sprintf("topo: box %s does not fit pod %s", b, g.pod))
	}
}

// Busy returns a Counter of busy chips, valid until the next Assign or Release.
func (g *Grid) Busy() *Counter {
	if g.occ == nil {
		g.occ = NewCounter(g.pod, func(i int) bool { return g.owner[i] != Free })
	}
	return g.occ
}

// IsFree reports whether every chip in b is idle.
func (g *Grid) IsFree(b Box) bool { return g.Busy().Count(b) == 0 }

// Clone returns an independent copy of the grid.
func (g *Grid) Clone() *Grid {
	c := *g
	c.owner = slices.Clone(g.owner)
	return &c
}

// Placements calls fn for every distinct placement of a slice of the given size, rotations
// included, until fn returns false. A side that spans a whole axis is placed only at origin 0 on
// that axis, because every origin would cover the same chips.
func (g *Grid) Placements(size Dims, fn func(Box) bool) {
	for _, o := range size.Orientations(g.pod) {
		nx, ny, nz := g.pod.X, g.pod.Y, g.pod.Z
		if o.X == g.pod.X {
			nx = 1
		}
		if o.Y == g.pod.Y {
			ny = 1
		}
		if o.Z == g.pod.Z {
			nz = 1
		}
		for z := 0; z < nz; z++ {
			for y := 0; y < ny; y++ {
				for x := 0; x < nx; x++ {
					if !fn(Box{Point{x, y, z}, o}) {
						return
					}
				}
			}
		}
	}
}

// Strategy chooses among the free placements for a slice.
type Strategy int

const (
	// FirstFit takes the first free placement in scan order.
	FirstFit Strategy = iota
	// BestFit takes the free placement whose faces touch the most busy chips.
	BestFit
)

func (s Strategy) String() string {
	if s == BestFit {
		return "best-fit"
	}
	return "first-fit"
}

// Find returns a free placement for a slice of the given size, or false if there is none.
// If avoid is not nil, placements that overlap it are skipped.
func (g *Grid) Find(size Dims, s Strategy, avoid *Box) (Box, bool) {
	if size.Volume() > g.free {
		return Box{}, false
	}
	busy := g.Busy()
	var best Box
	bestScore, found := -1, false
	g.Placements(size, func(b Box) bool {
		if busy.Count(b) != 0 || (avoid != nil && b.Overlaps(*avoid, g.pod)) {
			return true
		}
		if s == FirstFit {
			best, found = b, true
			return false
		}
		if score := g.contact(busy, b); score > bestScore {
			best, bestScore, found = b, score, true
		}
		return true
	})
	return best, found
}

// contact counts the busy chips touching b's six faces. A side that spans a whole axis wraps onto
// itself, so both of its faces count as fully covered. Packing slices against each other leaves the
// free chips in large contiguous blocks, where later large slices still fit.
func (g *Grid) contact(busy *Counter, b Box) int {
	o, s, p := b.Origin, b.Size, g.pod
	score := 0
	faces := func(full bool, area int, lo, hi Box) {
		if full {
			score += 2 * area
			return
		}
		score += busy.Count(lo) + busy.Count(hi)
	}
	faces(s.X == p.X, s.Y*s.Z, Box{Point{o.X - 1, o.Y, o.Z}, Dims{1, s.Y, s.Z}}, Box{Point{o.X + s.X, o.Y, o.Z}, Dims{1, s.Y, s.Z}})
	faces(s.Y == p.Y, s.X*s.Z, Box{Point{o.X, o.Y - 1, o.Z}, Dims{s.X, 1, s.Z}}, Box{Point{o.X, o.Y + s.Y, o.Z}, Dims{s.X, 1, s.Z}})
	faces(s.Z == p.Z, s.X*s.Y, Box{Point{o.X, o.Y, o.Z - 1}, Dims{s.X, s.Y, 1}}, Box{Point{o.X, o.Y, o.Z + s.Z}, Dims{s.X, s.Y, 1}})
	return score
}
