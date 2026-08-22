package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWatchEnsuresDaemonAfterConnectionFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var output cancelOnOutput
	output.cancel = cancel
	output.needles = []string{"atct decision answered (decision_id: revived)"}

	ensured := false
	ensureCalls := 0
	snapshotCalls := 0
	snapshot := func(context.Context) (string, []watchDecision, error) {
		snapshotCalls++
		if !ensured {
			return "", nil, errors.New("daemon unavailable")
		}
		return "http://unused", []watchDecision{{ID: "revived"}}, nil
	}
	ensure := func() error {
		ensureCalls++
		ensured = true
		return nil
	}

	err := watchLoopWithEnsure(ctx, &output, &http.Client{}, time.Millisecond, snapshot, ensure)
	if err != nil {
		t.Fatalf("watchLoopWithEnsure() error = %v", err)
	}
	if ensureCalls != 1 {
		t.Fatalf("Ensure calls = %d, want 1", ensureCalls)
	}
	if snapshotCalls < 2 {
		t.Fatalf("snapshot calls = %d, want a retry after Ensure", snapshotCalls)
	}
	if got := output.String(); !strings.Contains(got, "atct decision answered (decision_id: revived)\n") {
		t.Fatalf("watch output = %q, want revived notification", got)
	}
}

func TestWatchStopsEnsuringAfterFiveFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var output cancelOnOutput
	output.cancel = cancel
	output.needles = []string{watchEnsureLimitMessage}

	ensureCalls := 0
	snapshot := func(context.Context) (string, []watchDecision, error) {
		return "", nil, errors.New("daemon unavailable")
	}
	ensure := func() error {
		ensureCalls++
		return errors.New("database unavailable")
	}

	err := watchLoopWithEnsure(ctx, &output, &http.Client{}, time.Millisecond, snapshot, ensure)
	if err != nil {
		t.Fatalf("watchLoopWithEnsure() error = %v", err)
	}
	if ensureCalls != watchEnsureMaxFailures {
		t.Fatalf("Ensure calls = %d, want %d", ensureCalls, watchEnsureMaxFailures)
	}
	got := output.String()
	if !strings.Contains(got, "database unavailable") {
		t.Fatalf("watch output = %q, want Ensure error", got)
	}
	if strings.Count(got, watchEnsureLimitMessage) != 1 {
		t.Fatalf("watch output = %q, want one Ensure limit message", got)
	}
}

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
			_, _ = io.WriteString(w, "event: decision.rejected\ndata: {\"id\":\"rejected\"}\n\n")
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
		"atct decision rejected (decision_id: rejected)\n" +
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

func TestWatchEmitsWakeupEvents(t *testing.T) {
	var output strings.Builder
	delivered := make(map[watchDeliveryKey]struct{})
	wakeupDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	for _, id := range []string{"wakeup-1", "wakeup-2"} {
		if err := emitWatchDecision(&output, "wakeup", watchDecision{WakeupID: id}, delivered, wakeupDelivered, detectionDelivered); err != nil {
			t.Fatalf("emitWatchDecision(%s): %v", id, err)
		}
	}

	want := "atct wakeup: active_goals=0 unstarted_tasks=0 waiting_answer_tasks=0 working_tasks=0 untouched_tasks=0 waiting_answers=0\n" +
		"atct wakeup: active_goals=0 unstarted_tasks=0 waiting_answer_tasks=0 working_tasks=0 untouched_tasks=0 waiting_answers=0\n"
	if got := output.String(); got != want {
		t.Fatalf("wakeup output = %q, want %q", got, want)
	}
}

func TestWatchEmitsWakeupTaskBreakdownSeparatelyFromDecisionCount(t *testing.T) {
	var output strings.Builder
	delivered := make(map[watchDeliveryKey]struct{})
	wakeupDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	var decision watchDecision
	if err := json.Unmarshal([]byte(`{"wakeup_id":"wakeup-breakdown","active_goal_count":3,"unstarted_task_count":3,"waiting_answer_task_count":1,"working_task_count":1,"untouched_task_count":1,"waiting_answer_count":2}`), &decision); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if err := emitWatchDecision(&output, "wakeup", decision, delivered, wakeupDelivered, detectionDelivered); err != nil {
		t.Fatalf("emitWatchDecision: %v", err)
	}

	want := "atct wakeup: active_goals=3 unstarted_tasks=3 waiting_answer_tasks=1 working_tasks=1 untouched_tasks=1 waiting_answers=2\n"
	if got := output.String(); got != want {
		t.Fatalf("wakeup output = %q, want %q", got, want)
	}
}

