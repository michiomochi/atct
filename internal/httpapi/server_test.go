package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/httpapi"
	"github.com/michiomochi/atct/internal/store"
)

type fixture struct {
	ctx      context.Context
	store    *store.Store
	project  domain.Project
	goal     domain.Goal
	tasks    []domain.Task
	open     domain.Decision
	answered domain.Decision
}

type decisionViewResponse struct {
	domain.Decision
	SettledByDefault bool `json:"settled_by_default"`
}

type goalDetailResponse struct {
	Goal                domain.Goal        `json:"goal"`
	NeedsDecision       []httpapi.TaskView `json:"needs_decision"`
	UnattachedDecisions []domain.Decision  `json:"unattached_decisions"`
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := newBareFixture(t)

	var err error
	f.tasks, err = f.store.DeclareTasks(f.ctx, f.goal.ID, "fixture-agent", "fixture-declare", []string{"needs", "now", "next"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ClaimTask(f.ctx, f.tasks[0].ID, "fixture-run"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.UpdateTask(f.ctx, f.tasks[1].ID, domain.TaskDoing); err != nil {
		t.Fatal(err)
	}
	f.open, err = f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:   f.goal.ID,
		TaskID:   f.tasks[0].ID,
		Kind:     domain.DecisionKind("question"),
		Question: "Which option?",
		RunID:    "fixture-run",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.answered, err = f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:   f.goal.ID,
		TaskID:   f.tasks[2].ID,
		Kind:     domain.DecisionKind("question"),
		Question: "Already answered?",
		RunID:    "fixture-run",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.answered, err = f.store.AnswerDecision(f.ctx, store.AnswerInput{
		DecisionID: f.answered.ID,
		AnswerText: "later",
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func newBareFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "atct.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ns, err := db.CreateProject(ctx, "fixture", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	goal, err := db.CreateGoal(ctx, ns.ID, "Fixture goal", "For HTTP API tests")
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{ctx: ctx, store: db, project: ns, goal: goal}
}

func TestHTTPInboxMarksDefaultSettledDecision(t *testing.T) {
	f := newBareFixture(t)
	afterMs := int64(1)
	decision, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:         f.goal.ID,
		Kind:           domain.DecisionKind("question"),
		Question:       "Which option should be used?",
		Options:        []domain.Option{{Label: "A"}, {Label: "B"}},
		DefaultOption:  "A",
		DefaultAfterMs: &afterMs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ApplyExpiredDefaults(f.ctx, decision.CreatedAt.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, srv.URL+"/api/inbox", nil)
	if status != http.StatusOK {
		t.Fatalf("inbox status = %d; body=%s", status, body)
	}
	var response struct {
		UnappliedDecisions []decisionViewResponse `json:"unapplied_decisions"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.UnappliedDecisions) != 1 || response.UnappliedDecisions[0].ID != decision.ID {
		t.Fatalf("unapplied decisions = %+v", response.UnappliedDecisions)
	}
	if !response.UnappliedDecisions[0].SettledByDefault {
		t.Fatalf("settled_by_default = false; response=%s", body)
	}
}

func newTestServer(t *testing.T, db *store.Store) *httptest.Server {
	t.Helper()
	return httptest.NewServer(httpapi.New(db).Handler())
}

func doRequest(t *testing.T, client *http.Client, method, url string, body []byte) (int, http.Header, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, data
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertErrorObject(t *testing.T, status int, headers http.Header, body []byte, wantStatus int) {
	t.Helper()
	if status != wantStatus {
		t.Fatalf("status = %d, want %d; body=%s", status, wantStatus, body)
	}
	if !strings.HasPrefix(headers.Get("Content-Type"), "application/json") {
		t.Fatalf("content type = %q, want application/json", headers.Get("Content-Type"))
	}
	var response map[string]string
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("error response is not JSON: %v; body=%s", err, body)
	}
	if response["error"] == "" {
		t.Fatalf("error response lacks error: %s", body)
	}
}

func TestInboxAndGoalDetailUseExclusiveTaskColumns(t *testing.T) {
	f := newFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()

	status, headers, body := doRequest(t, srv.Client(), http.MethodGet, srv.URL+"/api/inbox", nil)
	if status != http.StatusOK {
		t.Fatalf("inbox status = %d; body=%s", status, body)
	}
	if !strings.HasPrefix(headers.Get("Content-Type"), "application/json") {
		t.Fatalf("inbox content type = %q", headers.Get("Content-Type"))
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"open_decisions", "unapplied_decisions", "active_goals", "attention_tasks"} {
		if value, ok := raw[key]; !ok || string(value) == "null" {
			t.Fatalf("inbox %s is missing or null: %s", key, body)
		}
	}
	var inbox struct {
		OpenDecisions      []domain.Decision  `json:"open_decisions"`
		UnappliedDecisions []domain.Decision  `json:"unapplied_decisions"`
		ActiveGoals        []domain.Goal      `json:"active_goals"`
		AttentionTasks     []httpapi.TaskView `json:"attention_tasks"`
	}
	if err := json.Unmarshal(body, &inbox); err != nil {
		t.Fatal(err)
	}
	if len(inbox.OpenDecisions) != 1 || inbox.OpenDecisions[0].ID != f.open.ID {
		t.Fatalf("open decisions = %+v", inbox.OpenDecisions)
	}
	if len(inbox.UnappliedDecisions) != 1 || inbox.UnappliedDecisions[0].ID != f.answered.ID {
		t.Fatalf("unapplied decisions = %+v", inbox.UnappliedDecisions)
	}
	if len(inbox.ActiveGoals) != 1 || inbox.ActiveGoals[0].ID != f.goal.ID {
		t.Fatalf("active goals = %+v", inbox.ActiveGoals)
	}
	if len(inbox.AttentionTasks) != 1 || inbox.AttentionTasks[0].ID != f.tasks[0].ID {
		t.Fatalf("attention tasks = %+v", inbox.AttentionTasks)
	}
	if len(inbox.AttentionTasks[0].OpenDecisions) != 1 || inbox.AttentionTasks[0].OpenDecisions[0].ID != f.open.ID {
		t.Fatalf("attention task decisions = %+v", inbox.AttentionTasks[0].OpenDecisions)
	}
	if inbox.AttentionTasks[0].HeldForSeconds < 0 {
		t.Fatalf("held_for_seconds = %d", inbox.AttentionTasks[0].HeldForSeconds)
	}

	status, _, body = doRequest(t, srv.Client(), http.MethodGet, srv.URL+"/api/goals/"+f.goal.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("goal status = %d; body=%s", status, body)
	}
	var detail struct {
		Goal          domain.Goal        `json:"goal"`
		Now           []httpapi.TaskView `json:"now"`
		NeedsDecision []httpapi.TaskView `json:"needs_decision"`
		Next          []httpapi.TaskView `json:"next"`
	}
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Goal.ID != f.goal.ID {
		t.Fatalf("detail goal = %+v", detail.Goal)
	}
	if len(detail.NeedsDecision) != 1 || detail.NeedsDecision[0].ID != f.tasks[0].ID {
		t.Fatalf("needs_decision = %+v", detail.NeedsDecision)
	}
	if len(detail.Now) != 1 || detail.Now[0].ID != f.tasks[1].ID {
		t.Fatalf("now = %+v", detail.Now)
	}
	if len(detail.Next) != 1 || detail.Next[0].ID != f.tasks[2].ID {
		t.Fatalf("next = %+v", detail.Next)
	}
	if detail.NeedsDecision[0].HeldForSeconds < 0 {
		t.Fatalf("goal held_for_seconds = %d", detail.NeedsDecision[0].HeldForSeconds)
	}
}

func TestHTTPGoalDetailIncludesTasklessOpenDecision(t *testing.T) {
	f := newBareFixture(t)
	decision, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:   f.goal.ID,
		Kind:     domain.DecisionKind("question"),
		Question: "Which direction should we take?",
		RunID:    "taskless-run",
	})
	if err != nil {
		t.Fatal(err)
	}

	detail := fetchGoalDetail(t, f)
	if len(detail.UnattachedDecisions) != 1 || detail.UnattachedDecisions[0].ID != decision.ID {
		t.Fatalf("unattached_decisions = %+v", detail.UnattachedDecisions)
	}
}

func TestHTTPGoalDetailDoesNotDuplicateTaskDecision(t *testing.T) {
	f := newFixture(t)
	taskless, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:   f.goal.ID,
		Kind:     domain.DecisionKind("question"),
		Question: "A decision without a task",
		RunID:    "taskless-run",
	})
	if err != nil {
		t.Fatal(err)
	}

	detail := fetchGoalDetail(t, f)
	if len(detail.NeedsDecision) != 1 || len(detail.NeedsDecision[0].OpenDecisions) != 1 || detail.NeedsDecision[0].OpenDecisions[0].ID != f.open.ID {
		t.Fatalf("needs_decision = %+v", detail.NeedsDecision)
	}
	if len(detail.UnattachedDecisions) != 1 || detail.UnattachedDecisions[0].ID != taskless.ID {
		t.Fatalf("unattached_decisions = %+v", detail.UnattachedDecisions)
	}
	if detail.UnattachedDecisions[0].ID == f.open.ID {
		t.Fatalf("task-bound decision was duplicated: %+v", detail.UnattachedDecisions)
	}
}

func TestHTTPGoalDetailIncludesCompletionDecision(t *testing.T) {
	f := newBareFixture(t)
	decision, err := f.store.CompleteGoal(f.ctx, f.goal.ID, "The work is ready", "completion-run")
	if err != nil {
		t.Fatal(err)
	}

	detail := fetchGoalDetail(t, f)
	if len(detail.UnattachedDecisions) != 1 {
		t.Fatalf("unattached_decisions = %+v", detail.UnattachedDecisions)
	}
	got := detail.UnattachedDecisions[0]
	if got.ID != decision.ID || got.Kind != domain.DecisionKind("completion") || got.TaskID != "" {
		t.Fatalf("completion decision = %+v", got)
	}
}

func TestHTTPGoalDetailHasNoUnattachedDecisionsWhenEmpty(t *testing.T) {
	f := newBareFixture(t)
	detail := fetchGoalDetail(t, f)
	if detail.UnattachedDecisions == nil {
		t.Fatalf("unattached_decisions is null")
	}
	if len(detail.UnattachedDecisions) != 0 {
		t.Fatalf("unattached_decisions = %+v", detail.UnattachedDecisions)
	}
}

func fetchGoalDetail(t *testing.T, f *fixture) goalDetailResponse {
	t.Helper()
	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, srv.URL+"/api/goals/"+f.goal.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("goal status = %d; body=%s", status, body)
	}
	var detail goalDetailResponse
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("decode goal detail: %v; body=%s", err, body)
	}
	return detail
}

func TestHTTPDecisionAndReleaseEndpointsValidateAndTransition(t *testing.T) {
	f := newFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	client := srv.Client()

	status, headers, body := doRequest(t, client, http.MethodPost, srv.URL+"/api/decisions/"+f.open.ID+"/answer", mustJSON(t, map[string]string{}))
	assertErrorObject(t, status, headers, body, http.StatusBadRequest)
	var invalidAnswer map[string]string
	if err := json.Unmarshal(body, &invalidAnswer); err != nil {
		t.Fatalf("decode invalid answer response: %v", err)
	}
	if invalidAnswer["error"] != "an answer label or text is required" {
		t.Fatalf("unexpected invalid answer error = %q", invalidAnswer["error"])
	}

	status, headers, body = doRequest(t, client, http.MethodPost, srv.URL+"/api/decisions/missing/answer", mustJSON(t, map[string]string{"answer_text": "yes"}))
	assertErrorObject(t, status, headers, body, http.StatusNotFound)

	status, headers, body = doRequest(t, client, http.MethodPost, srv.URL+"/api/decisions/"+f.open.ID+"/answer", mustJSON(t, map[string]string{"answer_text": "yes"}))
	if status != http.StatusOK {
		t.Fatalf("answer status = %d; body=%s", status, body)
	}
	var answered domain.Decision
	if err := json.Unmarshal(body, &answered); err != nil {
		t.Fatal(err)
	}
	if answered.Status != domain.DecisionStatus("answered") || answered.AnswerText != "yes" {
		t.Fatalf("answered decision = %+v", answered)
	}
	status, headers, body = doRequest(t, client, http.MethodPost, srv.URL+"/api/decisions/"+f.open.ID+"/answer", mustJSON(t, map[string]string{"answer_text": "again"}))
	assertErrorObject(t, status, headers, body, http.StatusConflict)

	status, _, body = doRequest(t, client, http.MethodPost, srv.URL+"/api/tasks/"+f.tasks[0].ID+"/release", nil)
	if status != http.StatusOK {
		t.Fatalf("release status = %d; body=%s", status, body)
	}
	var released domain.Task
	if err := json.Unmarshal(body, &released); err != nil {
		t.Fatal(err)
	}
	if released.ClaimedBy != "" || released.ClaimedAt != nil {
		t.Fatalf("released task = %+v", released)
	}
	status, headers, body = doRequest(t, client, http.MethodPost, srv.URL+"/api/tasks/missing/release", nil)
	assertErrorObject(t, status, headers, body, http.StatusNotFound)

	status, headers, body = doRequest(t, client, http.MethodGet, srv.URL+"/api/goals/"+f.goal.ID+"/extra", nil)
	assertErrorObject(t, status, headers, body, http.StatusBadRequest)
}

func TestHTTPApproveAndRejectCompletionEndpoints(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	client := srv.Client()

	approveGoal, err := f.store.CreateGoal(f.ctx, f.project.ID, "Approve me", "")
	if err != nil {
		t.Fatal(err)
	}
	approveDecision, err := f.store.CompleteGoal(f.ctx, approveGoal.ID, "finished", "approve-run")
	if err != nil {
		t.Fatal(err)
	}
	status, headers, body := doRequest(t, client, http.MethodPost, srv.URL+"/api/decisions/"+approveDecision.ID+"/approve", mustJSON(t, map[string]string{}))
	if status != http.StatusOK {
		t.Fatalf("approve status = %d; body=%s", status, body)
	}
	var approvedGoal domain.Goal
	if err := json.Unmarshal(body, &approvedGoal); err != nil {
		t.Fatal(err)
	}
	if approvedGoal.ID != approveGoal.ID || approvedGoal.Status != domain.GoalStatus("done") {
		t.Fatalf("approved goal = %+v", approvedGoal)
	}

	rejectGoal, err := f.store.CreateGoal(f.ctx, f.project.ID, "Reject me", "")
	if err != nil {
		t.Fatal(err)
	}
	rejectDecision, err := f.store.CompleteGoal(f.ctx, rejectGoal.ID, "try again", "reject-run")
	if err != nil {
		t.Fatal(err)
	}
	status, _, body = doRequest(t, client, http.MethodPost, srv.URL+"/api/decisions/"+rejectDecision.ID+"/reject", mustJSON(t, map[string]string{"reason": "needs work"}))
	if status != http.StatusOK {
		t.Fatalf("reject status = %d; body=%s", status, body)
	}
	var rejected domain.Decision
	if err := json.Unmarshal(body, &rejected); err != nil {
		t.Fatal(err)
	}
	if rejected.ID != rejectDecision.ID || rejected.Status != domain.DecisionStatus("answered") {
		t.Fatalf("rejected decision = %+v", rejected)
	}
	status, headers, body = doRequest(t, client, http.MethodPost, srv.URL+"/api/decisions/"+rejectDecision.ID+"/reject", mustJSON(t, map[string]string{}))
	assertErrorObject(t, status, headers, body, http.StatusConflict)
}

type sseFrame struct {
	event string
	data  string
	lines []string
}

func readSSEFrame(t *testing.T, reader *bufio.Reader) sseFrame {
	t.Helper()
	result := make(chan sseFrame, 1)
	errors := make(chan error, 1)
	go func() {
		var frame sseFrame
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				errors <- err
				return
			}
			line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			if line == "" {
				if frame.event != "" || frame.data != "" {
					result <- frame
					return
				}
				continue
			}
			frame.lines = append(frame.lines, line)
			switch {
			case strings.HasPrefix(line, "event: "):
				frame.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				frame.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	select {
	case frame := <-result:
		return frame
	case err := <-errors:
		t.Fatalf("read SSE: %v", err)
		return sseFrame{}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SSE event")
		return sseFrame{}
	}
}

func assertSSEDecision(t *testing.T, reader *bufio.Reader, wantEvent string, want domain.Decision) {
	t.Helper()
	frame := readSSEFrame(t, reader)
	if frame.event != wantEvent {
		t.Fatalf("SSE event = %q, want %q; lines=%v", frame.event, wantEvent, frame.lines)
	}
	for _, line := range frame.lines {
		if strings.HasPrefix(line, "id:") {
			t.Fatalf("SSE frame unexpectedly has id: %v", frame.lines)
		}
	}
	var got domain.Decision
	if err := json.Unmarshal([]byte(frame.data), &got); err != nil {
		t.Fatalf("SSE data is not a Decision: %v; data=%q", err, frame.data)
	}
	wantJSON := string(mustJSON(t, want))
	if frame.data != wantJSON {
		t.Fatalf("SSE data = %s, want exact %s", frame.data, wantJSON)
	}
}

func TestSSEPublishesAllDecisionTransitionsWithExactPayloads(t *testing.T) {
	f := newBareFixture(t)
	goals := make([]domain.Goal, 0, 3)
	for _, title := range []string{"Approve later", "Reject later"} {
		goal, err := f.store.CreateGoal(f.ctx, f.project.ID, title, "")
		if err != nil {
			t.Fatal(err)
		}
		goals = append(goals, goal)
	}
	srv := newTestServer(t, f.store)
	defer srv.Close()

	response := make(chan struct {
		resp *http.Response
		err  error
	}, 1)
	client := srv.Client()
	go func() {
		resp, err := client.Get(srv.URL + "/api/events")
		response <- struct {
			resp *http.Response
			err  error
		}{resp: resp, err: err}
	}()
	var stream *http.Response
	select {
	case result := <-response:
		if result.err != nil {
			t.Fatal(result.err)
		}
		stream = result.resp
	case <-time.After(2 * time.Second):
		t.Fatal("timed out opening SSE stream")
	}
	defer stream.Body.Close()
	if stream.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(stream.Body)
		t.Fatalf("SSE status = %d; body=%s", stream.StatusCode, body)
	}
	reader := bufio.NewReader(stream.Body)

	created, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:   f.goal.ID,
		Kind:     domain.DecisionKind("question"),
		Question: "Need an answer",
		RunID:    "poll-run",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSSEDecision(t, reader, "decision.created", created)

	answered, err := f.store.AnswerDecision(f.ctx, store.AnswerInput{DecisionID: created.ID, AnswerLabel: "yes"})
	if err != nil {
		t.Fatal(err)
	}
	assertSSEDecision(t, reader, "decision.answered", answered)

	applied, err := f.store.PollDecisions(f.ctx, "poll-run", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 {
		t.Fatalf("applied decisions = %+v", applied)
	}
	assertSSEDecision(t, reader, "decision.applied", applied[0])

	withdrawn, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:   f.goal.ID,
		Kind:     domain.DecisionKind("question"),
		Question: "Withdraw me",
		RunID:    "withdraw-run",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSSEDecision(t, reader, "decision.created", withdrawn)
	if err := f.store.WithdrawDecision(f.ctx, withdrawn.ID, "no longer needed"); err != nil {
		t.Fatal(err)
	}
	withdrawn, err = f.store.GetDecision(f.ctx, withdrawn.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertSSEDecision(t, reader, "decision.withdrawn", withdrawn)

	approveDecision, err := f.store.CompleteGoal(f.ctx, goals[0].ID, "done", "approve-run")
	if err != nil {
		t.Fatal(err)
	}
	assertSSEDecision(t, reader, "decision.created", approveDecision)
	if _, err := f.store.ApproveCompletion(f.ctx, approveDecision.ID); err != nil {
		t.Fatal(err)
	}
	approveDecision, err = f.store.GetDecision(f.ctx, approveDecision.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertSSEDecision(t, reader, "decision.approved", approveDecision)

	rejectDecision, err := f.store.CompleteGoal(f.ctx, goals[1].ID, "not yet", "reject-run")
	if err != nil {
		t.Fatal(err)
	}
	assertSSEDecision(t, reader, "decision.created", rejectDecision)
	if err := f.store.RejectCompletion(f.ctx, rejectDecision.ID, "needs work"); err != nil {
		t.Fatal(err)
	}
	rejectDecision, err = f.store.GetDecision(f.ctx, rejectDecision.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertSSEDecision(t, reader, "decision.rejected", rejectDecision)
}

func TestHTTPUnknownGoalAndDecisionReturnJSONNotFound(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	client := srv.Client()

	status, headers, body := doRequest(t, client, http.MethodGet, srv.URL+"/api/goals/missing", nil)
	assertErrorObject(t, status, headers, body, http.StatusNotFound)
	status, headers, body = doRequest(t, client, http.MethodPost, srv.URL+"/api/decisions/missing/approve", mustJSON(t, map[string]string{}))
	assertErrorObject(t, status, headers, body, http.StatusNotFound)
}

func TestTaskViewJSONIncludesDomainFieldsAndDerivedFields(t *testing.T) {
	f := newFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, srv.URL+"/api/goals/"+f.goal.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d; body=%s", status, body)
	}
	var detail map[string]json.RawMessage
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatal(err)
	}
	var needs []httpapi.TaskView
	if err := json.Unmarshal(detail["needs_decision"], &needs); err != nil {
		t.Fatal(err)
	}
	if len(needs) != 1 {
		t.Fatalf("needs = %+v", needs)
	}
	data := mustJSON(t, needs[0])
	for _, field := range []string{"id", "goal_id", "title", "status", "held_for_seconds", "open_decisions"} {
		if !bytes.Contains(data, []byte(fmt.Sprintf("\"%s\"", field))) {
			t.Fatalf("TaskView lacks %s: %s", field, data)
		}
	}
}

func TestHTTPProjectsAndGoalCreationEndpoints(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	client := srv.Client()

	status, headers, body := doRequest(t, client, http.MethodGet, srv.URL+"/api/projects", nil)
	if status != http.StatusOK {
		t.Fatalf("projects status = %d; body=%s", status, body)
	}
	if contentType := headers.Get("Content-Type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("projects content type = %q", contentType)
	}
	var projects []domain.Project
	if err := json.Unmarshal(body, &projects); err != nil {
		t.Fatalf("decode projects: %v; body=%s", err, body)
	}
	if len(projects) != 1 || projects[0].ID != f.project.ID {
		t.Fatalf("projects = %+v", projects)
	}

	status, _, body = doRequest(t, client, http.MethodPost, srv.URL+"/api/goals", mustJSON(t, map[string]string{
		"project_id":  f.project.ID,
		"title":       "Created in inbox",
		"description": "Created through the human UI endpoint",
	}))
	if status != http.StatusOK {
		t.Fatalf("create goal status = %d; body=%s", status, body)
	}
	var created domain.Goal
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created goal: %v; body=%s", err, body)
	}
	if created.ProjectID != f.project.ID || created.Title != "Created in inbox" || created.Description != "Created through the human UI endpoint" {
		t.Fatalf("created goal = %+v", created)
	}

	status, headers, body = doRequest(t, client, http.MethodPost, srv.URL+"/api/goals", mustJSON(t, map[string]string{
		"project_id": f.project.ID,
	}))
	assertErrorObject(t, status, headers, body, http.StatusBadRequest)

	status, headers, body = doRequest(t, client, http.MethodPost, srv.URL+"/api/goals", mustJSON(t, map[string]string{
		"project_id": "missing-project",
		"title":      "Unknown project",
	}))
	assertErrorObject(t, status, headers, body, http.StatusNotFound)
}
