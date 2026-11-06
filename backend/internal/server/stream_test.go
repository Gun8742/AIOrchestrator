package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"pixel-agent-office/backend/internal/store"
)

type streamProjects struct {
	fakeProjects
	mu     sync.Mutex
	events []store.Event
}

func (s *streamProjects) CurrentSequence(context.Context) (int64, error) { return 0, nil }
func (s *streamProjects) EventsAfter(_ context.Context, after int64) ([]store.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []store.Event
	for _, event := range s.events {
		if event.Sequence > after {
			result = append(result, event)
		}
	}
	return result, nil
}

type streamWriter struct {
	header http.Header
	writes chan string
}

func (w *streamWriter) Header() http.Header         { return w.header }
func (w *streamWriter) WriteHeader(int)             {}
func (w *streamWriter) Write(p []byte) (int, error) { w.writes <- string(p); return len(p), nil }
func (w *streamWriter) Flush()                      {}

func TestEventStreamReadyBeforeCommittedEvents(t *testing.T) {
	db := &streamProjects{events: []store.Event{{Sequence: 1, TaskID: 7, Type: "status", Payload: json.RawMessage(`{"status":"queued"}`)}}}
	s := New(db, "127.0.0.1:8080", "secret", nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/events", nil).WithContext(ctx)
	w := &streamWriter{header: make(http.Header), writes: make(chan string, 4)}
	done := make(chan struct{})
	go func() { s.streamEvents(w, req); close(done) }()
	select {
	case got := <-w.writes:
		if !strings.Contains(got, "event: ready") {
			t.Fatalf("first message %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no ready event")
	}
	select {
	case got := <-w.writes:
		if !strings.Contains(got, "id: 1") || !strings.Contains(got, `"taskId":7`) {
			t.Fatalf("event %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no task event")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream did not close")
	}
}