func TestWatchEmitsWakeupAgainAfterStateReturns(t *testing.T) {
	var output strings.Builder
	delivered := make(map[watchDeliveryKey]struct{})
	wakeupDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	for _, wakeupID := range []string{"wakeup-before", "wakeup-after"} {
		if err := emitWatchDecision(&output, "wakeup", watchDecision{WakeupID: wakeupID}, delivered, wakeupDelivered, detectionDelivered); err != nil {
			t.Fatalf("emitWatchDecision(%s): %v", wakeupID, err)
		}
	}

	want := "atct wakeup: active_goals=0 unstarted_tasks=0 waiting_answer_tasks=0 working_tasks=0 untouched_tasks=0 waiting_answers=0\n" +
		"atct wakeup: active_goals=0 unstarted_tasks=0 waiting_answer_tasks=0 working_tasks=0 untouched_tasks=0 waiting_answers=0\n"
	if got := output.String(); got != want {
		t.Fatalf("wakeup output = %q, want %q", got, want)
	}
}

func TestWatchDoesNotFormatKeepaliveAsVisibleLine(t *testing.T) {
	line, ok := formatWatchDecision("keepalive", watchDecision{})
	if ok || line != "" {
		t.Fatalf("keepalive format = (%q, %v), want (empty, false)", line, ok)
	}
}

func TestWatchKeepsQuietWhileKeepalivesArriveAndReportsAfterTheyStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const timeout = 40 * time.Millisecond
	missing := formatWatchKeepaliveMissing(timeout)
	var output cancelOnOutput
	output.cancel = cancel
	output.needles = []string{missing}
	reader, writer := io.Pipe()
	defer writer.Close()
	client := &http.Client{Transport: watchRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       reader,
		}, nil
	})}
	errCh := make(chan error, 1)
	go func() {
		errCh <- consumeWatchEventsWithTimeout(ctx, client, "http://watch.test", "", &output, timeout, make(map[watchDeliveryKey]struct{}), make(map[watchWakeupDeliveryKey]struct{}), make(map[watchDetectionDeliveryKey]struct{}))
	}()

	for i := 0; i < 5; i++ {
		time.Sleep(10 * time.Millisecond)
		if _, err := io.WriteString(writer, "event: keepalive\ndata: {}\n\n"); err != nil {
			t.Fatalf("write keepalive %d: %v", i, err)
		}
		if got := output.String(); got != "" {
			t.Fatalf("watch output while keepalive %d was arriving = %q, want empty", i, got)
		}
	}

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("consumeWatchEventsWithTimeout() error after keepalives stopped = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for keepalive-missing notification")
	}
	if got := output.String(); got != missing+"\n" {
		t.Fatalf("watch output after keepalives stopped = %q, want one missing line %q", got, missing+"\n")
	}
}

func TestWatchReportsOneKeepaliveMissingLine(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const timeout = 10 * time.Millisecond
	missing := formatWatchKeepaliveMissing(timeout)
	var output cancelOnOutput
	output.cancel = cancel
	output.needles = []string{missing}
	reader, writer := io.Pipe()
	defer writer.Close()
	client := &http.Client{Transport: watchRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       reader,
		}, nil
	})}
	errCh := make(chan error, 1)
	go func() {
		errCh <- consumeWatchEventsWithTimeout(ctx, client, "http://watch.test", "", &output, timeout, make(map[watchDeliveryKey]struct{}), make(map[watchWakeupDeliveryKey]struct{}), make(map[watchDetectionDeliveryKey]struct{}))
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("consumeWatchEventsWithTimeout() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for keepalive-missing notification")
	}
	if got := output.String(); got != missing+"\n" {
		t.Fatalf("watch output = %q, want one missing line %q", got, missing+"\n")
	}
}

type watchRoundTripper func(*http.Request) (*http.Response, error)

