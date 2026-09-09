package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/michiomochi/atct/internal/daemonctl"
	"github.com/michiomochi/atct/internal/store"
)

const (
	watchReconnectInterval      = 5 * time.Second
	watchSnapshotTimeout        = 5 * time.Second
	watchKeepaliveTimeout       = 90 * time.Second
	watchReconcileInterval      = 30 * time.Second
	watchLivenessPromptInterval = 10 * time.Minute
	watchEnsureMaxFailures      = 5
	watchEnsureLimitMessage     = "atct watch: daemon ensure failed 5 consecutive times; continuing connection retries"
)

type watchDecision struct {
	ID                         string  `json:"id"`
	DecisionID                 string  `json:"decision_id"`
	ProjectID                  string  `json:"project_id"`
	Kind                       string  `json:"kind"`
	AnswerLabel                string  `json:"answer_label"`
	DefaultAppliedAt           *string `json:"default_applied_at"`
	SettledByDefault           bool    `json:"settled_by_default"`
	WakeupID                   string  `json:"wakeup_id"`
	Reason                     string  `json:"reason"`
	ActionableGoalCount        int     `json:"actionable_goal_count"`
	UnassignedGoalCount        int     `json:"unassigned_goal_count"`
	UnassignedGoalIDs          []int64 `json:"unassigned_goal_ids"`
	UnstartedTaskCount         int     `json:"unstarted_task_count"`
	WaitingAnswerTaskCount     int     `json:"waiting_answer_task_count"`
	UntouchedTaskCount         int     `json:"untouched_task_count"`
	DelegatedTaskCount         int     `json:"delegated_task_count"`
	WaitingAnswerCount         int     `json:"waiting_answer_count"`
	DetectorUnstartedTaskCount int     `json:"detector_unstarted_task_count"`
	CountedUnstartedTaskCount  int     `json:"counted_unstarted_task_count"`
	DetectionID                string  `json:"detection_id"`
	GoalID                     string  `json:"goal_id"`
	TaskID                     string  `json:"task_id"`
	HandoffID                  string  `json:"handoff_id"`
	WorktreeActivity           string  `json:"worktree_activity"`
	CompleteReport             string  `json:"complete_report"`
	Status                     string  `json:"status"`
	Condition                  string  `json:"condition"`
	TargetRole                 string  `json:"target_role"`
	ScopeKey                   string  `json:"scope_key"`
	Generation                 string  `json:"generation"`
	BlockerID                  string  `json:"blocker_id"`
	BlockerKind                string  `json:"blocker_kind"`
	SourceID                   string  `json:"source_id"`
	ExpectedRole               string  `json:"expected_role"`
	ExpectedAgentSessionID     int64   `json:"expected_agent_session_id"`
	ExpectedAgentKey           string  `json:"expected_agent_key"`
	ObservedRole               string  `json:"observed_role"`
	ObservedAgentSessionID     int64   `json:"observed_agent_session_id"`
	ObservedAgentKey           string  `json:"observed_agent_key"`
	Instruction                string  `json:"instruction"`
	deliveryGeneration         string
}

type watchInbox struct {
	UnappliedDecisions []watchDecision `json:"unapplied_decisions"`
}

type watchProject struct {
	ID       string `json:"id"`
	RootPath string `json:"root_path"`
}

// decodeEntityID accepts numeric IDs emitted by the daemon and preserves string
// input for URL/query and delivery-map keys. The CLI keeps IDs as strings
// internally even though the canonical IDs are numeric.
func decodeEntityID(data []byte) (string, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		return "", nil
	}

	var text string
	if err := json.Unmarshal([]byte(trimmed), &text); err == nil {
		return text, nil
	}

	var number json.Number
	if err := json.Unmarshal([]byte(trimmed), &number); err != nil {
		return "", fmt.Errorf("entity ID must be an integer or string: %w", err)
	}
	if _, err := strconv.ParseInt(number.String(), 10, 64); err != nil {
		return "", fmt.Errorf("entity ID must be an integer or string: %w", err)
	}
	return number.String(), nil
}

func decodeEntityIDObject(data []byte, ids map[string]*string, target any) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for name, destination := range ids {
		raw, ok := fields[name]
		if !ok {
			continue
		}
		value, err := decodeEntityID(raw)
		if err != nil {
			return fmt.Errorf("decode %s: %w", name, err)
		}
		*destination = value
		delete(fields, name)
	}
	rest, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return json.Unmarshal(rest, target)
}

func (d *watchDecision) UnmarshalJSON(data []byte) error {
	type plain watchDecision
	var decoded plain
	if err := decodeEntityIDObject(data, map[string]*string{
		"id":           &decoded.ID,
		"decision_id":  &decoded.DecisionID,
		"project_id":   &decoded.ProjectID,
		"wakeup_id":    &decoded.WakeupID,
		"detection_id": &decoded.DetectionID,
		"goal_id":      &decoded.GoalID,
		"task_id":      &decoded.TaskID,
		"handoff_id":   &decoded.HandoffID,
	}, &decoded); err != nil {
		return err
	}
	*d = watchDecision(decoded)
	return nil
}

func (p *watchProject) UnmarshalJSON(data []byte) error {
	type plain watchProject
	var decoded plain
	if err := decodeEntityIDObject(data, map[string]*string{
		"id": &decoded.ID,
	}, &decoded); err != nil {
		return err
	}
	*p = watchProject(decoded)
	return nil
}

type watchDeliveryKey struct {
	eventName      string
	decisionID     string
	defaultApplied bool
}

type watchWakeupDeliveryKey struct {
	eventName string
	wakeupID  string
}

// Keyed by the target rather than the detection id, which is fresh on every
// publish: the point is to say a condition once per goal, handoff, or task, not
// once per occurrence.
type watchDetectionDeliveryKey struct {
	eventName  string
	targetID   string
	generation string
}

type watchSnapshotFunc func(context.Context) (string, []watchDecision, error)
type watchEnsureFunc func() error

type watchLivenessState struct {
	lastPromptAt time.Time
}

type watchHealthSink interface {
	Report(context.Context, string, string, string)
	Stop()
}

type watchHealthScopeIdentity struct {
	ScopeKey       string
	AgentSessionID int64
	AgentKey       string
}

type watchHealthScopeSetter interface {
	SetExpectedScope(watchHealthScopeIdentity)
}

type watchHealthScopeClearer interface {
	ClearExpectedScope()
}

// watchHealthReporter publishes the health of any eligible watch. It is kept
// in the watch package boundary because both the normal Claude watch and the
// Codex bridge use the same reconciliation lifecycle.
type watchHealthReporter struct {
	client *http.Client
	urls   []string
	health store.MonitorHealth

	mu             sync.Mutex
	lastBaseURL    string
	state          string
	transitionedAt time.Time
	scopeActive    bool
}

func newWatchHealthReporter(client *http.Client, urls []string, cwd string, scope watchScope) *watchHealthReporter {
	if scope.Role != "commander" && scope.Role != "subcommander" && scope.Role != "executor" {
		return nil
	}
	projectID, err := strconv.ParseInt(scope.ProjectID, 10, 64)
	if err != nil || projectID <= 0 {
		return nil
	}
	goalID, taskID := monitorScopeID(scope.GoalID), monitorScopeID(scope.TaskID)
	if scope.Role == "commander" && (goalID != nil || taskID != nil) ||
		scope.Role == "subcommander" && (goalID == nil || taskID != nil) ||
		scope.Role == "executor" && (goalID == nil || taskID == nil) {
		return nil
	}
	processStartedAt, err := daemonctl.CodexMonitorProcessStartTime(os.Getpid())
	if err != nil {
		return nil
	}
	absCWD, err := filepath.Abs(filepath.Clean(cwd))
	if err != nil {
		return nil
	}
	health := store.MonitorHealth{
		CWD:              absCWD,
		Role:             scope.Role,
		ProjectID:        projectID,
		GoalID:           goalID,
		TaskID:           taskID,
		ScopeKey:         strings.TrimSpace(scope.ScopeKey),
		AgentSessionID:   currentAgentSessionID(),
		PID:              os.Getpid(),
		ProcessStartedAt: processStartedAt,
	}
	health.MonitorID = store.MonitorHealthID(health.CWD, health.Role, health.ProjectID, health.GoalID, health.TaskID, health.PID, health.ProcessStartedAt, health.ScopeKey)
	return &watchHealthReporter{client: client, urls: append([]string(nil), urls...), health: health, scopeActive: true}
}

func monitorScopeID(value string) *int64 {
	id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || id <= 0 {
		return nil
	}
	return &id
}

