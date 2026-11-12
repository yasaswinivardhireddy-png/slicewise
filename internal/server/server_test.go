package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/yasaswinivardhireddy-png/slicewise/internal/sched"
	"github.com/yasaswinivardhireddy-png/slicewise/internal/topo"
)

type manualClock struct {
	mu sync.Mutex
	t  time.Duration
}

func (c *manualClock) Now() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *manualClock) Set(t time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}

func newTestServer(static fs.FS) (*Server, *manualClock) {
	clock := &manualClock{}
	s := New(Options{
		Pod:     topo.Dims{X: 4, Y: 4, Z: 2},
		Tenants: []sched.Tenant{{Name: "research", Quota: 16}, {Name: "serving", Quota: 16}},
		Policy:  sched.Default, Clock: clock, Speed: 60, Static: static,
		SimPod: topo.Dims{X: 4, Y: 4, Z: 4},
	})
	return s, clock
}

func do(h http.Handler, method, target string, body any) *httptest.ResponseRecorder {
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		data, _ := json.Marshal(b)
		rd = bytes.NewReader(data)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, rd))
	return rec
}

func decodeAs[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func job(name, tenant, priority, shape string, minutes int) map[string]any {
	return map[string]any{"name": name, "tenant": tenant, "priority": priority, "shape": shape, "durationMinutes": minutes}
}

type problemBody struct {
	Title  string            `json:"title"`
	Status int               `json:"status"`
	Errors map[string]string `json:"errors"`
}

func TestSubmitInspectAndCancel(t *testing.T) {
	s, _ := newTestServer(nil)
	h := s.Handler()

	rec := do(h, "POST", "/api/v1/jobs", job("train-llm", "research", "batch", "2x2x2", 60))
	if rec.Code != http.StatusCreated || rec.Header().Get("Location") != "/api/v1/jobs/j-1" {
		t.Fatalf("submit: %d %s %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	created := decodeAs[jobDTO](t, rec)
	if created.State != "running" || created.Chips != 8 || created.Placement == nil || created.EndsAt == nil || *created.EndsAt != 3600 {
		t.Fatalf("created job = %+v", created)
	}

	pod := decodeAs[struct {
		BusyChips int      `json:"busyChips"`
		Cells     []string `json:"cells"`
	}](t, do(h, "GET", "/api/v1/pod", nil))
	held := 0
	for _, c := range pod.Cells {
		if c == "j-1" {
			held++
		}
	}
	if pod.BusyChips != 8 || held != 8 || len(pod.Cells) != 32 {
		t.Fatalf("pod: busy %d, cells held %d of %d", pod.BusyChips, held, len(pod.Cells))
	}

	got := decodeAs[jobDTO](t, do(h, "GET", "/api/v1/jobs/j-1", nil))
	if len(got.History) != 2 || got.History[1].State != "running" {
		t.Fatalf("history = %+v", got.History)
	}

	if rec := do(h, "POST", "/api/v1/jobs/j-1/cancel", nil); rec.Code != http.StatusOK || decodeAs[jobDTO](t, rec).State != "cancelled" {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, "POST", "/api/v1/jobs/j-1/cancel", nil); rec.Code != http.StatusConflict {
		t.Errorf("second cancel: %d, want 409", rec.Code)
	}
	if rec := do(h, "GET", "/api/v1/jobs/j-404", nil); rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Errorf("unknown job: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestSubmitReportsEveryFieldProblem(t *testing.T) {
	s, _ := newTestServer(nil)
	rec := do(s.Handler(), "POST", "/api/v1/jobs", job("X", "nobody", "urgent", "9x9x9", 0))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	p := decodeAs[problemBody](t, rec)
	for _, field := range []string{"name", "tenant", "priority", "shape", "durationMinutes"} {
		if p.Errors[field] == "" {
			t.Errorf("no error for %s in %+v", field, p.Errors)
		}
	}
}

func TestSubmitRejectsMalformedBodies(t *testing.T) {
	s, _ := newTestServer(nil)
	extra := job("train-a", "research", "batch", "2x2x2", 60)
	extra["gpus"] = 8
	for name, body := range map[string]any{
		"unknown field": extra,
		"not json":      "not json",
		"two objects":   `{"name":"a"} {"name":"b"}`,
		"too large":     `{"name":"` + strings.Repeat("a", 20<<10) + `"}`,
	} {
		if rec := do(s.Handler(), "POST", "/api/v1/jobs", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, rec.Code)
		}
	}
}

func TestJobListFiltersAndReservation(t *testing.T) {
	s, _ := newTestServer(nil)
	h := s.Handler()
	do(h, "POST", "/api/v1/jobs", job("whole-pod", "research", "production", "4x4x2", 120))
	do(h, "POST", "/api/v1/jobs", job("waits", "serving", "batch", "2x2x2", 30))

	type list struct {
		Jobs []jobDTO `json:"jobs"`
	}
	queued := decodeAs[list](t, do(h, "GET", "/api/v1/jobs?state=queued", nil)).Jobs
	if len(queued) != 1 || queued[0].Name != "waits" || queued[0].Reservation == nil || queued[0].Reservation.At != 7200 {
		t.Fatalf("queued = %+v", queued)
	}
	running := decodeAs[list](t, do(h, "GET", "/api/v1/jobs?state=running&tenant=research", nil)).Jobs
	if len(running) != 1 || running[0].Name != "whole-pod" {
		t.Fatalf("running = %+v", running)
	}
	if rec := do(h, "GET", "/api/v1/jobs?state=bogus", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad filter: %d", rec.Code)
	}
	detail := decodeAs[jobDTO](t, do(h, "GET", "/api/v1/jobs/j-2", nil))
	if detail.QueuePosition != 1 {
		t.Errorf("queue position = %d, want 1", detail.QueuePosition)
	}
}

func TestJobsFinishAsTheClockAdvances(t *testing.T) {
	s, clock := newTestServer(nil)
	do(s.Handler(), "POST", "/api/v1/jobs", job("eval-1", "serving", "batch", "2x2x1", 60))
	clock.Set(61 * time.Minute)
	s.Tick()
	got := decodeAs[jobDTO](t, do(s.Handler(), "GET", "/api/v1/jobs/j-1", nil))
	if got.State != "succeeded" || got.EndedAt == nil || *got.EndedAt != 3600 {
		t.Fatalf("job = %+v", got)
	}
	tenants := decodeAs[struct {
		Tenants []sched.TenantUsage `json:"tenants"`
	}](t, do(s.Handler(), "GET", "/api/v1/tenants", nil)).Tenants
	if len(tenants) != 2 || tenants[1].Used != 0 {
		t.Errorf("tenants = %+v", tenants)
	}
}

// readEvents connects to the stream and returns the first n messages as "event:id:type".
func readEvents(t *testing.T, base string, lastID string, n int) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/api/v1/events", nil)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Error(err)
		return nil
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content type %q", ct)
		return nil
	}
	var out []string
	var id, kind string
	sc := bufio.NewScanner(resp.Body)
	for len(out) < n && sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			kind = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			var e eventDTO
			_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &e)
			out = append(out, kind+":"+id+":"+e.Type)
			id, kind = "", ""
		}
	}
	return out
}

