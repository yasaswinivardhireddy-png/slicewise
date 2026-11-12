// Package server exposes the scheduler over HTTP: a JSON API, a Server-Sent Events stream of job
// changes, Prometheus metrics, and the web console.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yasaswinivardhireddy-png/slicewise/internal/sched"
	"github.com/yasaswinivardhireddy-png/slicewise/internal/sim"
	"github.com/yasaswinivardhireddy-png/slicewise/internal/topo"
)

// Clock reports simulated time since the server started.
type Clock interface{ Now() time.Duration }

// ScaledClock runs simulated time Speed times faster than wall time.
type ScaledClock struct {
	start time.Time
	speed float64
}

// NewScaledClock starts a clock running speed simulated seconds per wall second.
func NewScaledClock(speed float64) *ScaledClock { return &ScaledClock{start: time.Now(), speed: speed} }

// Now returns the simulated time, truncated to whole seconds.
func (c *ScaledClock) Now() time.Duration {
	return time.Duration(float64(time.Since(c.start)) * c.speed).Truncate(time.Second)
}

// Options configure a Server.
type Options struct {
	Pod     topo.Dims
	Tenants []sched.Tenant
	Policy  sched.Policy
	Clock   Clock
	Speed   float64 // shown in the console; simulated seconds per wall second
	Static  fs.FS   // the built web console; nil serves a placeholder page
	Logger  *slog.Logger
	SimPod  topo.Dims // pod used by simulation requests
}

// Server is the HTTP front end of one scheduler.
type Server struct {
	opts    Options
	log     *slog.Logger
	mu      sync.Mutex
	sched   *sched.Scheduler
	hub     *hub
	simBusy atomic.Bool
	handler http.Handler
}

// New returns a server for an idle pod.
func New(o Options) *Server {
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	s := &Server{opts: o, log: o.Logger, sched: sched.New(o.Pod, o.Tenants, o.Policy), hub: newHub(512)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/pod", s.getPod)
	mux.HandleFunc("GET /api/v1/jobs", s.listJobs)
	mux.HandleFunc("POST /api/v1/jobs", s.submitJob)
	mux.HandleFunc("GET /api/v1/jobs/{id}", s.getJob)
	mux.HandleFunc("POST /api/v1/jobs/{id}/cancel", s.cancelJob)
	mux.HandleFunc("GET /api/v1/tenants", s.listTenants)
	mux.HandleFunc("GET /api/v1/events", s.events)
	mux.HandleFunc("POST /api/v1/simulations", s.simulate)
	mux.HandleFunc("GET /metrics", s.metrics)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok\n") })
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		problem(w, http.StatusNotFound, "Not found", "No API endpoint matches this path.", nil)
	})
	mux.Handle("/", console(o.Static))
	s.handler = secure(recoverer(s.log, mux))
	return s
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler { return s.handler }

// Shutdown ends open event streams so that http.Server.Shutdown can finish.
func (s *Server) Shutdown() { s.hub.closeAll() }

// Submit queues a job at the current simulated time and schedules it.
func (s *Server) Submit(spec sched.Spec) (*sched.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Clock.Now()
	s.sched.Advance(now)
	j, err := s.sched.Submit(spec, now)
	if err == nil {
		s.sched.Schedule(now)
	}
	s.publish()
	return j, err
}

// Tick advances the scheduler to the current simulated time and publishes what changed.
func (s *Server) Tick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sched.Advance(s.opts.Clock.Now())
	s.publish()
}

// Run ticks every interval until ctx is done.
func (s *Server) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Tick()
		}
	}
}

// publish sends the scheduler's new events to stream subscribers. The caller holds s.mu.
func (s *Server) publish() {
	for _, e := range s.sched.Drain() {
		s.hub.publish(eventDTO{At: secs(e.At), Type: string(e.Type), JobID: e.JobID, JobName: e.Name, Detail: e.Detail})
	}
}

// ---- API types ----

type placementDTO = topo.Box

type historyDTO struct {
	At     int64  `json:"at"`
	State  string `json:"state"`
	Reason string `json:"reason"`
}

type reservationDTO struct {
	At        int64        `json:"at"`
	Placement placementDTO `json:"placement"`
}

