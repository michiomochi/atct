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
	lastWakeupContent := ""
	wakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	for _, decision := range []watchDecision{
		{WakeupID: "wakeup-1"},
		{WakeupID: "wakeup-2", UnstartedTaskCount: 1},
	} {
		if err := emitWatchDecisionWithState(&output, "wakeup", decision, delivered, &lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered); err != nil {
			t.Fatalf("emitWatchDecision(%s): %v", decision.WakeupID, err)
		}
	}

	want := "atct wakeup: actionable_goals=0 unassigned_goals=0 unstarted_tasks=0 waiting_answer_tasks=0 untouched_tasks=0 delegated_tasks=0 waiting_answers=0 unassigned=[]\n" +
		"atct wakeup: actionable_goals=0 unassigned_goals=0 unstarted_tasks=1 waiting_answer_tasks=0 untouched_tasks=0 delegated_tasks=0 waiting_answers=0 unassigned=[]\n"
	if strings.Contains(output.String(), "working_tasks=") {
		t.Fatalf("wakeup output contains removed working-task count: %q", output.String())
	}
	if got := output.String(); got != want {
		t.Fatalf("wakeup output = %q, want %q", got, want)
	}
}

func TestWatchFormatsActionableGoalCount(t *testing.T) {
	var decision watchDecision
	if err := json.Unmarshal([]byte(`{"wakeup_id":"wakeup-actionable","actionable_goal_count":3}`), &decision); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	line, ok := formatWatchDecision("wakeup", decision)
	if !ok {
		t.Fatal("formatWatchDecision returned false, want true")
	}
	if strings.Contains(line, "active_goals=") {
		t.Fatalf("wakeup output uses the old goal label: %q", line)
	}
	want := "atct wakeup: actionable_goals=3 unassigned_goals=0 unstarted_tasks=0 waiting_answer_tasks=0 untouched_tasks=0 delegated_tasks=0 waiting_answers=0 unassigned=[]"
	if line != want {
		t.Fatalf("wakeup output = %q, want %q", line, want)
	}
}

func TestWatchFormatsWakeupWithNoUnassignedGoals(t *testing.T) {
	line, ok := formatWatchDecision("wakeup", watchDecision{ActionableGoalCount: 6})
	if !ok {
		t.Fatal("formatWatchDecision returned false, want true")
	}
	const want = "atct wakeup: actionable_goals=6 unassigned_goals=0 unstarted_tasks=0 waiting_answer_tasks=0 untouched_tasks=0 delegated_tasks=0 waiting_answers=0 unassigned=[]"
	if line != want {
		t.Fatalf("wakeup output = %q, want %q", line, want)
	}
}

func TestWatchFormatsWakeupWithTwoUnassignedGoals(t *testing.T) {
	line, ok := formatWatchDecision("wakeup", watchDecision{
		ActionableGoalCount:    6,
		UnassignedGoalCount:    2,
		UnassignedGoalIDs:      []int64{136, 140},
		UnstartedTaskCount:     12,
		UntouchedTaskCount:     2,
		WaitingAnswerTaskCount: 0,
		DelegatedTaskCount:     0,
		WaitingAnswerCount:     0,
	})
	if !ok {
		t.Fatal("formatWatchDecision returned false, want true")
	}
	const want = "atct wakeup: actionable_goals=6 unassigned_goals=2 unstarted_tasks=12 waiting_answer_tasks=0 untouched_tasks=2 delegated_tasks=0 waiting_answers=0 unassigned=[136,140]"
	if line != want {
		t.Fatalf("wakeup output = %q, want %q", line, want)
	}
}

func TestWatchFormatsExactlyFiveUnassignedGoalsWithoutRemainder(t *testing.T) {
	line, ok := formatWatchDecision("wakeup", watchDecision{
		UnassignedGoalCount: 5,
		UnassignedGoalIDs:   []int64{136, 137, 138, 139, 140},
	})
	if !ok {
		t.Fatal("formatWatchDecision returned false, want true")
	}
	const want = "atct wakeup: actionable_goals=0 unassigned_goals=5 unstarted_tasks=0 waiting_answer_tasks=0 untouched_tasks=0 delegated_tasks=0 waiting_answers=0 unassigned=[136,137,138,139,140]"
	if line != want {
		t.Fatalf("wakeup output = %q, want %q", line, want)
	}
}