func TestEventStreamDeliversAndResumes(t *testing.T) {
	s, _ := newTestServer(nil)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	done := make(chan []string)
	go func() { done <- readEvents(t, ts.URL, "", 2) }()
	for s.hub.clients() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := s.Submit(sched.Spec{Name: "train-1", Tenant: "research", Priority: sched.Batch, Shape: topo.Dims{X: 2, Y: 2, Z: 2}, Duration: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if got := <-done; strings.Join(got, ",") != "job:1:submitted,job:2:started" {
		t.Fatalf("live events = %v", got)
	}
	if got := readEvents(t, ts.URL, "1", 1); strings.Join(got, ",") != "job:2:started" {
		t.Errorf("resume after 1 = %v", got)
	}
	if got := readEvents(t, ts.URL, "99", 1); strings.Join(got, ",") != "reset::" {
		t.Errorf("resume from the future = %v, want a reset", got)
	}
}

func TestShutdownEndsStreams(t *testing.T) {
	s, _ := newTestServer(nil)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	done := make(chan struct{})
	go func() {
		readEvents(t, ts.URL, "", 1)
		close(done)
	}()
	for s.hub.clients() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	s.Shutdown()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end on shutdown")
	}
}

func TestSimulation(t *testing.T) {
	s, _ := newTestServer(nil)
	h := s.Handler()
	rec := do(h, "POST", "/api/v1/simulations", map[string]any{"seed": 3, "jobs": 60, "load": 0.8})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	body := decodeAs[struct {
		Pod     string                `json:"pod"`
		Results []simulationResultDTO `json:"results"`
	}](t, rec)
	if body.Pod != "4x4x4" || len(body.Results) != 5 || body.Results[0].Utilization <= 0 {
		t.Fatalf("body = %+v", body)
	}
	p := decodeAs[problemBody](t, do(h, "POST", "/api/v1/simulations", map[string]any{"seed": -1, "jobs": 10, "load": 3}))
	if len(p.Errors) != 3 {
		t.Errorf("errors = %v, want seed, jobs and load", p.Errors)
	}
	s.simBusy.Store(true)
	if rec := do(h, "POST", "/api/v1/simulations", map[string]any{"seed": 1, "jobs": 60, "load": 0.8}); rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("busy: %d", rec.Code)
	}
}

