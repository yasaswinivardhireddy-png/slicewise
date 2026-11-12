// Package topo models a pod of accelerator chips wired as a 3D torus, and finds slices for jobs.
//
// A slice is an axis-aligned box of chips. On a torus every axis wraps around, so a box may cross
// the edge of the coordinate space and still be contiguous on the interconnect.
package topo

import (
	"fmt"
	"strconv"
	"strings"
)

// MaxSide is the largest size accepted for one side of a pod or slice.
const MaxSide = 64

// Dims is a size along the three torus axes.
type Dims struct {
	X int `json:"x"`
	Y int `json:"y"`
	Z int `json:"z"`
}

// Volume is the number of chips in a box of these dimensions.
func (d Dims) Volume() int { return d.X * d.Y * d.Z }

func (d Dims) String() string { return fmt.Sprintf("%dx%dx%d", d.X, d.Y, d.Z) }

// Canonical returns the dimensions sorted ascending, so rotations of one shape compare equal.
func (d Dims) Canonical() Dims {
	a, b, c := d.X, d.Y, d.Z
	if a > b {
		a, b = b, a
	}
	if b > c {
		b, c = c, b
	}
	if a > b {
		a, b = b, a
	}
	return Dims{a, b, c}
}

// ParseDims parses a shape written as "AxBxC", for example "2x2x4".
func ParseDims(s string) (Dims, error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(s)), "x")
	if len(parts) != 3 {
		return Dims{}, fmt.Errorf("shape %q must look like 2x2x4", s)
	}
	var v [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > MaxSide {
			return Dims{}, fmt.Errorf("shape %q must have three whole numbers from 1 to %d", s, MaxSide)
		}
		v[i] = n
	}
	return Dims{v[0], v[1], v[2]}, nil
}

// Orientations returns the distinct rotations of d that fit inside pod, in a fixed order.
func (d Dims) Orientations(pod Dims) []Dims {
	perms := [6]Dims{
		{d.X, d.Y, d.Z}, {d.X, d.Z, d.Y}, {d.Y, d.X, d.Z},
		{d.Y, d.Z, d.X}, {d.Z, d.X, d.Y}, {d.Z, d.Y, d.X},
	}
	out := make([]Dims, 0, len(perms))
	for _, p := range perms {
		if p.X > pod.X || p.Y > pod.Y || p.Z > pod.Z {
			continue
		}
		seen := false
		for _, q := range out {
			seen = seen || q == p
		}
		if !seen {
			out = append(out, p)
		}
	}
	return out
}

// Fits reports whether some rotation of d fits inside pod.
func (d Dims) Fits(pod Dims) bool { return len(d.Orientations(pod)) > 0 }

// Point is a chip coordinate.
type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
	Z int `json:"z"`
}

// Box is a slice of the torus: an origin and a size. Coordinates wrap around each axis.
type Box struct {
	Origin Point `json:"origin"`
	Size   Dims  `json:"size"`
}

func (b Box) String() string {
	return fmt.Sprintf("%s at (%d,%d,%d)", b.Size, b.Origin.X, b.Origin.Y, b.Origin.Z)
}

// Overlaps reports whether two boxes share a chip on a torus of the given dimensions.
func (b Box) Overlaps(o Box, pod Dims) bool {
	return ringOverlap(b.Origin.X, b.Size.X, o.Origin.X, o.Size.X, pod.X) &&
		ringOverlap(b.Origin.Y, b.Size.Y, o.Origin.Y, o.Size.Y, pod.Y) &&
		ringOverlap(b.Origin.Z, b.Size.Z, o.Origin.Z, o.Size.Z, pod.Z)
}

// ringOverlap reports whether [a, a+la) and [b, b+lb) intersect on a ring of n positions.
// Two arcs intersect exactly when one starts inside the other.
func ringOverlap(a, la, b, lb, n int) bool {
	return mod(b-a, n) < la || mod(a-b, n) < lb
}

func mod(a, n int) int {
	a %= n
	if a < 0 {
		a += n
	}
	return a
}
