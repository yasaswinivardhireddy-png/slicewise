// Command slicewise schedules ML jobs onto slices of an accelerator pod and serves a web console,
// or compares scheduling policies on synthetic workloads.
//
//	slicewise [serve] [flags]     serve the API and the console
//	slicewise simulate [flags]    print a policy comparison as a Markdown table
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yasaswinivardhireddy-png/slicewise/internal/sched"
	"github.com/yasaswinivardhireddy-png/slicewise/internal/server"
	"github.com/yasaswinivardhireddy-png/slicewise/internal/sim"
	"github.com/yasaswinivardhireddy-png/slicewise/internal/topo"
	"github.com/yasaswinivardhireddy-png/slicewise/internal/webui"
)

func main() {
	args := os.Args[1:]
	var err error
	switch {
	case len(args) > 0 && args[0] == "simulate":
		err = simulate(args[1:])
	case len(args) > 0 && args[0] == "serve":
		err = serve(args[1:])
	default:
		err = serve(args)
	}
	if err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "slicewise:", err)
		os.Exit(1)
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8080", "listen address")
	podFlag := fs.String("pod", "8x8x4", "pod dimensions")
	speed := fs.Float64("speed", 60, "simulated seconds per wall-clock second")
	demo := fs.Bool("demo", true, "keep the pod busy with a synthetic workload")
	seed := fs.Uint64("seed", 1, "seed of the demo workload")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pod, err := topo.ParseDims(*podFlag)
	if err != nil {
		return err
	}
	if *speed <= 0 {
		return errors.New("-speed must be positive")
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	srv := server.New(server.Options{
		Pod: pod, Tenants: sim.Tenants(pod), Policy: sched.Default, Clock: server.NewScaledClock(*speed),
		Speed: *speed, Static: webui.FS(), Logger: log, SimPod: topo.Dims{X: 8, Y: 8, Z: 8},
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go srv.Run(ctx, 200*time.Millisecond)
	if *demo {
		go srv.RunDemo(ctx, *seed, 200*time.Millisecond)
	}

	hs := &http.Server{
		Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: time.Minute, IdleTimeout: 2 * time.Minute,
	}
	hs.RegisterOnShutdown(srv.Shutdown)
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	log.Info("serving", "url", "http://"+ln.Addr().String(), "pod", pod.String(), "speed", *speed, "demo", *demo, "console", webui.FS() != nil)
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return hs.Shutdown(shutdown)
}

func simulate(args []string) error {
	fs := flag.NewFlagSet("simulate", flag.ContinueOnError)
	podFlag := fs.String("pod", "8x8x8", "pod dimensions")
	jobs := fs.Int("jobs", 2000, "jobs per workload")
	load := fs.Float64("load", 0.9, "offered load (requested chip-time / pod chip-time)")
	seeds := fs.Int("seeds", 5, "number of workloads (seeds 1..n); results are averaged")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pod, err := topo.ParseDims(*podFlag)
	if err != nil {
		return err
	}
	if *seeds < 1 {
		return errors.New("-seeds must be at least 1")
	}
	started := time.Now()
	var runs [][]sim.Result
	for seed := 1; seed <= *seeds; seed++ {
		res, err := sim.Compare(sim.Config{Seed: uint64(seed), Jobs: *jobs, Load: *load, Pod: pod})
		if err != nil {
			return err
		}
		runs = append(runs, res)
	}
	fmt.Printf("Pod %s (%d chips), %d workloads of %d jobs at offered load %.2f; means over the workloads.\n\n",
		pod, pod.Volume(), *seeds, *jobs, *load)
	fmt.Print(sim.Markdown(sim.Average(runs)))
	fmt.Fprintf(os.Stderr, "simulated %d jobs under %d policies in %s\n", *seeds**jobs, len(sim.Policies), time.Since(started).Round(time.Millisecond))
	return nil
}