func (r *watchHealthReporter) Report(ctx context.Context, baseURL, state, reason string) {
	if r == nil {
		return
	}
	now := time.Now().UTC()
	r.mu.Lock()
	if !r.scopeActive {
		r.mu.Unlock()
		return
	}
	if baseURL != "" {
		r.lastBaseURL = strings.TrimRight(baseURL, "/")
	}
	if state != r.state {
		r.state = state
		r.transitionedAt = now
	}
	health := r.health
	health.State = state
	health.Reason = reason
	health.TransitionedAt = r.transitionedAt
	health.LastSeenAt = now
	preferred := r.lastBaseURL
	r.mu.Unlock()
	r.post(ctx, preferred, health)
}

func (r *watchHealthReporter) SetExpectedScope(identity watchHealthScopeIdentity) {
	if r == nil {
		return
	}
	if strings.TrimSpace(identity.ScopeKey) == "" {
		r.ClearExpectedScope()
		return
	}
	r.mu.Lock()
	r.health.ScopeKey = strings.TrimSpace(identity.ScopeKey)
	r.health.AgentSessionID = identity.AgentSessionID
	r.health.AgentKey = strings.TrimSpace(identity.AgentKey)
	r.health.MonitorID = store.MonitorHealthID(r.health.CWD, r.health.Role, r.health.ProjectID, r.health.GoalID, r.health.TaskID, r.health.PID, r.health.ProcessStartedAt, r.health.ScopeKey)
	r.scopeActive = true
	r.mu.Unlock()
}

func (r *watchHealthReporter) ClearExpectedScope() {
	if r == nil {
		return
	}
	now := time.Now().UTC()
	r.mu.Lock()
	if !r.scopeActive {
		r.mu.Unlock()
		return
	}
	health := r.health
	health.State = "stopped"
	health.Reason = "stopped"
	health.TransitionedAt = now
	health.LastSeenAt = now
	health.StoppedAt = &now
	preferred := r.lastBaseURL
	r.scopeActive = false
	r.health.ScopeKey = ""
	r.health.AgentSessionID = 0
	r.health.AgentKey = ""
	r.health.MonitorID = store.MonitorHealthID(r.health.CWD, r.health.Role, r.health.ProjectID, r.health.GoalID, r.health.TaskID, r.health.PID, r.health.ProcessStartedAt, "")
	r.mu.Unlock()
	r.post(context.Background(), preferred, health)
}

func (r *watchHealthReporter) Stop() {
	r.ClearExpectedScope()
}

func (r *watchHealthReporter) post(ctx context.Context, preferred string, health store.MonitorHealth) {
	if ctx == nil {
		ctx = context.Background()
	}
	postCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	bases := make([]string, 0, len(r.urls)+1)
	if preferred != "" {
		bases = append(bases, preferred)
	}
	for _, base := range r.urls {
		base = strings.TrimRight(base, "/")
		if base == "" || base == preferred {
			continue
		}
		bases = append(bases, base)
	}
	for _, base := range bases {
		body, err := json.Marshal(health)
		if err != nil {
			return
		}
		req, err := http.NewRequestWithContext(postCtx, http.MethodPost, base+"/api/monitor-health", strings.NewReader(string(body)))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := r.client.Do(req)
		if err != nil {
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			r.mu.Lock()
			r.lastBaseURL = base
			r.mu.Unlock()
			return
		}
	}
}

type watchRecoveryState struct {
	state          string
	ensureFailures int
}

func newWatchRecoveryState() *watchRecoveryState {
	return &watchRecoveryState{state: "healthy"}
}

func (s *watchRecoveryState) Failure(report func(string)) {
	if s.state == "healthy" {
		s.state = "recovering"
		report("recovering")
	}
}

func (s *watchRecoveryState) EnsureFailure(report func(string)) {
	s.ensureFailures++
	if s.ensureFailures >= watchEnsureMaxFailures && s.state != "degraded" {
		s.state = "degraded"
		report("degraded")
	}
}

func (s *watchRecoveryState) EnsureSucceeded() {
	s.ensureFailures = 0
}

func (s *watchRecoveryState) Reconciled(report func(string)) {
	s.ensureFailures = 0
	if s.state != "healthy" {
		s.state = "healthy"
		report("healthy")
	}
}

func newWatchLivenessState(start time.Time) *watchLivenessState {
	return &watchLivenessState{lastPromptAt: start}
}

func (s *watchLivenessState) PromptDue(now time.Time, scope watchScope, snapshot watchReconciliation) bool {
	if !watchLivenessEligible(scope) || scopedOpenDecision(scope, snapshot) {
		s.lastPromptAt = now
		return false
	}
	if now.Sub(s.lastPromptAt) < watchLivenessPromptInterval {
		return false
	}
	s.lastPromptAt = now
	return true
}

type watchSinkError struct {
	err error
}

func (e *watchSinkError) Error() string {
	return e.err.Error()
}

func (e *watchSinkError) Unwrap() error {
	return e.err
}

func normalWatchScope(projectID, goalID string) watchScope {
	scope := watchScope{ProjectID: projectID, GoalID: goalID}
	if goalID != "" {
		scope.Role = "subcommander"
	}
	return scope
}

func runWatch(dir, goalID string) error {
	return runWatchWithOptions(dir, goalID, false, false)
}

func runWatchWithOptions(dir, goalID string, projectScope, monitor bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}

	client := &http.Client{}
	baseURLs := watchBaseURLs(dir)
	projectID := ""
	for _, baseURL := range baseURLs {
		projects, err := fetchWatchProjects(ctx, client, baseURL)
		if err != nil {
			continue
		}
		projectID = resolveWatchProjectID(cwd, projects)
		break
	}

	watchRegistrationScope := daemonctl.WatchScope{ProjectID: projectID, GoalID: goalID}
	humanOutput := io.Writer(os.Stdout)
	watchOutput := humanOutput
	var actionSink watchAgentActionSink
	if monitor {
		watchOutput = io.Discard
		writer := monitorActionWriter{writer: os.Stdout}
		actionSink = writer.Sink
	}
	cleanup, err := daemonctl.RegisterWatchScoped(dir, watchRegistrationScope)
	if err != nil {
		return fmt.Errorf("register watch: %w", err)
	}
	defer cleanup()

	if projectID != "" {
		result, err := daemonctl.ReapWatches(dir, watchRegistrationScope, os.Getpid())
		if err != nil {
			return fmt.Errorf("reap watches: %w", err)
		}
		if result.RemovedStale > 0 {
			word := "registrations"
			if result.RemovedStale == 1 {
				word = "registration"
			}
			if _, err := fmt.Fprintf(watchOutput, "atct watch: removed %d stale watch %s\n", result.RemovedStale, word); err != nil {
				return fmt.Errorf("write watch reap report: %w", err)
			}
		}
		if len(result.Stopped) > 0 {
			word := "watches"
			if len(result.Stopped) == 1 {
				word = "watch"
			}
			details := make([]string, 0, len(result.Stopped))
			for _, registration := range result.Stopped {
				watchScope := "project-wide"
				if registration.Scope.GoalID != "" {
					watchScope = "goal " + registration.Scope.GoalID
				}
				details = append(details, fmt.Sprintf("pid %d, %s", registration.PID, watchScope))
			}
			if _, err := fmt.Fprintf(watchOutput, "atct watch: stopped %d duplicate %s (%s)\n", len(result.Stopped), word, strings.Join(details, ", ")); err != nil {
				return fmt.Errorf("write watch reap report: %w", err)
			}
		}
		for _, pid := range result.Failed {
			if _, err := fmt.Fprintf(watchOutput, "atct watch: duplicate watch pid %d did not exit within 5s\n", pid); err != nil {
				return fmt.Errorf("write watch reap report: %w", err)
			}
		}
		registrations, err := daemonctl.ListWatches(dir)
		if err == nil {
			liveRegistrations := make([]daemonctl.WatchRegistration, 0, len(registrations))
			for _, registration := range registrations {
				if daemonctl.ProcessAlive(registration.PID) {
					liveRegistrations = append(liveRegistrations, registration)
				}
			}
			if _, err := fmt.Fprintln(watchOutput, daemonctl.WatchRosterLine(liveRegistrations, projectID)); err != nil {
				return fmt.Errorf("write watch roster: %w", err)
			}
		}
	}

	if projectScope {
		goalID = ""
	}
	scope := normalWatchScope(projectID, goalID)
	if monitor && goalID == "" {
		scope.Role = "commander"
	}
	snapshot, projectIDGetter := watchSnapshotWithProject(client, baseURLs, cwd)
	reporter := newWatchHealthReporter(client, baseURLs, cwd, scope)
	var reporters []watchHealthSink
	if reporter != nil {
		reporters = append(reporters, reporter)
	}
	return watchLoopWithEnsureAndProjectIDAndScopeAndActionSink(ctx, watchOutput, client, watchReconnectInterval, snapshot, func() error {
		return ensureWatchDaemon(dir)
	}, projectIDGetter, scope, nil, actionSink, reporters...)
}

