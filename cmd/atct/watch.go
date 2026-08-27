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
	"syscall"
	"time"

	"github.com/michiomochi/atct/internal/daemonctl"
)

const (
	watchReconnectInterval  = 5 * time.Second
	watchSnapshotTimeout    = 5 * time.Second
	watchKeepaliveTimeout   = 90 * time.Second
	watchEnsureMaxFailures  = 5
	watchEnsureLimitMessage = "atct watch: daemon ensure failed 5 consecutive times; continuing connection retries"
)

type watchDecision struct {
	ID                         string  `json:"id"`
	DecisionID                 string  `json:"decision_id"`
	ProjectID                  string  `json:"project_id"`
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
	eventName string
	targetID  string
}

type watchSnapshotFunc func(context.Context) (string, []watchDecision, error)
type watchEnsureFunc func() error

func runWatch(dir, goalID string) error {
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

	scope := daemonctl.WatchScope{ProjectID: projectID, GoalID: goalID}
	cleanup, err := daemonctl.RegisterWatchScoped(dir, scope)
	if err != nil {
		return fmt.Errorf("register watch: %w", err)
	}
	defer cleanup()

	if projectID != "" {
		result, err := daemonctl.ReapWatches(dir, scope, os.Getpid())
		if err != nil {
			return fmt.Errorf("reap watches: %w", err)
		}
		if result.RemovedStale > 0 {
			word := "registrations"
			if result.RemovedStale == 1 {
				word = "registration"
			}
			if _, err := fmt.Fprintf(os.Stdout, "atct watch: removed %d stale watch %s\n", result.RemovedStale, word); err != nil {
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
			if _, err := fmt.Fprintf(os.Stdout, "atct watch: stopped %d duplicate %s (%s)\n", len(result.Stopped), word, strings.Join(details, ", ")); err != nil {
				return fmt.Errorf("write watch reap report: %w", err)
			}
		}
		for _, pid := range result.Failed {
			if _, err := fmt.Fprintf(os.Stdout, "atct watch: duplicate watch pid %d did not exit within 5s\n", pid); err != nil {
				return fmt.Errorf("write watch reap report: %w", err)
			}
		}
	}

	snapshot, projectIDGetter := watchSnapshotWithProject(client, baseURLs, cwd)
	return watchLoopWithEnsureAndProjectIDAndGoal(ctx, os.Stdout, client, watchReconnectInterval, snapshot, func() error {
		return ensureWatchDaemon(dir)
	}, projectIDGetter, goalID)
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
	return watchLoopWithEnsureAndProjectIDAndGoal(ctx, out, client, retryInterval, snapshot, nil, projectID, goalID)
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
	if retryInterval <= 0 {
		retryInterval = watchReconnectInterval
	}
	delivered := make(map[watchDeliveryKey]struct{})
	// Keep only the last rendered wakeup content in this watch loop. On daemon
	// restart the daemon forgets its history, but this watch retains its last
	// content across reconnects and suppresses an unchanged first post-restart
	// wakeup; a changed line is sent. A newly started watch has no prior content
	// and sends its first current wakeup. State is per watch loop so a later
	// watch is not silenced by another watch's delivery.
	var lastWakeupContent string
	wakeupDiscrepancyDelivered := make(map[watchWakeupDeliveryKey]struct{})
	detectionDelivered := make(map[watchDetectionDeliveryKey]struct{})
	ensureFailures := 0
	ensureDisabled := false
	recoverDaemon := func() error {
		if ensure == nil || ensureDisabled || ctx.Err() != nil {
			return nil
		}
		if err := ensure(); err != nil {
			ensureFailures++
			if _, writeErr := fmt.Fprintln(out, err); writeErr != nil {
				return writeErr
			}
			if ensureFailures >= watchEnsureMaxFailures {
				ensureDisabled = true
				if _, writeErr := fmt.Fprintln(out, watchEnsureLimitMessage); writeErr != nil {
					return writeErr
				}
			}
			return nil
		}
		ensureFailures = 0
		ensureDisabled = false
		return nil
	}
	resetEnsureFailures := func() {
		ensureFailures = 0
		ensureDisabled = false
	}

	for {
		baseURL, decisions, err := snapshot(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
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
		for _, decision := range decisions {
			if filterProjectID != "" && decision.ProjectID != "" && decision.ProjectID != filterProjectID {
				continue
			}
			if err := emitWatchDecisionWithState(out, "decision.answered", decision, delivered, &lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered); err != nil {
				return err
			}
		}

		if err := consumeWatchEventsWithStateAndGoal(ctx, client, baseURL, filterProjectID, goalID, out, watchKeepaliveTimeout, delivered, &lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered); err != nil && ctx.Err() == nil {
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
			projectsFetched = true
			projects, err := fetchWatchProjects(ctx, client, baseURL)
			if err == nil {
				projectID = resolveWatchProjectID(cwd, projects)
			}
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
	data string
}

func consumeWatchEventsWithTimeout(ctx context.Context, client *http.Client, baseURL, projectID string, out io.Writer, keepaliveTimeout time.Duration, delivered map[watchDeliveryKey]struct{}, wakeupDiscrepancyDelivered map[watchWakeupDeliveryKey]struct{}, detectionDelivered map[watchDetectionDeliveryKey]struct{}) error {
	var lastWakeupContent string
	return consumeWatchEventsWithState(ctx, client, baseURL, projectID, out, keepaliveTimeout, delivered, &lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered)
}

func consumeWatchEventsWithState(ctx context.Context, client *http.Client, baseURL, projectID string, out io.Writer, keepaliveTimeout time.Duration, delivered map[watchDeliveryKey]struct{}, lastWakeupContent *string, wakeupDiscrepancyDelivered map[watchWakeupDeliveryKey]struct{}, detectionDelivered map[watchDetectionDeliveryKey]struct{}) error {
	return consumeWatchEventsWithStateAndGoal(ctx, client, baseURL, projectID, "", out, keepaliveTimeout, delivered, lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered)
}

func consumeWatchEventsWithStateAndGoal(ctx context.Context, client *http.Client, baseURL, projectID, goalID string, out io.Writer, keepaliveTimeout time.Duration, delivered map[watchDeliveryKey]struct{}, lastWakeupContent *string, wakeupDiscrepancyDelivered map[watchWakeupDeliveryKey]struct{}, detectionDelivered map[watchDetectionDeliveryKey]struct{}) error {
	eventsURL, err := watchEventsURLWithGoal(baseURL, projectID, goalID)
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

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
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
			var decision watchDecision
			if err := json.Unmarshal([]byte(frame.data), &decision); err != nil {
				return fmt.Errorf("decode SSE event %s: %w", frame.name, err)
			}
			if projectID != "" && decision.ProjectID != "" && decision.ProjectID != projectID {
				continue
			}
			if err := emitWatchDecisionWithState(out, frame.name, decision, delivered, lastWakeupContent, wakeupDiscrepancyDelivered, detectionDelivered); err != nil {
				return err
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
		var data strings.Builder
		dispatch := func() error {
			if eventName == "" || data.Len() == 0 {
				eventName = ""
				data.Reset()
				return nil
			}
			frame := watchSSEFrame{name: eventName, data: data.String()}
			select {
			case frames <- frame:
			case <-ctx.Done():
				return ctx.Err()
			}
			eventName = ""
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
	endpoint := strings.TrimRight(baseURL, "/") + "/api/events"
	if projectID == "" && goalID == "" {
		return endpoint, nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	if projectID != "" {
		query.Set("project_id", projectID)
	}
	if goalID != "" {
		query.Set("goal_id", goalID)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func emitWatchDecisionWithState(out io.Writer, eventName string, decision watchDecision, delivered map[watchDeliveryKey]struct{}, lastWakeupContent *string, wakeupDiscrepancyDelivered map[watchWakeupDeliveryKey]struct{}, detectionDelivered map[watchDetectionDeliveryKey]struct{}) error {
	line, ok := formatWatchDecision(eventName, decision)
	if !ok {
		return nil
	}
	if eventName == "handoff_yielded" {
		_, err := fmt.Fprintln(out, line)
		return err
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
		key := watchDetectionDeliveryKey{eventName: eventName, targetID: target}
		if _, ok := detectionDelivered[key]; ok {
			return nil
		}
		if _, err := fmt.Fprintln(out, line); err != nil {
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
			if _, err := fmt.Fprintln(out, line); err != nil {
				return err
			}
			*lastWakeupContent = line
			return nil
		}
		key := watchWakeupDeliveryKey{eventName: eventName, wakeupID: id}
		if _, ok := wakeupDiscrepancyDelivered[key]; ok {
			return nil
		}
		if _, err := fmt.Fprintln(out, line); err != nil {
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
	if _, err := fmt.Fprintln(out, line); err != nil {
		return err
	}
	delivered[key] = struct{}{}
	return nil
}

func formatWatchDecision(eventName string, decision watchDecision) (string, bool) {
	switch eventName {
	case "decision.answered":
		if decision.defaultApplied() {
			return fmt.Sprintf("atct decision default applied (decision_id: %s)", decision.decisionID()), true
		}
		return fmt.Sprintf("atct decision answered (decision_id: %s)", decision.decisionID()), true
	case "decision.approved":
		return fmt.Sprintf("atct decision approved (decision_id: %s)", decision.decisionID()), true
	case "decision.rejected":
		return fmt.Sprintf("atct decision rejected (decision_id: %s)", decision.decisionID()), true
	case "goal.created":
		return fmt.Sprintf("atct goal created (goal_id: %s)", decision.GoalID), true
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