func TestWatchFormatsTwentyUnassignedGoalsWithRemainderOnOneLine(t *testing.T) {
	ids := make([]int64, 20)
	for i := range ids {
		ids[i] = 136 + int64(i)
	}

	line, ok := formatWatchDecision("wakeup", watchDecision{
		UnassignedGoalCount: 20,
		UnassignedGoalIDs:   ids,
	})
	if !ok {
		t.Fatal("formatWatchDecision returned false, want true")
	}
	if strings.Contains(line, "\n") {
		t.Fatalf("wakeup output contains a newline: %q", line)
	}
	const want = "atct wakeup: actionable_goals=0 unassigned_goals=20 unstarted_tasks=0 waiting_answer_tasks=0 untouched_tasks=0 delegated_tasks=0 waiting_answers=0 unassigned=[136,137,138,139,140,+15]"
	if line != want {
		t.Fatalf("wakeup output = %q, want %q", line, want)
	}
}

func TestWatchDecodesUnassignedGoalFields(t *testing.T) {
	var decision watchDecision
	if err := json.Unmarshal([]byte(`{"wakeup_id":"wakeup-unassigned","unassigned_goal_count":2,"unassigned_goal_ids":[136,140]}`), &decision); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if decision.UnassignedGoalCount != 2 {
		t.Fatalf("unassigned goal count = %d, want 2", decision.UnassignedGoalCount)
	}
	if len(decision.UnassignedGoalIDs) != 2 || decision.UnassignedGoalIDs[0] != 136 || decision.UnassignedGoalIDs[1] != 140 {
		t.Fatalf("unassigned goal IDs = %#v, want [136 140]", decision.UnassignedGoalIDs)
	}
}

func TestWatchDecodesNumericEntityIDs(t *testing.T) {
	var decision watchDecision
	if err := json.Unmarshal([]byte(`{"id":11,"decision_id":12,"project_id":13,"wakeup_id":14,"detection_id":15,"goal_id":16,"task_id":17,"handoff_id":18}`), &decision); err != nil {
		t.Fatalf("json.Unmarshal decision: %v", err)
	}

	for name, values := range map[string]struct {
		got  string
		want string
	}{
		"id":           {decision.ID, "11"},
		"decision_id":  {decision.DecisionID, "12"},
		"project_id":   {decision.ProjectID, "13"},
		"wakeup_id":    {decision.WakeupID, "14"},
		"detection_id": {decision.DetectionID, "15"},
		"goal_id":      {decision.GoalID, "16"},
		"task_id":      {decision.TaskID, "17"},
		"handoff_id":   {decision.HandoffID, "18"},
	} {
		got, want := values.got, values.want
		if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}

	var project watchProject
	if err := json.Unmarshal([]byte(`{"id":19,"root_path":"/repo"}`), &project); err != nil {
		t.Fatalf("json.Unmarshal project: %v", err)
	}
	if project.ID != "19" {
		t.Fatalf("project ID = %q, want %q", project.ID, "19")
	}
}

func TestWatchEmitsWakeupTaskBreakdownSeparatelyFromDecisionCount(t *testing.T) {
	var output strings.Builder
	delivered := make(map[watchDeliveryKey]struct{})
	lastWakeupContent := ""
	wakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	var decision watchDecision
	if err := json.Unmarshal([]byte(`{"wakeup_id":"wakeup-breakdown","actionable_goal_count":3,"unstarted_task_count":3,"waiting_answer_task_count":1,"untouched_task_count":2,"waiting_answer_count":2}`), &decision); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if err := emitWatchDecisionWithState(&output, "wakeup", decision, delivered, &lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered); err != nil {
		t.Fatalf("emitWatchDecision: %v", err)
	}

	want := "atct wakeup: actionable_goals=3 unassigned_goals=0 unstarted_tasks=3 waiting_answer_tasks=1 untouched_tasks=2 delegated_tasks=0 waiting_answers=2 unassigned=[]\n"
	if got := output.String(); got != want {
		t.Fatalf("wakeup output = %q, want %q", got, want)
	}
}

func TestWatchFormatsDelegatedTaskCountOutsideUnstartedBreakdown(t *testing.T) {
	line, ok := formatWatchDecision("wakeup", watchDecision{
		WakeupID:               "wakeup-delegated",
		UnstartedTaskCount:     3,
		WaitingAnswerTaskCount: 1,
		UntouchedTaskCount:     2,
		WaitingAnswerCount:     4,
		DelegatedTaskCount:     2,
	})
	if !ok {
		t.Fatal("formatWatchDecision returned false, want true")
	}
	const want = "atct wakeup: actionable_goals=0 unassigned_goals=0 unstarted_tasks=3 waiting_answer_tasks=1 untouched_tasks=2 delegated_tasks=2 waiting_answers=4 unassigned=[]"
	if line != want {
		t.Fatalf("wakeup output = %q, want %q", line, want)
	}
}