type jobDTO struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	Tenant          string          `json:"tenant"`
	Priority        string          `json:"priority"`
	Shape           string          `json:"shape"`
	Chips           int             `json:"chips"`
	DurationMinutes int             `json:"durationMinutes"`
	State           string          `json:"state"`
	SubmittedAt     int64           `json:"submittedAt"`
	StartedAt       *int64          `json:"startedAt"`
	EndsAt          *int64          `json:"endsAt"`
	EndedAt         *int64          `json:"endedAt"`
	WaitSeconds     int64           `json:"waitSeconds"`
	Placement       *placementDTO   `json:"placement"`
	Preemptions     int             `json:"preemptions"`
	QueuePosition   int             `json:"queuePosition,omitempty"`
	Reservation     *reservationDTO `json:"reservation,omitempty"`
	History         []historyDTO    `json:"history,omitempty"`
}

type eventDTO struct {
	Seq     int64  `json:"seq"`
	At      int64  `json:"at"`
	Type    string `json:"type"`
	JobID   string `json:"jobId"`
	JobName string `json:"jobName"`
	Detail  string `json:"detail"`
}

func secs(d time.Duration) int64 { return int64(d / time.Second) }

func ptr(v int64) *int64 { return &v }

// job converts a job for the API. The caller holds s.mu.
func (s *Server) job(j *sched.Job, now time.Duration, detail bool) jobDTO {
	d := jobDTO{
		ID: j.ID, Name: j.Name, Tenant: j.Tenant, Priority: j.Priority.String(), Shape: j.Shape.String(),
		Chips: j.Chips(), DurationMinutes: int(j.Duration / time.Minute), State: j.State.String(),
		SubmittedAt: secs(j.Submitted), WaitSeconds: secs(j.Wait(now)), Preemptions: j.Preemptions,
	}
	if j.HasStarted {
		d.StartedAt = ptr(secs(j.Started))
	}
	switch j.State {
	case sched.Running:
		d.EndsAt = ptr(secs(j.EndsAt()))
		box := j.Box
		d.Placement = &box
	case sched.Queued:
		if detail {
			d.QueuePosition = s.sched.QueuePosition(j.ID)
		}
		if r, ok := s.sched.Reservation(); ok && r.JobID == j.ID {
			d.Reservation = &reservationDTO{At: secs(r.At), Placement: r.Box}
		}
	default:
		d.EndedAt = ptr(secs(j.Ended))
	}
	if detail {
		for _, h := range j.History {
			d.History = append(d.History, historyDTO{At: secs(h.At), State: h.State.String(), Reason: h.Reason})
		}
	}
	return d
}

// ---- handlers ----

func (s *Server) getPod(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Clock.Now()
	g := s.sched.Grid()
	cells := make([]string, g.Chips())
	for i := range cells {
		if j := s.sched.JobAt(i); j != nil {
			cells[i] = j.ID
		}
	}
	running := s.sched.Running()
	slices.SortFunc(running, func(a, b *sched.Job) int { return a.Seq - b.Seq })
	jobs := make([]jobDTO, len(running))
	for i, j := range running {
		jobs[i] = s.job(j, now, false)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dims": g.Pod(), "chips": g.Chips(), "busyChips": g.Chips() - g.FreeChips(), "now": secs(now),
		"speed": s.opts.Speed, "policy": s.sched.Policy().Name, "queued": s.sched.QueueLen(),
		"cells": cells, "running": jobs,
	})
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	state, tenant := r.URL.Query().Get("state"), r.URL.Query().Get("tenant")
	if !slices.Contains([]string{"", "all", "queued", "running", "finished"}, state) {
		problem(w, http.StatusBadRequest, "Invalid filter", "state must be queued, running, finished or all.", nil)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Clock.Now()
	all := s.sched.Jobs()
	out := []jobDTO{}
	for i := len(all) - 1; i >= 0 && len(out) < 500; i-- {
		j := all[i]
		match := state == "" || state == "all" || state == j.State.String() || (state == "finished" && j.State.Done())
		if match && (tenant == "" || tenant == j.Tenant) {
			out = append(out, s.job(j, now, false))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"now": secs(now), "jobs": out})
}