func ensureWatchDaemon(dir string) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	_, err = daemonctl.Ensure(daemonctl.Config{
		Dir:        dir,
		Version:    version,
		Executable: executable,
		ListenAddr: defaultListenAddr,
	})
	return err
}

func watchWithURLs(ctx context.Context, urls []string, out io.Writer, client *http.Client, retryInterval time.Duration) error {
	if client == nil {
		client = &http.Client{}
	}
	return watchLoop(ctx, out, client, retryInterval, func(ctx context.Context) (string, []watchDecision, error) {
		return fetchWatchSnapshot(ctx, client, urls)
	})
}

func watchWithURLsAndProject(ctx context.Context, urls []string, out io.Writer, client *http.Client, retryInterval time.Duration, cwd string) error {
	return watchWithURLsAndProjectAndGoal(ctx, urls, out, client, retryInterval, cwd, "")
}

func watchWithURLsAndProjectAndGoal(ctx context.Context, urls []string, out io.Writer, client *http.Client, retryInterval time.Duration, cwd, goalID string) error {
	if client == nil {
		client = &http.Client{}
	}
	snapshot, projectID := watchSnapshotWithProject(client, urls, cwd)
	return watchLoopWithEnsureAndProjectIDAndScopeAndSinkAndCursor(ctx, out, client, retryInterval, snapshot, nil, projectID, watchScope{GoalID: goalID}, nil, "")
}

func watchLoop(ctx context.Context, out io.Writer, client *http.Client, retryInterval time.Duration, snapshot watchSnapshotFunc) error {
	return watchLoopWithEnsureAndProjectID(ctx, out, client, retryInterval, snapshot, nil, nil)
}

func watchLoopWithEnsure(ctx context.Context, out io.Writer, client *http.Client, retryInterval time.Duration, snapshot watchSnapshotFunc, ensure watchEnsureFunc) error {
	return watchLoopWithEnsureAndProjectID(ctx, out, client, retryInterval, snapshot, ensure, nil)
}

func watchLoopWithEnsureAndProjectID(ctx context.Context, out io.Writer, client *http.Client, retryInterval time.Duration, snapshot watchSnapshotFunc, ensure watchEnsureFunc, projectID func() string) error {
	return watchLoopWithEnsureAndProjectIDAndGoal(ctx, out, client, retryInterval, snapshot, ensure, projectID, "")
}

func watchLoopWithEnsureAndProjectIDAndGoal(ctx context.Context, out io.Writer, client *http.Client, retryInterval time.Duration, snapshot watchSnapshotFunc, ensure watchEnsureFunc, projectID func() string, goalID string) error {
	return watchLoopWithEnsureAndProjectIDAndGoalAndSink(ctx, out, client, retryInterval, snapshot, ensure, projectID, goalID, nil)
}

func watchLoopWithEnsureAndProjectIDAndGoalAndSink(ctx context.Context, out io.Writer, client *http.Client, retryInterval time.Duration, snapshot watchSnapshotFunc, ensure watchEnsureFunc, projectID func() string, goalID string, sink watchRawLineSink) error {
	return watchLoopWithEnsureAndProjectIDAndScopeAndSink(ctx, out, client, retryInterval, snapshot, ensure, projectID, watchScope{GoalID: goalID}, sink)
}

func watchLoopWithEnsureAndProjectIDAndScopeAndSink(ctx context.Context, out io.Writer, client *http.Client, retryInterval time.Duration, snapshot watchSnapshotFunc, ensure watchEnsureFunc, projectID func() string, scope watchScope, sink watchRawLineSink) error {
	return watchLoopWithEnsureAndProjectIDAndScopeAndSinkAndCursor(ctx, out, client, retryInterval, snapshot, ensure, projectID, scope, sink, "")
}

// watchLoopWithEnsureAndProjectIDAndScopeAndSinkAndCursor keeps the old
// call shape for the Codex monitor bridge. The final argument is ignored:
// watch delivery no longer has a durable cursor.
func watchLoopWithEnsureAndProjectIDAndScopeAndSinkAndCursor(ctx context.Context, out io.Writer, client *http.Client, retryInterval time.Duration, snapshot watchSnapshotFunc, ensure watchEnsureFunc, projectID func() string, scope watchScope, sink watchRawLineSink, _ string, reporters ...watchHealthSink) error {
	return watchLoopWithEnsureAndProjectIDAndScopeAndActionSink(ctx, out, client, retryInterval, snapshot, ensure, projectID, scope, sink, nil, reporters...)
}

func watchLoopWithEnsureAndProjectIDAndScopeAndActionSink(ctx context.Context, out io.Writer, client *http.Client, retryInterval time.Duration, snapshot watchSnapshotFunc, ensure watchEnsureFunc, projectID func() string, scope watchScope, sink watchRawLineSink, actionSink watchAgentActionSink, reporters ...watchHealthSink) error {
	if retryInterval <= 0 {
		retryInterval = watchReconnectInterval
	}
	delivered := make(map[watchDeliveryKey]struct{})
	scopeFilter := newWatchScopeFilter(scope.GoalID)
	if scope.TaskID != "" {
		scopeFilter = newWatchTaskScopeFilter(scope.TaskID)
	}
	// Keep only the last rendered wakeup content in this watch loop. On daemon
	// restart the daemon forgets its history, but this watch retains its last
	// content across reconnects and suppresses an unchanged first post-restart
	// wakeup; a changed line is sent. A newly started watch has no prior content
	// and sends its first current wakeup. State is per watch loop so a later
	// watch is not silenced by another watch's delivery.
	var lastWakeupContent string
	wakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})
	latestReconciliation := watchReconciliation{}
	recoveryState := newWatchRecoveryState()
	ensureDisabled := false
	var healthReporter watchHealthSink
	if len(reporters) > 0 {
		healthReporter = reporters[0]
		defer healthReporter.Stop()
	}
	reportHealth := func(baseURL, state, reason string) {
		if healthReporter != nil {
			healthReporter.Report(ctx, baseURL, state, reason)
		}
	}
	recoverDaemon := func() error {
		if ensure == nil || ensureDisabled || ctx.Err() != nil {
			return nil
		}
		if err := ensure(); err != nil {
			recoveryState.EnsureFailure(func(state string) {
				reportHealth("", state, err.Error())
			})
			if _, writeErr := fmt.Fprintln(out, err); writeErr != nil {
				return writeErr
			}
			if recoveryState.ensureFailures >= watchEnsureMaxFailures {
				ensureDisabled = true
				if _, writeErr := fmt.Fprintln(out, watchEnsureLimitMessage); writeErr != nil {
					return writeErr
				}
			}
			return nil
		}
		recoveryState.EnsureSucceeded()
		ensureDisabled = false
		return nil
	}
	resetEnsureFailures := func() {
		recoveryState.EnsureSucceeded()
		ensureDisabled = false
	}
	reconcileSucceeded := func(baseURL string) {
		wasRecovering := recoveryState.state != "healthy"
		recoveryState.Reconciled(func(state string) {
			reportHealth(baseURL, state, "reconciliation succeeded")
		})
		if !wasRecovering {
			reportHealth(baseURL, "healthy", "reconciliation succeeded")
		}
	}

	for {
		baseURL, _, err := snapshot(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			recoveryState.Failure(func(state string) {
				reportHealth("", state, err.Error())
			})
			if err := recoverDaemon(); err != nil {
				return err
			}
			if err := waitForWatchReconnect(ctx, out, retryInterval); err != nil {
				return err
			}
			continue
		}
		resetEnsureFailures()

		filterProjectID := ""
		if projectID != nil {
			filterProjectID = projectID()
		}
		streamScope := scope
		streamScope.ProjectID = filterProjectID
		if err := reconcileWatchScope(ctx, client, baseURL, streamScope, out, delivered, &lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered, scopeFilter, sink, actionSink, &latestReconciliation, healthReporter); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var sinkErr *watchSinkError
			if errors.As(err, &sinkErr) {
				return err
			}
			recoveryState.Failure(func(state string) {
				reportHealth(baseURL, state, err.Error())
			})
			if err := recoverDaemon(); err != nil {
				return err
			}
			if err := waitForWatchReconnect(ctx, out, retryInterval); err != nil {
				return err
			}
			continue
		}
		reconcileSucceeded(baseURL)

		err = consumeWatchEventsWithStateAndScopeAndSinkAndCursor(ctx, client, baseURL, streamScope, out, watchKeepaliveTimeout, delivered, &lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered, scopeFilter, sink, actionSink, &latestReconciliation, func() {
			reconcileSucceeded(baseURL)
		})
		if err != nil && ctx.Err() == nil {
			var sinkErr *watchSinkError
			if errors.As(err, &sinkErr) {
				return err
			}
			recoveryState.Failure(func(state string) {
				reportHealth(baseURL, state, err.Error())
			})
			if err := recoverDaemon(); err != nil {
				return err
			}
			if err := waitForWatchReconnect(ctx, out, retryInterval); err != nil {
				return err
			}
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

func fetchWatchSnapshot(ctx context.Context, client *http.Client, urls []string) (string, []watchDecision, error) {
	if len(urls) == 0 {
		return "", nil, errors.New("no daemon HTTP addresses to try")
	}
	var lastErr error
	for _, baseURL := range urls {
		baseURL = strings.TrimRight(baseURL, "/")
		snapshotCtx, cancel := context.WithTimeout(ctx, watchSnapshotTimeout)
		req, err := http.NewRequestWithContext(snapshotCtx, http.MethodGet, baseURL+"/api/inbox", nil)
		if err != nil {
			cancel()
			lastErr = err
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			cancel()
			lastErr = err
			continue
		}
		var inbox watchInbox
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			cancel()
			lastErr = fmt.Errorf("GET %s/api/inbox: HTTP %s", baseURL, resp.Status)
			continue
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&inbox)
		_ = resp.Body.Close()
		cancel()
		if decodeErr != nil {
			lastErr = fmt.Errorf("decode %s/api/inbox: %w", baseURL, decodeErr)
			continue
		}
		return baseURL, inbox.UnappliedDecisions, nil
	}
	if lastErr == nil {
		lastErr = errors.New("unable to read daemon inbox")
	}
	return "", nil, lastErr
}

