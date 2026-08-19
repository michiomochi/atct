package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWatchEmitsHumanDecisionEventsOnly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var output cancelOnOutput
	output.cancel = cancel
	output.needles = []string{"decision_id: approved"}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/inbox":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"unapplied_decisions":[]}`)
		case "/api/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "event: decision.created\ndata: {\"id\":\"created\"}\n\n")
			_, _ = io.WriteString(w, "event: decision.applied\ndata: {\"id\":\"applied\"}\n\n")
			_, _ = io.WriteString(w, "event: decision.withdrawn\ndata: {\"id\":\"withdrawn\"}\n\n")
			_, _ = io.WriteString(w, "event: decision.answered\ndata: {\"id\":\"human\",\"default_applied_at\":null}\n\n")
			_, _ = io.WriteString(w, "event: decision.answered\ndata: {\"id\":\"default\",\"default_applied_at\":\"2026-08-19T00:00:00Z\"}\n\n")
			_, _ = io.WriteString(w, "event: decision.approved\ndata: {\"id\":\"approved\"}\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	err := watchWithURLs(ctx, []string{server.URL}, &output, server.Client(), time.Millisecond)
	if err != nil {
		t.Fatalf("watchWithURLs() error = %v", err)
	}

	got := output.String()
	want := "atct decision answered (decision_id: human)\n" +
		"atct decision default applied (decision_id: default)\n" +
		"atct decision approved (decision_id: approved)\n"
	if got != want {
		t.Fatalf("watch output = %q, want %q", got, want)
	}
	for _, id := range []string{"created", "applied", "withdrawn"} {
		if strings.Contains(got, "decision_id: "+id) {
			t.Errorf("watch output contains suppressed decision %q: %q", id, got)
		}
	}
}

func TestWatchReadsSnapshotAfterDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var output cancelOnOutput
	output.cancel = cancel

	var mu sync.Mutex
	inboxCalls := 0
	eventCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/inbox":
			mu.Lock()
			inboxCalls++
			call := inboxCalls
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if call == 1 {
				_, _ = io.WriteString(w, `{"unapplied_decisions":[]}`)
				return
			}
			_, _ = io.WriteString(w, `{"unapplied_decisions":[{"id":"stranded","answer_label":"yes","answer_text":"","default_applied_at":null}]}`)
		case "/api/events":
			mu.Lock()
			eventCalls++
			call := eventCalls
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if call == 1 {
				return
			}
			cancel()
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	err := watchWithURLs(ctx, []string{server.URL}, &output, server.Client(), time.Millisecond)
	if err != nil {
		t.Fatalf("watchWithURLs() error = %v", err)
	}

	got := output.String()
	if !strings.Contains(got, "atct decision answered (decision_id: stranded)\n") {
		t.Fatalf("watch output = %q, want stranded snapshot notification", got)
	}
	mu.Lock()
	gotInboxCalls, gotEventCalls := inboxCalls, eventCalls
	mu.Unlock()
	if gotInboxCalls < 2 || gotEventCalls < 2 {
		t.Fatalf("requests after disconnect = inbox %d, events %d, want both at least 2", gotInboxCalls, gotEventCalls)
	}
}

func TestWatchEmitsApprovalAfterSnapshotAnswer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var output cancelOnOutput
	output.cancel = cancel
	output.needles = []string{"atct decision approved (decision_id: same)"}

	eventCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/inbox":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"unapplied_decisions":[{"id":"same","default_applied_at":null}]}`)
		case "/api/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "event: decision.approved\ndata: {\"id\":\"same\"}\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			eventCalls++
			if eventCalls >= 2 {
				cancel()
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	err := watchWithURLs(ctx, []string{server.URL}, &output, server.Client(), time.Millisecond)
	if err != nil {
		t.Fatalf("watchWithURLs() error = %v", err)
	}

	want := "atct decision answered (decision_id: same)\n" +
		"atct decision approved (decision_id: same)\n"
	if got := output.String(); got != want {
		t.Fatalf("watch output = %q, want %q", got, want)
	}
}

func TestWatchReportsReconnectWhileUnavailable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var output cancelOnOutput
	output.cancel = cancel
	output.needles = []string{"reconnecting"}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/inbox" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"unapplied_decisions":[]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	err := watchWithURLs(ctx, []string{server.URL}, &output, server.Client(), 10*time.Millisecond)
	if err != nil {
		t.Fatalf("watchWithURLs() error = %v", err)
	}

	if got := output.String(); !strings.Contains(got, "atct watch: connection unavailable; reconnecting in 10ms\n") {
		t.Fatalf("watch output = %q, want reconnect notification", got)
	}
}

type cancelOnOutput struct {
	mu      sync.Mutex
	buf     strings.Builder
	cancel  context.CancelFunc
	needles []string
}

func (w *cancelOnOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	_, _ = w.buf.Write(p)
	got := w.buf.String()
	shouldCancel := false
	for _, needle := range w.needles {
		if strings.Contains(got, needle) {
			shouldCancel = true
			break
		}
	}
	cancel := w.cancel
	w.mu.Unlock()
	if shouldCancel {
		cancel()
	}
	return len(p), nil
}

func (w *cancelOnOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return fmt.Sprint(w.buf.String())
}