func (f watchRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func runWatchWithProjects(t *testing.T, cwd string, projects []watchProject) (url.Values, int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var output cancelOnOutput
	output.cancel = cancel
	output.needles = []string{"atct decision answered (decision_id: project-query)"}
	queries := make(chan url.Values, 1)
	var mu sync.Mutex
	projectCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/projects":
			mu.Lock()
			projectCalls++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(projects); err != nil {
				t.Errorf("encode projects: %v", err)
			}
		case "/api/inbox":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"unapplied_decisions":[]}`)
		case "/api/events":
			queries <- r.URL.Query()
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "event: decision.answered\ndata: {\"id\":\"project-query\"}\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	if err := watchWithURLsAndProject(ctx, []string{server.URL}, &output, server.Client(), time.Millisecond, cwd); err != nil {
		t.Fatalf("watchWithURLsAndProject() error = %v", err)
	}
	query := <-queries
	mu.Lock()
	defer mu.Unlock()
	return query, projectCalls
}

func TestWatchPassesMatchingProjectIDToEvents(t *testing.T) {
	cwd := t.TempDir()
	query, projectCalls := runWatchWithProjects(t, cwd, []watchProject{{ID: "project-1", RootPath: cwd}})
	if got := query.Get("project_id"); got != "project-1" {
		t.Fatalf("events project_id = %q, want %q", got, "project-1")
	}
	if got := query.Get("cwd"); got != "" {
		t.Fatalf("events unexpectedly received cwd = %q", got)
	}
	if projectCalls != 1 {
		t.Fatalf("GET /api/projects calls = %d, want 1", projectCalls)
	}
}

func TestWatchMatchesProjectFromSubdirectory(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "nested", "work")
	query, _ := runWatchWithProjects(t, cwd, []watchProject{{ID: "project-1", RootPath: root}})
	if got := query.Get("project_id"); got != "project-1" {
		t.Fatalf("events project_id = %q, want %q", got, "project-1")
	}
}

func TestWatchOmitsProjectIDWhenCWDDoesNotMatch(t *testing.T) {
	cwd := t.TempDir()
	query, _ := runWatchWithProjects(t, cwd, []watchProject{{ID: "other", RootPath: t.TempDir()}})
	if _, ok := query["project_id"]; ok {
		t.Fatalf("events unexpectedly received project_id = %q", query.Get("project_id"))
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

func TestWatchFiltersOtherProjectFromSnapshot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var output cancelOnOutput
	output.cancel = cancel
	output.needles = []string{"atct decision answered (decision_id: unscoped)"}

	root := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/projects":
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode([]watchProject{{ID: "project-1", RootPath: root}}); err != nil {
				t.Errorf("encode projects: %v", err)
			}
		case "/api/inbox":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"unapplied_decisions":[{"id":"other","project_id":"other-project","default_applied_at":null},{"id":"assigned","project_id":"project-1","default_applied_at":null},{"id":"unscoped","project_id":"","default_applied_at":null}]}`)
		case "/api/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	if err := watchWithURLsAndProject(ctx, []string{server.URL}, &output, server.Client(), time.Millisecond, root); err != nil {
		t.Fatalf("watchWithURLsAndProject() error = %v", err)
	}

	want := "atct decision answered (decision_id: assigned)\n" +
		"atct decision answered (decision_id: unscoped)\n"
	if got := output.String(); got != want {
		t.Fatalf("watch output = %q, want %q", got, want)
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

func TestEmitWatchDetectionWritesOneLinePerCondition(t *testing.T) {
	cases := []struct {
		event  string
		record watchDecision
		want   string
	}{
		{"detection.completion_report_missing", watchDecision{GoalID: "goal-1"}, "atct detection: goal goal-1 has all tasks done but no completion report"},
		{"detection.commits_missing", watchDecision{GoalID: "goal-2"}, "atct detection: goal goal-2 has no linked commits"},
		{"detection.undeclared_goal", watchDecision{GoalID: "goal-3"}, "atct detection: goal goal-3 has no tasks declared"},
		{"detection.all_tasks_dropped", watchDecision{GoalID: "goal-4"}, "atct detection: goal goal-4 has all tasks dropped"},
		{"detection.unclaimed_doing", watchDecision{TaskID: "task-1"}, "atct detection: task task-1 is doing without a work lock"},
	}
	for _, tc := range cases {
		var output bytes.Buffer
		delivered := make(map[watchDeliveryKey]struct{})
		wakeupDelivered := make(map[watchWakeupDeliveryKey]struct{})
		detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})
		if err := emitWatchDecision(&output, tc.event, tc.record, delivered, wakeupDelivered, detectionDelivered); err != nil {
			t.Fatalf("emitWatchDecision(%s): %v", tc.event, err)
		}
		if got := strings.TrimSpace(output.String()); got != tc.want {
			t.Fatalf("%s wrote %q, want %q", tc.event, got, tc.want)
		}
	}
}