func watchSnapshotWithProject(client *http.Client, urls []string, cwd string) (watchSnapshotFunc, func() string) {
	projectID := ""
	projectsFetched := false
	snapshot := func(ctx context.Context) (string, []watchDecision, error) {
		baseURL, decisions, err := fetchWatchSnapshot(ctx, client, urls)
		if err != nil {
			return "", nil, err
		}
		if !projectsFetched {
			projects, err := fetchWatchProjects(ctx, client, baseURL)
			if err != nil {
				return "", nil, err
			}
			projectsFetched = true
			projectID = resolveWatchProjectID(cwd, projects)
		}
		return baseURL, decisions, nil
	}
	return snapshot, func() string { return projectID }
}

func fetchWatchProjects(ctx context.Context, client *http.Client, baseURL string) ([]watchProject, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	projectsCtx, cancel := context.WithTimeout(ctx, watchSnapshotTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(projectsCtx, http.MethodGet, baseURL+"/api/projects", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("GET %s/api/projects: HTTP %s", baseURL, resp.Status)
	}
	var projects []watchProject
	if err := json.NewDecoder(resp.Body).Decode(&projects); err != nil {
		return nil, fmt.Errorf("decode %s/api/projects: %w", baseURL, err)
	}
	return projects, nil
}

func resolveWatchProjectID(cwd string, projects []watchProject) string {
	if strings.TrimSpace(cwd) == "" {
		return ""
	}
	absoluteCWD, err := filepath.Abs(filepath.Clean(cwd))
	if err != nil {
		return ""
	}
	bestID := ""
	bestRoot := ""
	for _, project := range projects {
		if project.ID == "" || strings.TrimSpace(project.RootPath) == "" {
			continue
		}
		root, err := filepath.Abs(filepath.Clean(project.RootPath))
		if err != nil || !watchPathWithin(root, absoluteCWD) {
			continue
		}
		if len(root) > len(bestRoot) {
			bestID = project.ID
			bestRoot = root
		}
	}
	return bestID
}

func watchPathWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func consumeWatchEvents(ctx context.Context, client *http.Client, baseURL, projectID string, out io.Writer, delivered map[watchDeliveryKey]struct{}) error {
	return consumeWatchEventsWithTimeout(ctx, client, baseURL, projectID, out, watchKeepaliveTimeout, delivered, make(map[watchWakeupDeliveryKey]struct{}), make(map[watchDetectionDeliveryKey]struct{}))
}

type watchSSEFrame struct {
	name string
	id   string
	data string
}

func consumeWatchEventsWithTimeout(ctx context.Context, client *http.Client, baseURL, projectID string, out io.Writer, keepaliveTimeout time.Duration, delivered map[watchDeliveryKey]struct{}, wakeupDiscrepancyDelivered map[watchWakeupDeliveryKey]struct{}, detectionDelivered map[watchDetectionDeliveryKey]struct{}) error {
	var lastWakeupContent string
	return consumeWatchEventsWithState(ctx, client, baseURL, projectID, out, keepaliveTimeout, delivered, &lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered)
}

func consumeWatchEventsWithState(ctx context.Context, client *http.Client, baseURL, projectID string, out io.Writer, keepaliveTimeout time.Duration, delivered map[watchDeliveryKey]struct{}, lastWakeupContent *string, wakeupDiscrepancyDelivered map[watchWakeupDeliveryKey]struct{}, detectionDelivered map[watchDetectionDeliveryKey]struct{}) error {
	return consumeWatchEventsWithStateAndGoal(ctx, client, baseURL, projectID, "", out, keepaliveTimeout, delivered, lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered, newWatchPassThroughFilter())
}

func consumeWatchEventsWithStateAndGoal(ctx context.Context, client *http.Client, baseURL, projectID, goalID string, out io.Writer, keepaliveTimeout time.Duration, delivered map[watchDeliveryKey]struct{}, lastWakeupContent *string, wakeupDiscrepancyDelivered map[watchWakeupDeliveryKey]struct{}, detectionDelivered map[watchDetectionDeliveryKey]struct{}, scopeFilter *watchScopeFilter) error {
	return consumeWatchEventsWithStateAndGoalAndSink(ctx, client, baseURL, projectID, goalID, out, keepaliveTimeout, delivered, lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered, scopeFilter, nil)
}

func consumeWatchEventsWithStateAndGoalAndSink(ctx context.Context, client *http.Client, baseURL, projectID, goalID string, out io.Writer, keepaliveTimeout time.Duration, delivered map[watchDeliveryKey]struct{}, lastWakeupContent *string, wakeupDiscrepancyDelivered map[watchWakeupDeliveryKey]struct{}, detectionDelivered map[watchDetectionDeliveryKey]struct{}, scopeFilter *watchScopeFilter, sink func(string) error) error {
	return consumeWatchEventsWithStateAndScopeAndSink(ctx, client, baseURL, watchScope{ProjectID: projectID, GoalID: goalID}, out, keepaliveTimeout, delivered, lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered, scopeFilter, sink)
}

func consumeWatchEventsWithStateAndScopeAndSink(ctx context.Context, client *http.Client, baseURL string, scope watchScope, out io.Writer, keepaliveTimeout time.Duration, delivered map[watchDeliveryKey]struct{}, lastWakeupContent *string, wakeupDiscrepancyDelivered map[watchWakeupDeliveryKey]struct{}, detectionDelivered map[watchDetectionDeliveryKey]struct{}, scopeFilter *watchScopeFilter, sink func(string) error) error {
	return consumeWatchEventsWithStateAndScopeAndSinkAndCursor(ctx, client, baseURL, scope, out, keepaliveTimeout, delivered, lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered, scopeFilter, sink)
}

// consumeWatchEventsWithStateAndScopeAndSinkAndCursor keeps the old call shape
// for callers outside this file. Any legacy cursor arguments are intentionally
// ignored.
func consumeWatchEventsWithStateAndScopeAndSinkAndCursor(ctx context.Context, client *http.Client, baseURL string, scope watchScope, out io.Writer, keepaliveTimeout time.Duration, delivered map[watchDeliveryKey]struct{}, lastWakeupContent *string, wakeupDiscrepancyDelivered map[watchWakeupDeliveryKey]struct{}, detectionDelivered map[watchDetectionDeliveryKey]struct{}, scopeFilter *watchScopeFilter, sink func(string) error, args ...any) error {
	return consumeWatchEventsWithStateAndScopeAndSinkAndInterval(ctx, client, baseURL, scope, out, keepaliveTimeout, watchReconcileInterval, delivered, lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered, scopeFilter, sink, args...)
}

func consumeWatchEventsWithStateAndScopeAndSinkAndInterval(ctx context.Context, client *http.Client, baseURL string, scope watchScope, out io.Writer, keepaliveTimeout, reconcileInterval time.Duration, delivered map[watchDeliveryKey]struct{}, lastWakeupContent *string, wakeupDiscrepancyDelivered map[watchWakeupDeliveryKey]struct{}, detectionDelivered map[watchDetectionDeliveryKey]struct{}, scopeFilter *watchScopeFilter, sink func(string) error, args ...any) error {
	actionSink := watchActionSinkFromArgs(args...)
	var latestReconciliation *watchReconciliation
	for _, arg := range args {
		if state, ok := arg.(*watchReconciliation); ok {
			latestReconciliation = state
		}
	}
	var reconciliationSucceeded func()
	for _, arg := range args {
		if callback, ok := arg.(func()); ok {
			reconciliationSucceeded = callback
		}
	}
	if client == nil {
		client = &http.Client{}
	}
	if scopeFilter == nil {
		scopeFilter = newWatchPassThroughFilter()
	}
	eventsURL, err := watchEventsURLWithScope(baseURL, scope)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, eventsURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("GET %s: HTTP %s", eventsURL, resp.Status)
	}

	frames, readDone := readWatchSSEFrames(ctx, resp.Body)
	if keepaliveTimeout <= 0 {
		keepaliveTimeout = watchKeepaliveTimeout
	}
	timer := time.NewTimer(keepaliveTimeout)
	defer timer.Stop()
	timerC := (<-chan time.Time)(timer.C)
	missingReported := false
	resetKeepalive := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(keepaliveTimeout)
		timerC = timer.C
		missingReported = false
	}
	if reconcileInterval <= 0 {
		reconcileInterval = watchReconcileInterval
	}
	reconcileTicker := time.NewTicker(reconcileInterval)
	defer reconcileTicker.Stop()
	livenessTicker := time.NewTicker(watchLivenessPromptInterval)
	defer livenessTicker.Stop()
	livenessState := newWatchLivenessState(time.Now())

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-reconcileTicker.C:
			if err := reconcileWatchScope(ctx, client, baseURL, scope, out, delivered, lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered, scopeFilter, sink, actionSink, latestReconciliation); err != nil {
				return err
			}
			if reconciliationSucceeded != nil {
				reconciliationSucceeded()
			}
		case now := <-livenessTicker.C:
			if latestReconciliation != nil && livenessState.PromptDue(now, scope, *latestReconciliation) {
				line := formatWatchLiveness(scope)
				if err := writeWatchLineWithActionSink(out, line, "monitor.liveness", watchDecision{GoalID: scope.GoalID, TaskID: scope.TaskID}, sink, actionSink); err != nil {
					return err
				}
			}
		case <-timerC:
			if !missingReported {
				if _, err := fmt.Fprintln(out, formatWatchKeepaliveMissing(keepaliveTimeout)); err != nil {
					return err
				}
				missingReported = true
			}
			timerC = nil
		case frame, ok := <-frames:
			if !ok {
				if err := <-readDone; err != nil {
					return err
				}
				return io.EOF
			}
			if frame.name == "keepalive" {
				resetKeepalive()
				continue
			}
			if err := reconcileWatchScope(ctx, client, baseURL, scope, out, delivered, lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered, scopeFilter, sink, actionSink, latestReconciliation); err != nil {
				return err
			}
			if reconciliationSucceeded != nil {
				reconciliationSucceeded()
			}
		}
	}
}