type submitRequest struct {
	Name            string `json:"name"`
	Tenant          string `json:"tenant"`
	Priority        string `json:"priority"`
	Shape           string `json:"shape"`
	DurationMinutes int    `json:"durationMinutes"`
}

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,39}$`)

func (s *Server) submitJob(w http.ResponseWriter, r *http.Request) {
	var req submitRequest
	if !decode(w, r, &req) {
		return
	}
	spec, problems := s.parseSpec(req)
	if len(problems) > 0 {
		problem(w, http.StatusBadRequest, "The job has problems", "Fix the fields listed in errors.", problems)
		return
	}
	j, err := s.Submit(spec)
	var ve *sched.ValidationError
	if errors.As(err, &ve) {
		problem(w, http.StatusBadRequest, "The job has problems", "Fix the fields listed in errors.", ve.Fields)
		return
	}
	if err != nil {
		problem(w, http.StatusInternalServerError, "Could not submit the job", err.Error(), nil)
		return
	}
	s.mu.Lock()
	dto := s.job(j, s.opts.Clock.Now(), true)
	s.mu.Unlock()
	w.Header().Set("Location", "/api/v1/jobs/"+j.ID)
	writeJSON(w, http.StatusCreated, dto)
}

// parseSpec validates a request field by field, so the console can show every problem at once.
func (s *Server) parseSpec(req submitRequest) (sched.Spec, map[string]string) {
	problems := map[string]string{}
	spec := sched.Spec{Name: req.Name, Tenant: req.Tenant, Duration: time.Duration(req.DurationMinutes) * time.Minute}
	if !namePattern.MatchString(req.Name) {
		problems["name"] = "Enter 3 to 40 lowercase letters, numbers or hyphens, starting with a letter or number."
	}
	if !slices.ContainsFunc(s.opts.Tenants, func(t sched.Tenant) bool { return t.Name == req.Tenant }) {
		problems["tenant"] = "Choose one of the teams."
	}
	if p, err := sched.ParsePriority(req.Priority); err != nil {
		problems["priority"] = "Choose a priority."
	} else {
		spec.Priority = p
	}
	if d, err := topo.ParseDims(req.Shape); err != nil || !d.Fits(s.opts.Pod) {
		problems["shape"] = fmt.Sprintf("Choose a slice shape that fits the %s pod.", s.opts.Pod)
	} else {
		spec.Shape = d
	}
	if req.DurationMinutes < 1 || time.Duration(req.DurationMinutes)*time.Minute > sched.MaxDuration {
		problems["durationMinutes"] = "Enter a run time from 1 to 10,080 minutes (7 days)."
	}
	return spec, problems
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.sched.Job(r.PathValue("id"))
	if !ok {
		problem(w, http.StatusNotFound, "Job not found", "There is no job with this ID.", nil)
		return
	}
	writeJSON(w, http.StatusOK, s.job(j, s.opts.Clock.Now(), true))
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Clock.Now()
	s.sched.Advance(now)
	j, err := s.sched.Cancel(r.PathValue("id"), now)
	switch {
	case errors.Is(err, sched.ErrNotFound):
		problem(w, http.StatusNotFound, "Job not found", "There is no job with this ID.", nil)
		return
	case errors.Is(err, sched.ErrFinished):
		problem(w, http.StatusConflict, "Job already finished", fmt.Sprintf("%s is %s and cannot be cancelled.", j.Name, j.State), nil)
		return
	}
	s.sched.Schedule(now)
	s.publish()
	writeJSON(w, http.StatusOK, s.job(j, now, true))
}

func (s *Server) listTenants(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"tenants": s.sched.Tenants()})
}

type simulateRequest struct {
	Seed int64   `json:"seed"`
	Jobs int     `json:"jobs"`
	Load float64 `json:"load"`
}

type simulationResultDTO struct {
	Policy                 string  `json:"policy"`
	Utilization            float64 `json:"utilization"`
	MeanWaitHours          float64 `json:"meanWaitHours"`
	P95WaitHours           float64 `json:"p95WaitHours"`
	ProductionP95WaitHours float64 `json:"productionP95WaitHours"`
	Backfilled             int     `json:"backfilled"`
	Preemptions            int     `json:"preemptions"`
	LostChipHours          float64 `json:"lostChipHours"`
	FragmentationDelayed   int     `json:"fragmentationDelayed"`
}

func (s *Server) simulate(w http.ResponseWriter, r *http.Request) {
	var req simulateRequest
	if !decode(w, r, &req) {
		return
	}
	problems := map[string]string{}
	if req.Seed < 0 {
		problems["seed"] = "Enter a whole number of 0 or more."
	}
	if req.Jobs < 50 || req.Jobs > 3000 {
		problems["jobs"] = "Enter from 50 to 3,000 jobs."
	}
	if req.Load < 0.3 || req.Load > 1.5 {
		problems["load"] = "Enter an offered load from 0.3 to 1.5."
	}
	if len(problems) > 0 {
		problem(w, http.StatusBadRequest, "The simulation has problems", "Fix the fields listed in errors.", problems)
		return
	}
	if !s.simBusy.CompareAndSwap(false, true) {
		w.Header().Set("Retry-After", "5")
		problem(w, http.StatusTooManyRequests, "A simulation is already running", "Try again in a few seconds.", nil)
		return
	}
	defer s.simBusy.Store(false)
	started := time.Now()
	results, err := sim.Compare(sim.Config{Seed: uint64(req.Seed), Jobs: req.Jobs, Load: req.Load, Pod: s.opts.SimPod})
	if err != nil {
		problem(w, http.StatusInternalServerError, "The simulation failed", err.Error(), nil)
		return
	}
	out := make([]simulationResultDTO, len(results))
	for i, res := range results {
		out[i] = simulationResultDTO{
			Policy: res.Policy, Utilization: res.Utilization, MeanWaitHours: res.MeanWait.Hours(),
			P95WaitHours: res.P95Wait.Hours(), ProductionP95WaitHours: res.ProductionP95Wait.Hours(),
			Backfilled: res.Backfilled, Preemptions: res.Preemptions, LostChipHours: res.LostChipHours,
			FragmentationDelayed: res.FragmentationDelayed,
		}
	}
	s.log.Info("simulation", "jobs", req.Jobs, "load", req.Load, "seed", req.Seed, "took", time.Since(started))
	writeJSON(w, http.StatusOK, map[string]any{
		"pod": s.opts.SimPod.String(), "seed": req.Seed, "jobs": req.Jobs, "load": req.Load,
		"elapsedMs": time.Since(started).Milliseconds(), "results": out,
	})
}

func (s *Server) metrics(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	g, st := s.sched.Grid(), s.sched.Stats()
	states := map[sched.State]int{}
	for _, j := range s.sched.Jobs() {
		states[j.State]++
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	var b strings.Builder
	metric := func(name, kind, help string, samples ...string) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
		for _, s := range samples {
			fmt.Fprintf(&b, "%s%s\n", name, s)
		}
	}
	metric("slicewise_chips", "gauge", "Chips in the pod by state.",
		fmt.Sprintf(`{state="busy"} %d`, g.Chips()-g.FreeChips()), fmt.Sprintf(`{state="idle"} %d`, g.FreeChips()))
	var jobs []string
	for _, st := range []sched.State{sched.Queued, sched.Running, sched.Succeeded, sched.Cancelled} {
		jobs = append(jobs, fmt.Sprintf(`{state=%q} %d`, st.String(), states[st]))
	}
	metric("slicewise_jobs", "gauge", "Jobs by state.", jobs...)
	metric("slicewise_scheduler_passes_total", "counter", "Scheduling passes.", " "+strconv.Itoa(st.Passes))
	metric("slicewise_jobs_backfilled_total", "counter", "Jobs started by backfilling.", " "+strconv.Itoa(st.Backfilled))
	metric("slicewise_preemptions_total", "counter", "Jobs preempted.", " "+strconv.Itoa(st.Preemptions))
	metric("slicewise_event_stream_clients", "gauge", "Open event streams.", " "+strconv.Itoa(s.hub.clients()))
	_, _ = io.WriteString(w, b.String())
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// problem writes an RFC 9457 problem document. fields maps request fields to what is wrong.
func problem(w http.ResponseWriter, status int, title, detail string, fields map[string]string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	body := map[string]any{"type": "about:blank", "title": title, "status": status, "detail": detail}
	if len(fields) > 0 {
		body["errors"] = fields
	}
	_ = json.NewEncoder(w).Encode(body)
}

// decode reads one JSON object of at most 16 KB, rejecting unknown fields.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil && dec.More() {
		err = errors.New("the body must hold a single JSON object")
	}
	if err != nil {
		problem(w, http.StatusBadRequest, "Invalid request body", err.Error(), nil)
		return false
	}
	return true
}

func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func recoverer(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				log.Error("panic", "path", r.URL.Path, "error", v)
				problem(w, http.StatusInternalServerError, "Internal error", "The server hit an unexpected error.", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

const placeholder = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Slicewise</title></head>
<body><main><h1>Slicewise</h1><p>The web console is not built. Run <code>npm run build</code> in <code>web/</code>, then rebuild the server.</p></main></body></html>`

// console serves the single-page web console: real files when they exist, index.html for every
// other path so client-side routes work on reload.
func console(static fs.FS) http.Handler {
	if static == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, placeholder)
		})
	}
	files := http.FileServerFS(static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			problem(w, http.StatusMethodNotAllowed, "Method not allowed", "The console only serves GET requests.", nil)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if info, err := fs.Stat(static, name); err == nil && !info.IsDir() && name != "index.html" {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		page, err := fs.ReadFile(static, "index.html")
		if err != nil {
			http.Error(w, "index.html is missing", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	})
}