func TestMetrics(t *testing.T) {
	s, _ := newTestServer(nil)
	do(s.Handler(), "POST", "/api/v1/jobs", job("eval-1", "serving", "batch", "2x2x1", 60))
	body := do(s.Handler(), "GET", "/metrics", nil).Body.String()
	for _, want := range []string{`slicewise_chips{state="busy"} 4`, `slicewise_jobs{state="running"} 1`, "# TYPE slicewise_preemptions_total counter"} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q:\n%s", want, body)
		}
	}
}

func TestConsoleServesFilesAndFallsBackToIndex(t *testing.T) {
	s, _ := newTestServer(fstest.MapFS{
		"index.html":        {Data: []byte("<!doctype html><title>console</title>")},
		"assets/app-abc.js": {Data: []byte("console.log(1)")},
	})
	h := s.Handler()
	for _, p := range []string{"/", "/jobs/j-7", "/simulator"} {
		rec := do(h, "GET", p, nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<title>console</title>") {
			t.Errorf("GET %s: %d %q", p, rec.Code, rec.Body)
		}
		if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
			t.Errorf("GET %s: CSP %q", p, csp)
		}
	}
	if rec := do(h, "GET", "/assets/app-abc.js", nil); !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("asset cache control %q", rec.Header().Get("Cache-Control"))
	}
	if rec := do(h, "POST", "/", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /: %d", rec.Code)
	}
	if rec := do(h, "GET", "/api/v1/nope", nil); rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Errorf("unknown API path: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestPlaceholderWhenTheConsoleIsNotBuilt(t *testing.T) {
	s, _ := newTestServer(nil)
	if body := do(s.Handler(), "GET", "/", nil).Body.String(); !strings.Contains(body, "not built") {
		t.Errorf("body = %q", body)
	}
}

func TestDemoFillsThePod(t *testing.T) {
	s, _ := newTestServer(nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.RunDemo(ctx, 1, time.Millisecond)
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		busy := s.sched.Grid().Chips() - s.sched.Grid().FreeChips()
		s.mu.Unlock()
		if busy >= 16 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("demo filled only %d chips", busy)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}