func TestWatchEmitsWakeupAgainAfterStateReturns(t *testing.T) {
	var output strings.Builder
	delivered := make(map[watchDeliveryKey]struct{})
	lastWakeupContent := ""
	wakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	for _, decision := range []watchDecision{
		{WakeupID: "wakeup-before"},
		{WakeupID: "wakeup-during", UnstartedTaskCount: 1},
		{WakeupID: "wakeup-after"},
	} {
		if err := emitWatchDecisionWithState(&output, "wakeup", decision, delivered, &lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered); err != nil {
			t.Fatalf("emitWatchDecision(%s): %v", decision.WakeupID, err)
		}
	}

	want := "atct wakeup: actionable_goals=0 unassigned_goals=0 unstarted_tasks=0 waiting_answer_tasks=0 untouched_tasks=0 delegated_tasks=0 waiting_answers=0 unassigned=[]\n" +
		"atct wakeup: actionable_goals=0 unassigned_goals=0 unstarted_tasks=1 waiting_answer_tasks=0 untouched_tasks=0 delegated_tasks=0 waiting_answers=0 unassigned=[]\n" +
		"atct wakeup: actionable_goals=0 unassigned_goals=0 unstarted_tasks=0 waiting_answer_tasks=0 untouched_tasks=0 delegated_tasks=0 waiting_answers=0 unassigned=[]\n"
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
	return runWatchWithProjectsAndGoal(t, cwd, projects, "")
}

func runWatchWithProjectsAndGoal(t *testing.T, cwd string, projects []watchProject, goalID string) (url.Values, int) {
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

	if err := watchWithURLsAndProjectAndGoal(ctx, []string{server.URL}, &output, server.Client(), time.Millisecond, cwd, goalID); err != nil {
		t.Fatalf("watchWithURLsAndProjectAndGoal() error = %v", err)
	}
	query := <-queries
	mu.Lock()
	defer mu.Unlock()
	return query, projectCalls
}

func TestWatchPassesGoalIDToEvents(t *testing.T) {
	cwd := t.TempDir()
	query, _ := runWatchWithProjectsAndGoal(t, cwd, []watchProject{{ID: "project-1", RootPath: cwd}}, "goal-1")
	if got := query.Get("goal_id"); got != "goal-1" {
		t.Fatalf("events goal_id = %q, want %q", got, "goal-1")
	}
}

func TestWatchOmitsGoalIDWhenNotSet(t *testing.T) {
	cwd := t.TempDir()
	query, _ := runWatchWithProjectsAndGoal(t, cwd, []watchProject{{ID: "project-1", RootPath: cwd}}, "")
	if _, ok := query["goal_id"]; ok {
		t.Fatalf("events unexpectedly received goal_id = %q", query.Get("goal_id"))
	}
}

func TestParseWatchGoal(t *testing.T) {
	config, err := parseArgs([]string{"watch", "--goal", "goal-1"})
	if err != nil {
		t.Fatalf("parseArgs(watch --goal): %v", err)
	}
	if config.watchGoalID != "goal-1" {
		t.Fatalf("watch goal_id = %q, want %q", config.watchGoalID, "goal-1")
	}

	config, err = parseArgs([]string{"watch"})
	if err != nil {
		t.Fatalf("parseArgs(watch): %v", err)
	}
	if config.watchGoalID != "" {
		t.Fatalf("watch goal_id = %q, want empty", config.watchGoalID)
	}
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
		{"detection.handoff_unreceived", watchDecision{HandoffID: "handoff-1"}, "atct detection: handoff handoff-1 has no receipt"},
		{"detection.handoff_unreported", watchDecision{HandoffID: "handoff-2"}, "atct detection: handoff handoff-2 has no completion report"},
		{"handoff_reported", watchDecision{HandoffID: "handoff-3", TaskID: "task-3", CompleteReport: "task report"}, "atct handoff reported: task task-3 (handoff handoff-3): task report"},
		{"handoff_reported", watchDecision{HandoffID: "handoff-4", GoalID: "goal-4", CompleteReport: "goal report"}, "atct handoff reported: goal goal-4 (handoff handoff-4): goal report"},
		{"handoff_yielded", watchDecision{TaskID: "task-yielded"}, "atct handoff yielded: task task-yielded"},
		{"detection.claim_undelegated", watchDecision{TaskID: "task-2"}, "atct detection: task task-2 has no handoff request"},
		{"detection.decision_answered_unapplied", watchDecision{DecisionID: "decision-1", GoalID: "goal-1"}, "atct detection: decision decision-1 was answered but not applied"},
		{"detection.decision_default_unapplied", watchDecision{DecisionID: "decision-2", GoalID: "goal-1"}, "atct detection: decision decision-2 was default-applied but not applied"},
		{"detection.claim_stale", watchDecision{TaskID: "task-3"}, "atct detection: task task-3 has a stale claim"},
	}
	for _, tc := range cases {
		var output bytes.Buffer
		delivered := make(map[watchDeliveryKey]struct{})
		wakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
		detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})
		if err := emitWatchDecisionWithState(&output, tc.event, tc.record, delivered, nil, wakeupDiscrepancyDelivered, detectionDelivered); err != nil {
			t.Fatalf("emitWatchDecision(%s): %v", tc.event, err)
		}
		if got := strings.TrimSpace(output.String()); got != tc.want {
			t.Fatalf("%s wrote %q, want %q", tc.event, got, tc.want)
		}
	}
}