func readWatchSSEFrames(ctx context.Context, body io.Reader) (<-chan watchSSEFrame, <-chan error) {
	frames := make(chan watchSSEFrame, 16)
	done := make(chan error, 1)
	go func() {
		defer close(frames)
		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		var eventName string
		var eventID string
		var data strings.Builder
		dispatch := func() error {
			if eventName == "" || data.Len() == 0 {
				eventName = ""
				eventID = ""
				data.Reset()
				return nil
			}
			frame := watchSSEFrame{name: eventName, id: eventID, data: data.String()}
			select {
			case frames <- frame:
			case <-ctx.Done():
				return ctx.Err()
			}
			eventName = ""
			eventID = ""
			data.Reset()
			return nil
		}

		for scanner.Scan() {
			if ctx.Err() != nil {
				done <- ctx.Err()
				return
			}
			line := scanner.Text()
			switch {
			case line == "":
				if err := dispatch(); err != nil {
					done <- err
					return
				}
			case strings.HasPrefix(line, "event:"):
				eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "id:"):
				eventID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
			case strings.HasPrefix(line, "data:"):
				value := strings.TrimPrefix(line, "data:")
				if strings.HasPrefix(value, " ") {
					value = value[1:]
				}
				data.WriteString(value)
				data.WriteByte('\n')
			}
		}
		if err := scanner.Err(); err != nil {
			done <- err
			return
		}
		if err := dispatch(); err != nil {
			done <- err
			return
		}
		done <- nil
	}()
	return frames, done
}

func watchEventsURL(baseURL, projectID string) (string, error) {
	return watchEventsURLWithGoal(baseURL, projectID, "")
}

func watchEventsURLWithGoal(baseURL, projectID, goalID string) (string, error) {
	return watchEventsURLWithScope(baseURL, watchScope{ProjectID: projectID, GoalID: goalID})
}