func TestEmitWatchDetectionDoesNotRepeatSameTarget(t *testing.T) {
	var output bytes.Buffer
	delivered := make(map[watchDeliveryKey]struct{})
	wakeupDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	// A fresh detection_id on the second delivery must not make it look new;
	// the key is the target, not the occurrence.
	first := watchDecision{GoalID: "goal-1", DetectionID: "detection-1"}
	second := watchDecision{GoalID: "goal-1", DetectionID: "detection-2"}
	for _, record := range []watchDecision{first, second} {
		if err := emitWatchDecision(&output, "detection.commits_missing", record, delivered, wakeupDelivered, detectionDelivered); err != nil {
			t.Fatalf("emitWatchDecision: %v", err)
		}
	}
	if lines := strings.Count(strings.TrimSpace(output.String()), "\n"); lines != 0 {
		t.Fatalf("output = %q, want a single line", output.String())
	}
}

func TestEmitWatchDetectionSeparatesTargets(t *testing.T) {
	var output bytes.Buffer
	delivered := make(map[watchDeliveryKey]struct{})
	wakeupDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	// Deduplication must not be so broad that a second goal with the same
	// condition goes unreported.
	for _, goalID := range []string{"goal-1", "goal-2"} {
		if err := emitWatchDecision(&output, "detection.commits_missing", watchDecision{GoalID: goalID}, delivered, wakeupDelivered, detectionDelivered); err != nil {
			t.Fatalf("emitWatchDecision(%s): %v", goalID, err)
		}
	}
	for _, goalID := range []string{"goal-1", "goal-2"} {
		if !strings.Contains(output.String(), goalID) {
			t.Fatalf("output = %q, want it to mention %s", output.String(), goalID)
		}
	}
}

func TestEmitWatchGoalCreatedWritesOneLine(t *testing.T) {
	var output bytes.Buffer
	delivered := make(map[watchDeliveryKey]struct{})
	wakeupDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	if err := emitWatchDecision(&output, "goal.created", watchDecision{GoalID: "goal-1"}, delivered, wakeupDelivered, detectionDelivered); err != nil {
		t.Fatalf("emitWatchDecision: %v", err)
	}

	want := "atct goal created (goal_id: goal-1)\n"
	if got := output.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestEmitWatchGoalCreatedDoesNotRepeatSameGoal(t *testing.T) {
	var output bytes.Buffer
	delivered := make(map[watchDeliveryKey]struct{})
	wakeupDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	for _, decision := range []watchDecision{
		{ID: "event-1", GoalID: "goal-1"},
		{ID: "event-2", GoalID: "goal-1"},
	} {
		if err := emitWatchDecision(&output, "goal.created", decision, delivered, wakeupDelivered, detectionDelivered); err != nil {
			t.Fatalf("emitWatchDecision: %v", err)
		}
	}

	want := "atct goal created (goal_id: goal-1)\n"
	if got := output.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestEmitWatchGoalCreatedSeparatesGoals(t *testing.T) {
	var output bytes.Buffer
	delivered := make(map[watchDeliveryKey]struct{})
	wakeupDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	for _, goalID := range []string{"goal-1", "goal-2"} {
		if err := emitWatchDecision(&output, "goal.created", watchDecision{GoalID: goalID}, delivered, wakeupDelivered, detectionDelivered); err != nil {
			t.Fatalf("emitWatchDecision(%s): %v", goalID, err)
		}
	}

	want := "atct goal created (goal_id: goal-1)\n" +
		"atct goal created (goal_id: goal-2)\n"
	if got := output.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestEmitWatchDetectionIgnoresUnknownEvent(t *testing.T) {
	var output bytes.Buffer
	delivered := make(map[watchDeliveryKey]struct{})
	wakeupDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	// A newer daemon may publish conditions this build has never heard of.
	// Staying quiet is correct; failing is not.
	if err := emitWatchDecision(&output, "detection.something_new", watchDecision{GoalID: "goal-1"}, delivered, wakeupDelivered, detectionDelivered); err != nil {
		t.Fatalf("emitWatchDecision(unknown): %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("output = %q, want nothing", output.String())
	}
}

func TestEmitWatchDetectionRejectsMissingTarget(t *testing.T) {
	var output bytes.Buffer
	delivered := make(map[watchDeliveryKey]struct{})
	wakeupDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	err := emitWatchDecision(&output, "detection.commits_missing", watchDecision{}, delivered, wakeupDelivered, detectionDelivered)
	if err == nil {
		t.Fatal("emitWatchDecision() with no target = nil, want an error")
	}
	if output.Len() != 0 {
		t.Fatalf("output = %q, want nothing", output.String())
	}
}