func TestEmitWatchHandoffYieldedRepeatsEveryDelivery(t *testing.T) {
	var output bytes.Buffer
	delivered := make(map[watchDeliveryKey]struct{})
	wakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})
	record := watchDecision{TaskID: "task-yielded"}

	for range 2 {
		if err := emitWatchDecisionWithState(&output, "handoff_yielded", record, delivered, nil, wakeupDiscrepancyDelivered, detectionDelivered); err != nil {
			t.Fatalf("emitWatchDecision: %v", err)
		}
	}

	want := "atct handoff yielded: task task-yielded\n" +
		"atct handoff yielded: task task-yielded\n"
	if got := output.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestEmitWatchDetectionDoesNotRepeatSameTarget(t *testing.T) {
	var output bytes.Buffer
	delivered := make(map[watchDeliveryKey]struct{})
	wakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	// A fresh detection_id on the second delivery must not make it look new;
	// the key is the target, not the occurrence.
	first := watchDecision{GoalID: "goal-1", DetectionID: "detection-1"}
	second := watchDecision{GoalID: "goal-1", DetectionID: "detection-2"}
	for _, record := range []watchDecision{first, second} {
		if err := emitWatchDecisionWithState(&output, "detection.commits_missing", record, delivered, nil, wakeupDiscrepancyDelivered, detectionDelivered); err != nil {
			t.Fatalf("emitWatchDecision: %v", err)
		}
	}
	if lines := strings.Count(strings.TrimSpace(output.String()), "\n"); lines != 0 {
		t.Fatalf("output = %q, want a single line", output.String())
	}
}