func watchEventsURLWithScope(baseURL string, scope watchScope) (string, error) {
	endpoint := strings.TrimRight(baseURL, "/") + "/api/events"
	if scope.ProjectID == "" && scope.GoalID == "" && scope.TaskID == "" {
		return endpoint, nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	if scope.ProjectID != "" {
		query.Set("project_id", scope.ProjectID)
	}
	if scope.GoalID != "" {
		query.Set("goal_id", scope.GoalID)
	}
	if scope.TaskID != "" {
		query.Set("task_id", scope.TaskID)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func watchReconcileURL(baseURL string, scope watchScope) (string, error) {
	endpoint := strings.TrimRight(baseURL, "/") + "/api/events/reconcile"
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	if scope.ProjectID != "" {
		query.Set("project_id", scope.ProjectID)
	}
	if scope.GoalID != "" {
		query.Set("goal_id", scope.GoalID)
	}
	if scope.TaskID != "" {
		query.Set("task_id", scope.TaskID)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func watchKeyForScope(cwd string, scope watchScope) string {
	cleanCWD := strings.TrimSpace(cwd)
	if absolute, err := filepath.Abs(filepath.Clean(cleanCWD)); err == nil {
		cleanCWD = absolute
	}
	parts := []string{"atct-watch", cleanCWD}
	if scope.ProjectID != "" {
		parts = append(parts, "project="+scope.ProjectID)
	}
	if scope.GoalID != "" {
		parts = append(parts, "goal="+scope.GoalID)
	}
	if scope.TaskID != "" {
		parts = append(parts, "task="+scope.TaskID)
	}
	if scope.Role != "" {
		parts = append(parts, "role="+scope.Role)
	}
	return strings.Join(parts, "|")
}

type watchReconciliationHandoff struct {
	ID                        string  `json:"ID"`
	GoalID                    int64   `json:"GoalID"`
	TaskID                    int64   `json:"TaskID"`
	RequestedAt               *string `json:"RequestedAt"`
	ReceivedAt                *string `json:"ReceivedAt"`
	CompletedReportAt         *string `json:"CompletedReportAt"`
	ReviewRequestedAt         *string `json:"ReviewRequestedAt"`
	ReviewReceivedAt          *string `json:"ReviewReceivedAt"`
	ReviewRejectedAt          *string `json:"ReviewRejectedAt"`
	ReviewRejectionReceivedAt *string `json:"ReviewRejectionReceivedAt"`
}

type watchReconciliationGoal struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func (g *watchReconciliationGoal) UnmarshalJSON(data []byte) error {
	type plain watchReconciliationGoal
	var decoded plain
	if err := decodeEntityIDObject(data, map[string]*string{
		"id": &decoded.ID,
	}, &decoded); err != nil {
		return err
	}
	*g = watchReconciliationGoal(decoded)
	return nil
}

type watchReconciliation struct {
	Goals              []watchReconciliationGoal    `json:"goals"`
	Decisions          []watchDecision              `json:"decisions"`
	GoalHandoffs       []watchReconciliationHandoff `json:"goal_handoffs"`
	PlanHandoffs       []watchReconciliationHandoff `json:"plan_handoffs"`
	TaskHandoffs       []watchReconciliationHandoff `json:"task_handoffs"`
	TaskCreateHandoffs []watchTaskCreateHandoff     `json:"task_create_handoffs"`
}

type watchTaskCreateHandoff struct {
	ID          string  `json:"ID"`
	GoalID      int64   `json:"GoalID"`
	RequestedAt *string `json:"RequestedAt"`
	ReceivedAt  *string `json:"ReceivedAt"`
	CompletedAt *string `json:"CompletedAt"`
}

func watchActionSinkFromArgs(args ...any) watchAgentActionSink {
	for _, arg := range args {
		if actionSink, ok := arg.(watchAgentActionSink); ok {
			return actionSink
		}
		if actionSink, ok := arg.(func(watchAgentAction) error); ok {
			return watchAgentActionSink(actionSink)
		}
	}
	return nil
}

func reconcileWatchScope(ctx context.Context, client *http.Client, baseURL string, scope watchScope, out io.Writer, delivered map[watchDeliveryKey]struct{}, lastWakeupContent *string, wakeupDiscrepancyDelivered map[watchWakeupDeliveryKey]struct{}, detectionDelivered map[watchDetectionDeliveryKey]struct{}, scopeFilter *watchScopeFilter, sink watchRawLineSink, args ...any) error {
	actionSink := watchActionSinkFromArgs(args...)
	var latestReconciliation *watchReconciliation
	for _, arg := range args {
		if state, ok := arg.(*watchReconciliation); ok {
			latestReconciliation = state
		}
	}
	reconcileURL, err := watchReconcileURL(baseURL, scope)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reconcileURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", reconcileURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("GET %s: HTTP %s", reconcileURL, resp.Status)
	}
	var state watchReconciliation
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return fmt.Errorf("decode %s: %w", reconcileURL, err)
	}
	if scopeFilter == nil {
		scopeFilter = newWatchPassThroughFilter()
	}
	for _, decision := range state.Decisions {
		if !watchScopeMatchesDecision(scope, decision) {
			continue
		}
		if shouldProjectAppliedGoalReview(scope, state, decision) {
			decision.TargetRole = "commander"
			if err := emitWatchDecisionWithStateAndSinks(out, "goal.review.complete", decision,
				delivered, lastWakeupContent, wakeupDiscrepancyDelivered,
				detectionDelivered, sink, actionSink); err != nil {
				return err
			}
			continue
		}
		if shouldProjectAppliedGoalApproval(scope, state, decision) {
			if err := emitWatchDecisionWithStateAndSinks(out, "decision.approved", decision,
				delivered, lastWakeupContent, wakeupDiscrepancyDelivered,
				detectionDelivered, sink, actionSink); err != nil {
				return err
			}
			continue
		}
		var eventName string
		switch decision.Status {
		case "open":
			eventName = "decision.pending"
		case "answered":
			eventName = "decision.answered"
		default:
			continue
		}
		if !scopeFilter.delivers(eventName, decision) {
			continue
		}
		if err := emitWatchDecisionWithStateAndSinks(out, eventName, decision, delivered, lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered, sink, actionSink); err != nil {
			return err
		}
	}
	for _, handoff := range state.TaskHandoffs {
		if eventName, decision, ok := watchReconciliationHandoffEvent("task", handoff); ok && watchHandoffProjectionMatchesScope(decision, scope) {
			if !scopeFilter.delivers(eventName, decision) {
				continue
			}
			if err := emitWatchDecisionWithStateAndSinks(out, eventName, decision, delivered, lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered, sink, actionSink); err != nil {
				return err
			}
		}
	}
	for _, handoff := range state.GoalHandoffs {
		if eventName, decision, ok := watchReconciliationHandoffEvent("goal", handoff); ok && watchHandoffProjectionMatchesScope(decision, scope) {
			if !scopeFilter.delivers(eventName, decision) {
				continue
			}
			if err := emitWatchDecisionWithStateAndSinks(out, eventName, decision, delivered, lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered, sink, actionSink); err != nil {
				return err
			}
		}
	}
	for _, handoff := range state.PlanHandoffs {
		if eventName, decision, ok := watchReconciliationHandoffEvent("plan", handoff); ok && watchHandoffProjectionMatchesScope(decision, scope) {
			if !scopeFilter.delivers(eventName, decision) {
				continue
			}
			if err := emitWatchDecisionWithStateAndSinks(out, eventName, decision, delivered, lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered, sink, actionSink); err != nil {
				return err
			}
		}
	}
	for _, handoff := range state.TaskCreateHandoffs {
		if eventName, decision, ok := watchTaskCreateHandoffEvent(handoff); ok && watchHandoffProjectionMatchesScope(decision, scope) {
			if err := emitWatchDecisionWithStateAndSinks(out, eventName, decision, delivered, lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered, sink, actionSink); err != nil {
				return err
			}
		}
	}
	if latestReconciliation != nil {
		*latestReconciliation = state
	}
	return nil
}

func watchReconciliationHandoffEvent(kind string, handoff watchReconciliationHandoff) (string, watchDecision, bool) {
	if handoff.CompletedReportAt != nil {
		return "", watchDecision{}, false
	}
	decision := watchDecision{HandoffID: handoff.ID, GoalID: strconv.FormatInt(handoff.GoalID, 10)}
	if handoff.TaskID != 0 {
		decision.TaskID = strconv.FormatInt(handoff.TaskID, 10)
	}
	prefix := kind + ".handoff."
	switch {
	case handoff.ReviewRejectionReceivedAt != nil:
		decision.deliveryGeneration = *handoff.ReviewRejectionReceivedAt
		decision.TargetRole = handoffProjectionRole(kind, "review.reject.receive")
		return prefix + "review.reject.receive", decision, decision.TargetRole != ""
	case handoff.ReviewRejectedAt != nil:
		decision.deliveryGeneration = *handoff.ReviewRejectedAt
		decision.TargetRole = handoffProjectionRole(kind, "review.reject")
		return prefix + "review.reject", decision, decision.TargetRole != ""
	case handoff.ReviewReceivedAt != nil:
		decision.deliveryGeneration = *handoff.ReviewReceivedAt
		decision.TargetRole = handoffProjectionRole(kind, "review.receive")
		return prefix + "review.receive", decision, decision.TargetRole != ""
	case handoff.ReviewRequestedAt != nil:
		decision.deliveryGeneration = *handoff.ReviewRequestedAt
		decision.TargetRole = handoffProjectionRole(kind, "review.request")
		return prefix + "review.request", decision, decision.TargetRole != ""
	case handoff.ReceivedAt != nil:
		return "", watchDecision{}, false
	case handoff.RequestedAt != nil:
		decision.deliveryGeneration = *handoff.RequestedAt
		decision.TargetRole = handoffProjectionRole(kind, "request")
		return prefix + "request", decision, decision.TargetRole != ""
	default:
		return "", watchDecision{}, false
	}
}

func handoffProjectionRole(kind, phase string) string {
	switch kind {
	case "goal":
		switch phase {
		case "request", "review.request", "review.receive":
			return "commander"
		case "review.reject", "review.reject.receive":
			return "subcommander"
		}
	case "plan":
		switch phase {
		case "review.request", "review.receive":
			return "commander"
		case "review.reject", "review.reject.receive":
			return "subcommander"
		}
	case "task":
		switch phase {
		case "request", "review.request", "review.receive":
			return "subcommander"
		case "review.reject", "review.reject.receive":
			return "executor"
		}
	}
	return ""
}

func watchTaskCreateHandoffEvent(handoff watchTaskCreateHandoff) (string, watchDecision, bool) {
	if handoff.CompletedAt != nil {
		return "", watchDecision{}, false
	}
	decision := watchDecision{HandoffID: handoff.ID, GoalID: strconv.FormatInt(handoff.GoalID, 10), TargetRole: "subcommander"}
	if handoff.ReceivedAt != nil {
		decision.deliveryGeneration = *handoff.ReceivedAt
		return "task.create_handoff.receive", decision, true
	}
	if handoff.RequestedAt != nil {
		decision.deliveryGeneration = *handoff.RequestedAt
		return "task.create_handoff.request", decision, true
	}
	return "", watchDecision{}, false
}

func watchHandoffProjectionMatchesScope(decision watchDecision, scope watchScope) bool {
	if decision.TargetRole != "" && scope.Role != "" && decision.TargetRole != scope.Role {
		return false
	}
	if scope.TaskID != "" {
		return decision.TaskID == scope.TaskID
	}
	return scope.GoalID == "" || decision.GoalID == scope.GoalID
}

func emitWatchDecisionWithState(out io.Writer, eventName string, decision watchDecision, delivered map[watchDeliveryKey]struct{}, lastWakeupContent *string, wakeupDiscrepancyDelivered map[watchWakeupDeliveryKey]struct{}, detectionDelivered map[watchDetectionDeliveryKey]struct{}) error {
	return emitWatchDecisionWithStateAndSink(out, eventName, decision, delivered, lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered, nil)
}

func emitWatchDecisionWithStateAndSink(out io.Writer, eventName string, decision watchDecision, delivered map[watchDeliveryKey]struct{}, lastWakeupContent *string, wakeupDiscrepancyDelivered map[watchWakeupDeliveryKey]struct{}, detectionDelivered map[watchDetectionDeliveryKey]struct{}, sink func(string) error) error {
	return emitWatchDecisionWithStateAndSinks(out, eventName, decision, delivered, lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered, sink, nil)
}

func emitWatchDecisionWithStateAndSinks(out io.Writer, eventName string, decision watchDecision, delivered map[watchDeliveryKey]struct{}, lastWakeupContent *string, wakeupDiscrepancyDelivered map[watchWakeupDeliveryKey]struct{}, detectionDelivered map[watchDetectionDeliveryKey]struct{}, sink watchRawLineSink, actionSink watchAgentActionSink) error {
	line, ok := formatWatchDecision(eventName, decision)
	if !ok {
		return nil
	}
	writeLine := func() error {
		return writeWatchLineWithActionSink(out, line, eventName, decision, sink, actionSink)
	}
	if eventName == "handoff_yielded" {
		return writeLine()
	}
	if eventName == "goal.created" || strings.HasPrefix(eventName, "detection.") || eventName == "handoff_reported" {
		target := decision.GoalID
		if strings.HasPrefix(eventName, "detection.") {
			target = decision.DecisionID
			if target == "" {
				target = decision.GoalID
			}
			if target == "" {
				target = decision.HandoffID
			}
			if target == "" {
				target = decision.TaskID
			}
		} else if eventName == "handoff_reported" {
			target = decision.HandoffID
		}
		if target == "" {
			if eventName == "goal.created" {
				return fmt.Errorf("SSE event %s has no goal_id", eventName)
			}
			return fmt.Errorf("SSE event %s has neither decision_id, goal_id, handoff_id, nor task_id", eventName)
		}
		key := watchDetectionDeliveryKey{eventName: eventName, targetID: target, generation: decision.deliveryGeneration}
		if _, ok := detectionDelivered[key]; ok {
			return nil
		}
		if err := writeLine(); err != nil {
			return err
		}
		detectionDelivered[key] = struct{}{}
		return nil
	}
	if strings.Contains(eventName, ".handoff.") || strings.Contains(eventName, "_handoff.") {
		target := decision.HandoffID
		if target == "" {
			target = decision.TaskID
		}
		if target == "" {
			target = decision.GoalID
		}
		if target == "" {
			return fmt.Errorf("SSE event %s has no handoff_id, task_id, or goal_id", eventName)
		}
		key := watchDetectionDeliveryKey{eventName: eventName, targetID: target, generation: decision.deliveryGeneration}
		if _, ok := detectionDelivered[key]; ok {
			return nil
		}
		if err := writeLine(); err != nil {
			return err
		}
		detectionDelivered[key] = struct{}{}
		return nil
	}
	if eventName == "wakeup" || eventName == "wakeup.discrepancy" || eventName == "wakeup.evaluate_failed" {
		id := decision.wakeupID()
		if id == "" {
			return fmt.Errorf("SSE event %s has no wakeup_id", eventName)
		}
		if eventName == "wakeup" {
			if lastWakeupContent == nil {
				return errors.New("wakeup delivery state is nil")
			}
			// The daemon assigns a fresh ID to each periodic resend. Compare only
			// with the last rendered content so A -> B -> A delivers the final A.
			if *lastWakeupContent == line {
				return nil
			}
			if err := writeLine(); err != nil {
				return err
			}
			*lastWakeupContent = line
			return nil
		}
		key := watchWakeupDeliveryKey{eventName: eventName, wakeupID: id}
		if _, ok := wakeupDiscrepancyDelivered[key]; ok {
			return nil
		}
		if err := writeLine(); err != nil {
			return err
		}
		wakeupDiscrepancyDelivered[key] = struct{}{}
		return nil
	}
	id := decision.decisionID()
	if id == "" {
		return fmt.Errorf("SSE event %s has no decision_id", eventName)
	}
	key := watchDeliveryKey{
		eventName:      eventName,
		decisionID:     id,
		defaultApplied: decision.defaultApplied(),
	}
	if _, ok := delivered[key]; ok {
		return nil
	}
	if err := writeLine(); err != nil {
		return err
	}
	delivered[key] = struct{}{}
	return nil
}

func writeWatchDecisionLine(out io.Writer, eventName string, decision watchDecision, sink watchRawLineSink, actionSinks ...watchAgentActionSink) error {
	line, ok := formatWatchDecision(eventName, decision)
	if !ok {
		return nil
	}
	var actionSink watchAgentActionSink
	if len(actionSinks) > 0 {
		actionSink = actionSinks[0]
	}
	return writeWatchLineWithActionSink(out, line, eventName, decision, sink, actionSink)
}

func writeWatchLine(out io.Writer, line string, sink watchRawLineSink) error {
	return writeWatchLineWithActionSink(out, line, "", watchDecision{}, sink, nil)
}

func writeWatchLineWithActionSink(out io.Writer, line, eventName string, decision watchDecision, sink watchRawLineSink, actionSink watchAgentActionSink) error {
	if _, err := fmt.Fprintln(out, line); err != nil {
		return err
	}
	if sink != nil {
		if err := sink(line); err != nil {
			return &watchSinkError{err: err}
		}
	}
	if actionSink != nil {
		if action, ok := selectWatchAgentAction(line, eventName, decision); ok {
			if err := actionSink(action); err != nil {
				return &watchSinkError{err: err}
			}
		}
	}
	return nil
}

func shouldProjectAppliedGoalApproval(scope watchScope, state watchReconciliation, decision watchDecision) bool {
	if scope.ProjectID == "" || scope.GoalID != "" || scope.TaskID != "" {
		return false
	}
	if decision.Kind != "goal_approval" || decision.Status != "applied" || decision.GoalID == "" {
		return false
	}
	return watchReconciliationHasActiveGoal(state, decision.GoalID)
}

func shouldProjectAppliedGoalReview(scope watchScope, state watchReconciliation, decision watchDecision) bool {
	if scope.ProjectID == "" || scope.GoalID != "" || scope.TaskID != "" {
		return false
	}
	if decision.Kind != "goal_review" || decision.Status != "applied" || decision.AnswerLabel != "approve" || decision.GoalID == "" || decision.TaskID != "" {
		return false
	}
	return watchReconciliationHasActiveGoal(state, decision.GoalID)
}

func watchReconciliationHasActiveGoal(state watchReconciliation, goalID string) bool {
	for _, goal := range state.Goals {
		if goal.ID == goalID && goal.Status == "active" {
			return true
		}
	}
	return false
}

func formatWatchDecision(eventName string, decision watchDecision) (string, bool) {
	switch eventName {
	case "decision.answered":
		if decision.defaultApplied() {
			return fmt.Sprintf("atct decision default applied (decision_id: %s)", decision.decisionID()), true
		}
		return fmt.Sprintf("atct decision answered (decision_id: %s)", decision.decisionID()), true
	case "decision.pending":
		return fmt.Sprintf("atct decision pending (decision_id: %s)", decision.decisionID()), true
	case "decision.approved":
		return fmt.Sprintf("atct decision approved (decision_id: %s)", decision.decisionID()), true
	case "decision.rejected":
		return fmt.Sprintf("atct decision rejected (decision_id: %s)", decision.decisionID()), true
	case "goal.review.complete":
		return fmt.Sprintf("atct goal review approved (goal_id: %s, decision_id: %s): commander should call goal.review.complete", decision.GoalID, decision.decisionID()), true
	case "goal.created":
		return fmt.Sprintf("atct goal created (goal_id: %s)", decision.GoalID), true
	case "task.handoff.request":
		return fmt.Sprintf("atct task handoff requested (task_id: %s, handoff_id: %s)", decision.TaskID, decision.HandoffID), true
	case "task.handoff.receive":
		return fmt.Sprintf("atct task handoff received (task_id: %s, handoff_id: %s)", decision.TaskID, decision.HandoffID), true
	case "task.handoff.review.request":
		return fmt.Sprintf("atct task handoff review requested (task_id: %s, handoff_id: %s)", decision.TaskID, decision.HandoffID), true
	case "task.handoff.review.receive":
		return fmt.Sprintf("atct task handoff review received (task_id: %s, handoff_id: %s)", decision.TaskID, decision.HandoffID), true
	case "task.handoff.review.reject":
		return fmt.Sprintf("atct task handoff review rejected (task_id: %s, handoff_id: %s)", decision.TaskID, decision.HandoffID), true
	case "task.handoff.review.reject.receive":
		return fmt.Sprintf("atct task handoff review rejection received (task_id: %s, handoff_id: %s)", decision.TaskID, decision.HandoffID), true
	case "task.handoff.complete":
		return fmt.Sprintf("atct task handoff completed (task_id: %s, handoff_id: %s)", decision.TaskID, decision.HandoffID), true
	case "goal.handoff.request":
		return fmt.Sprintf("atct goal handoff requested (goal_id: %s, handoff_id: %s)", decision.GoalID, decision.HandoffID), true
	case "goal.handoff.receive":
		return fmt.Sprintf("atct goal handoff received (goal_id: %s, handoff_id: %s)", decision.GoalID, decision.HandoffID), true
	case "goal.handoff.review.request":
		return fmt.Sprintf("atct goal handoff review requested (goal_id: %s, handoff_id: %s)", decision.GoalID, decision.HandoffID), true
	case "goal.handoff.review.receive":
		return fmt.Sprintf("atct goal handoff review received (goal_id: %s, handoff_id: %s)", decision.GoalID, decision.HandoffID), true
	case "goal.handoff.review.reject":
		return fmt.Sprintf("atct goal handoff review rejected (goal_id: %s, handoff_id: %s)", decision.GoalID, decision.HandoffID), true
	case "goal.handoff.review.reject.receive":
		return fmt.Sprintf("atct goal handoff review rejection received (goal_id: %s, handoff_id: %s)", decision.GoalID, decision.HandoffID), true
	case "goal.handoff.complete":
		return fmt.Sprintf("atct goal handoff completed (goal_id: %s, handoff_id: %s)", decision.GoalID, decision.HandoffID), true
	case "plan.handoff.request":
		return fmt.Sprintf("atct plan handoff requested (goal_id: %s, handoff_id: %s)", decision.GoalID, decision.HandoffID), true
	case "plan.handoff.receive":
		return fmt.Sprintf("atct plan handoff received (goal_id: %s, handoff_id: %s)", decision.GoalID, decision.HandoffID), true
	case "plan.handoff.review.request":
		return fmt.Sprintf("atct plan handoff review requested (goal_id: %s, handoff_id: %s)", decision.GoalID, decision.HandoffID), true
	case "plan.handoff.review.receive":
		return fmt.Sprintf("atct plan handoff review received (goal_id: %s, handoff_id: %s)", decision.GoalID, decision.HandoffID), true
	case "plan.handoff.review.reject":
		return fmt.Sprintf("atct plan handoff review rejected (goal_id: %s, handoff_id: %s)", decision.GoalID, decision.HandoffID), true
	case "plan.handoff.review.reject.receive":
		return fmt.Sprintf("atct plan handoff review rejection received (goal_id: %s, handoff_id: %s)", decision.GoalID, decision.HandoffID), true
	case "plan.handoff.complete":
		return fmt.Sprintf("atct plan handoff completed (goal_id: %s, handoff_id: %s)", decision.GoalID, decision.HandoffID), true
	case "task.create_handoff.request":
		return fmt.Sprintf("atct task-create handoff requested (goal_id: %s, handoff_id: %s)", decision.GoalID, decision.HandoffID), true
	case "task.create_handoff.receive":
		return fmt.Sprintf("atct task-create handoff received (goal_id: %s, handoff_id: %s)", decision.GoalID, decision.HandoffID), true
	case "wakeup":
		return fmt.Sprintf("atct wakeup: actionable_goals=%d unassigned_goals=%d unstarted_tasks=%d waiting_answer_tasks=%d untouched_tasks=%d delegated_tasks=%d waiting_answers=%d unassigned=%s", decision.ActionableGoalCount, decision.UnassignedGoalCount, decision.UnstartedTaskCount, decision.WaitingAnswerTaskCount, decision.UntouchedTaskCount, decision.DelegatedTaskCount, decision.WaitingAnswerCount, formatUnassignedGoalIDs(decision.UnassignedGoalIDs)), true
	case "detection.completion_report_missing":
		return fmt.Sprintf("atct detection: goal %s has all tasks done but no completion report", decision.GoalID), true
	case "detection.commits_missing":
		return fmt.Sprintf("atct detection: goal %s has no linked commits", decision.GoalID), true
	case "detection.undeclared_goal":
		return fmt.Sprintf("atct detection: goal %s has no tasks declared", decision.GoalID), true
	case "detection.all_tasks_dropped":
		return fmt.Sprintf("atct detection: goal %s has all tasks dropped", decision.GoalID), true
	case "detection.unclaimed_doing":
		return fmt.Sprintf("atct detection: task %s is doing without a work lock", decision.TaskID), true
	case "detection.handoff_unreceived":
		return fmt.Sprintf("atct detection: handoff %s has no receipt", decision.HandoffID), true
	case "detection.handoff_unreported":
		switch decision.WorktreeActivity {
		case "changed":
			return fmt.Sprintf("atct detection: handoff %s has no completion report, but the goal's worktree changed after receipt", decision.HandoffID), true
		case "unchanged":
			return fmt.Sprintf("atct detection: handoff %s has no completion report and the goal's worktree is unchanged since receipt", decision.HandoffID), true
		}
		return fmt.Sprintf("atct detection: handoff %s has no completion report", decision.HandoffID), true
	case "handoff_reported":
		target := "goal " + decision.GoalID
		if decision.TaskID != "" {
			target = "task " + decision.TaskID
		}
		return fmt.Sprintf("atct handoff reported: %s (handoff %s): %s", target, decision.HandoffID, watchHandoffReportPreview(decision.CompleteReport)), true
	case "handoff_yielded":
		return fmt.Sprintf("atct handoff yielded: task %s", decision.TaskID), true
	case "detection.claim_undelegated":
		return fmt.Sprintf("atct detection: task %s has no handoff request", decision.TaskID), true
	case "detection.decision_answered_unapplied":
		return fmt.Sprintf("atct detection: decision %s was answered but not applied", decision.DecisionID), true
	case "detection.decision_default_unapplied":
		return fmt.Sprintf("atct detection: decision %s was default-applied but not applied", decision.DecisionID), true
	case "detection.claim_stale":
		return fmt.Sprintf("atct detection: task %s has a stale claim", decision.TaskID), true
	case "wakeup.discrepancy":
		return fmt.Sprintf("atct wakeup discrepancy: detector_unstarted_tasks=%d counted_unstarted_tasks=%d", decision.DetectorUnstartedTaskCount, decision.CountedUnstartedTaskCount), true
	case "wakeup.evaluate_failed":
		return fmt.Sprintf("atct wakeup evaluate failed: %s", decision.Reason), true
	default:
		return "", false
	}
}

func formatWatchLiveness(scope watchScope) string {
	if scope.TaskID != "" {
		return fmt.Sprintf("atct monitor liveness: recheck task %s", scope.TaskID)
	}
	return fmt.Sprintf("atct monitor liveness: recheck goal %s", scope.GoalID)
}

func formatUnassignedGoalIDs(ids []int64) string {
	const maxDisplayedIDs = 5
	displayed := ids
	remaining := 0
	if len(displayed) > maxDisplayedIDs {
		remaining = len(displayed) - maxDisplayedIDs
		displayed = displayed[:maxDisplayedIDs]
	}

	parts := make([]string, 0, len(displayed)+1)
	for _, id := range displayed {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	if remaining > 0 {
		parts = append(parts, "+"+strconv.Itoa(remaining))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func (d watchDecision) decisionID() string {
	if d.ID != "" {
		return d.ID
	}
	return d.DecisionID
}

func (d watchDecision) wakeupID() string {
	if d.WakeupID != "" {
		return d.WakeupID
	}
	return d.ID
}

func (d watchDecision) recoveryGeneration() string {
	if d.deliveryGeneration != "" {
		return d.deliveryGeneration
	}
	return d.Generation
}

func formatWatchKeepaliveMissing(timeout time.Duration) string {
	if timeout == watchKeepaliveTimeout {
		return "atct watch: daemon keepalive missing for 90s"
	}
	return fmt.Sprintf("atct watch: daemon keepalive missing for %s", timeout)
}

func (d watchDecision) defaultApplied() bool {
	return d.SettledByDefault || (d.DefaultAppliedAt != nil && strings.TrimSpace(*d.DefaultAppliedAt) != "")
}

func watchHandoffReportPreview(report string) string {
	report = strings.Join(strings.Fields(report), " ")
	const maxReportRunes = 80
	runes := []rune(report)
	if len(runes) <= maxReportRunes {
		return report
	}
	return string(runes[:maxReportRunes]) + "…"
}

func waitForWatchReconnect(ctx context.Context, out io.Writer, interval time.Duration) error {
	if _, err := fmt.Fprintf(out, "atct watch: connection unavailable; reconnecting in %s\n", interval); err != nil {
		return err
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil
	case <-timer.C:
		return nil
	}
}

func watchBaseURLs(dir string) []string {
	var urls []string
	if registry, err := daemonctl.ReadRegistry(dir); err == nil && registry.HTTPAddr != "" {
		urls = appendUniqueWatchURL(urls, registry.HTTPAddr)
	}
	host, _, err := net.SplitHostPort(defaultListenAddr)
	if err != nil {
		host = "127.0.0.1"
	}
	for port := defaultListenPort; port <= lastListenPort; port++ {
		urls = appendUniqueWatchURL(urls, net.JoinHostPort(host, strconv.Itoa(port)))
	}
	return urls
}

func appendUniqueWatchURL(urls []string, addr string) []string {
	baseURL := strings.TrimRight(addr, "/")
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		baseURL = "http://" + baseURL
	}
	for _, existing := range urls {
		if existing == baseURL {
			return urls
		}
	}
	return append(urls, baseURL)
}
