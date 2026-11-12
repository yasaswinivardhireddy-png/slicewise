package server

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// hub fans job events out to Server-Sent Events streams. It keeps the most recent events so a
// client that reconnects with Last-Event-ID misses nothing.
type hub struct {
	mu     sync.Mutex
	seq    int64
	keep   int
	recent []eventDTO // oldest first
	subs   map[chan eventDTO]struct{}
	closed bool
}

func newHub(keep int) *hub { return &hub{keep: keep, subs: map[chan eventDTO]struct{}{}} }

func (h *hub) publish(e eventDTO) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	e.Seq = h.seq
	h.recent = append(h.recent, e)
	if len(h.recent) > h.keep {
		h.recent = h.recent[len(h.recent)-h.keep:]
	}
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
			// The client is too slow. Dropping it is safe: its browser reconnects with
			// Last-Event-ID and catches up from the recent events.
			delete(h.subs, ch)
			close(ch)
		}
	}
}

// subscribe returns a channel of future events and the kept events after lastID. complete is
// false when events after lastID were already discarded, so the client must reload its state.
func (h *hub) subscribe(lastID int64) (ch chan eventDTO, missed []eventDTO, complete bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch = make(chan eventDTO, 64)
	if h.closed {
		close(ch)
		return ch, nil, true
	}
	h.subs[ch] = struct{}{}
	if lastID <= 0 {
		return ch, nil, true
	}
	oldest := h.seq - int64(len(h.recent)) + 1
	complete = lastID <= h.seq && lastID+1 >= oldest
	for _, e := range h.recent {
		if e.Seq > lastID {
			missed = append(missed, e)
		}
	}
	return ch, missed, complete
}

func (h *hub) unsubscribe(ch chan eventDTO) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
}

func (h *hub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for ch := range h.subs {
		delete(h.subs, ch)
		close(ch)
	}
}

func (h *hub) clients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// events streams job changes as Server-Sent Events. A "reset" event tells the client that it
// missed events and must reload.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	last, _ := strconv.ParseInt(cmp.Or(r.Header.Get("Last-Event-ID"), r.URL.Query().Get("lastEventId")), 10, 64)
	ch, missed, complete := s.hub.subscribe(last)
	defer s.hub.unsubscribe(ch)

	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // streams outlive the server's write timeout
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	write := func(format string, args ...any) bool {
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	send := func(e eventDTO) bool {
		data, _ := json.Marshal(e)
		return write("id: %d\nevent: job\ndata: %s\n\n", e.Seq, data)
	}
	if !write("retry: 3000\n\n") || (!complete && !write("event: reset\ndata: {}\n\n")) {
		return
	}
	for _, e := range missed {
		if !send(e) {
			return
		}
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-ch:
			if !ok || !send(e) {
				return
			}
		case <-heartbeat.C:
			if !write(": keep-alive\n\n") {
				return
			}
		}
	}
}