func TestEmitWatchDetectionDoesNotRepeatSameDecision(t *testing.T) {
	var output bytes.Buffer
	delivered := make(map[watchDeliveryKey]struct{})
	wakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	for _, record := range []watchDecision{
		{ID: "event-1", DecisionID: "decision-1", GoalID: "goal-1"},
		{ID: "event-2", DecisionID: "decision-1", GoalID: "goal-2"},
	} {
		if err := emitWatchDecisionWithState(&output, "detection.decision_answered_unapplied", record, delivered, nil, wakeupDiscrepancyDelivered, detectionDelivered); err != nil {
			t.Fatalf("emitWatchDecision: %v", err)
		}
	}

	want := "atct detection: decision decision-1 was answered but not applied\n"
	if got := output.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestEmitWatchDetectionDoesNotRepeatSameHandoff(t *testing.T) {
	var output bytes.Buffer
	delivered := make(map[watchDeliveryKey]struct{})
	wakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	for _, record := range []watchDecision{
		{HandoffID: "handoff-1", DetectionID: "detection-1"},
		{HandoffID: "handoff-1", DetectionID: "detection-2"},
	} {
		if err := emitWatchDecisionWithState(&output, "detection.handoff_unreceived", record, delivered, nil, wakeupDiscrepancyDelivered, detectionDelivered); err != nil {
			t.Fatalf("emitWatchDecision: %v", err)
		}
	}
	want := "atct detection: handoff handoff-1 has no receipt\n"
	if got := output.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestEmitWatchHandoffReportedDoesNotRepeatSameHandoff(t *testing.T) {
	var output bytes.Buffer
	delivered := make(map[watchDeliveryKey]struct{})
	wakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	for _, record := range []watchDecision{
		{HandoffID: "handoff-1", TaskID: "task-1", CompleteReport: "first report", DetectionID: "detection-1"},
		{HandoffID: "handoff-1", TaskID: "task-1", CompleteReport: "first report", DetectionID: "detection-2"},
	} {
		if err := emitWatchDecisionWithState(&output, "handoff_reported", record, delivered, nil, wakeupDiscrepancyDelivered, detectionDelivered); err != nil {
			t.Fatalf("emitWatchDecision: %v", err)
		}
	}
	want := "atct handoff reported: task task-1 (handoff handoff-1): first report\n"
	if got := output.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestEmitWatchHandoffReportedTruncatesLongReport(t *testing.T) {
	var output bytes.Buffer
	delivered := make(map[watchDeliveryKey]struct{})
	wakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})
	report := strings.Repeat("x", 81)

	if err := emitWatchDecisionWithState(&output, "handoff_reported", watchDecision{
		HandoffID:      "handoff-long",
		GoalID:         "goal-long",
		CompleteReport: report,
	}, delivered, nil, wakeupDiscrepancyDelivered, detectionDelivered); err != nil {
		t.Fatalf("emitWatchDecision: %v", err)
	}
	want := "atct handoff reported: goal goal-long (handoff handoff-long): " + strings.Repeat("x", 80) + "…\n"
	if got := output.String(); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestEmitWatchHandoffReportedRejectsMissingTarget(t *testing.T) {
	var output bytes.Buffer
	delivered := make(map[watchDeliveryKey]struct{})
	wakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	err := emitWatchDecisionWithState(&output, "handoff_reported", watchDecision{CompleteReport: "report"}, delivered, nil, wakeupDiscrepancyDelivered, detectionDelivered)
	if err == nil {
		t.Fatal("emitWatchDecision() with no task or goal target = nil, want an error")
	}
	if output.Len() != 0 {
		t.Fatalf("output = %q, want nothing", output.String())
	}
}

func TestEmitWatchDetectionSeparatesTargets(t *testing.T) {
	var output bytes.Buffer
	delivered := make(map[watchDeliveryKey]struct{})
	wakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	// Deduplication must not be so broad that a second goal with the same
	// condition goes unreported.
	for _, goalID := range []string{"goal-1", "goal-2"} {
		if err := emitWatchDecisionWithState(&output, "detection.commits_missing", watchDecision{GoalID: goalID}, delivered, nil, wakeupDiscrepancyDelivered, detectionDelivered); err != nil {
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
	wakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	if err := emitWatchDecisionWithState(&output, "goal.created", watchDecision{GoalID: "goal-1"}, delivered, nil, wakeupDiscrepancyDelivered, detectionDelivered); err != nil {
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
	wakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	for _, decision := range []watchDecision{
		{ID: "event-1", GoalID: "goal-1"},
		{ID: "event-2", GoalID: "goal-1"},
	} {
		if err := emitWatchDecisionWithState(&output, "goal.created", decision, delivered, nil, wakeupDiscrepancyDelivered, detectionDelivered); err != nil {
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
	wakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	for _, goalID := range []string{"goal-1", "goal-2"} {
		if err := emitWatchDecisionWithState(&output, "goal.created", watchDecision{GoalID: goalID}, delivered, nil, wakeupDiscrepancyDelivered, detectionDelivered); err != nil {
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
	wakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	// A newer daemon may publish conditions this build has never heard of.
	// Staying quiet is correct; failing is not.
	if err := emitWatchDecisionWithState(&output, "detection.something_new", watchDecision{GoalID: "goal-1"}, delivered, nil, wakeupDiscrepancyDelivered, detectionDelivered); err != nil {
		t.Fatalf("emitWatchDecision(unknown): %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("output = %q, want nothing", output.String())
	}
}

func TestEmitWatchDetectionRejectsMissingTarget(t *testing.T) {
	var output bytes.Buffer
	delivered := make(map[watchDeliveryKey]struct{})
	wakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})

	err := emitWatchDecisionWithState(&output, "detection.commits_missing", watchDecision{}, delivered, nil, wakeupDiscrepancyDelivered, detectionDelivered)
	if err == nil {
		t.Fatal("emitWatchDecision() with no target = nil, want an error")
	}
	if output.Len() != 0 {
		t.Fatalf("output = %q, want nothing", output.String())
	}
}
