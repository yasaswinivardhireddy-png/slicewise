package server

import (
	"context"
	"time"

	"github.com/yasaswinivardhireddy-png/slicewise/internal/sim"
	"github.com/yasaswinivardhireddy-png/slicewise/internal/topo"
)

// demoShapes keeps every demo job at a quarter of an 8x8x4 pod or less.
var demoShapes = []sim.Shape{
	{Dims: topo.Dims{X: 2, Y: 2, Z: 1}, Weight: 30},
	{Dims: topo.Dims{X: 2, Y: 2, Z: 2}, Weight: 28},
	{Dims: topo.Dims{X: 2, Y: 2, Z: 4}, Weight: 20},
	{Dims: topo.Dims{X: 2, Y: 4, Z: 4}, Weight: 14},
	{Dims: topo.Dims{X: 4, Y: 4, Z: 4}, Weight: 8},
}

// RunDemo keeps the pod busy with a synthetic workload until ctx is done. Enough jobs arrive at
// once to fill three quarters of the pod, so the console has something to show immediately; the
// rest arrive as simulated time passes. Demo jobs use at most a quarter of the pod each.
func (s *Server) RunDemo(ctx context.Context, seed uint64, every time.Duration) {
	var shapes []sim.Shape
	for _, sh := range demoShapes {
		if sh.Dims.Volume() <= s.opts.Pod.Volume()/4 {
			shapes = append(shapes, sh)
		}
	}
	cfg := sim.Config{Seed: seed, Jobs: 500, Load: 0.85, Pod: s.opts.Pod, Shapes: shapes, MinRun: 20 * time.Minute, MaxRun: 6 * time.Hour}
	batch := func() []sim.Arrival {
		a, err := sim.Generate(cfg)
		if err != nil {
			s.log.Error("demo workload", "error", err)
			return nil
		}
		cfg.Seed++
		return a
	}
	pending := batch()
	if len(pending) == 0 {
		return
	}
	warm, chips := 0, 0
	for warm < len(pending)-1 && chips < s.opts.Pod.Volume()*3/4 {
		chips += pending[warm].Spec.Shape.Volume()
		warm++
	}
	for _, a := range pending[:warm] {
		_, _ = s.Submit(a.Spec)
	}
	pending = pending[warm:]
	offset := s.opts.Clock.Now() - pending[0].At

	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := s.opts.Clock.Now()
		for len(pending) > 0 && offset+pending[0].At <= now {
			_, _ = s.Submit(pending[0].Spec)
			pending = pending[1:]
		}
		if len(pending) == 0 {
			if pending = batch(); len(pending) == 0 {
				return
			}
			offset = now - pending[0].At
		}
	}
}
