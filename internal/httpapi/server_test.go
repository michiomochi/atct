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
	"net/url"
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

func TestInboxAttentionTasksIncludeProjectIdentityPerTask(t *testing.T) {
	f := newFixture(t)
	otherProject, err := f.store.CreateProject(f.ctx, "other", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	otherGoal, err := f.store.CreateGoal(f.ctx, otherProject.ID, "Other project goal", "")
	if err != nil {
		t.Fatal(err)
	}
	otherTasks, err := f.store.DeclareTasks(f.ctx, otherGoal.ID, "other-agent", "other-declare", []string{"needs"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ClaimTask(f.ctx, otherTasks[0].ID, "other-run"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:   otherGoal.ID,
		TaskID:   otherTasks[0].ID,
		Kind:     f.open.Kind,
		Question: "Other project question",
		Options:  f.open.Options,
	}); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, srv.URL+"/api/inbox", nil)
	if status != http.StatusOK {
		t.Fatalf("inbox status = %d; body=%s", status, body)
	}
	var inbox struct {
		AttentionTasks []struct {
			ID          string `json:"id"`
			ProjectID   string `json:"project_id"`
			ProjectName string `json:"project_name"`
		} `json:"attention_tasks"`
	}
	if err := json.Unmarshal(body, &inbox); err != nil {
		t.Fatal(err)
	}
	if len(inbox.AttentionTasks) != 2 {
		t.Fatalf("attention tasks = %+v", inbox.AttentionTasks)
	}
	got := make(map[string]struct {
		projectID   string
		projectName string
	})
	for _, task := range inbox.AttentionTasks {
		got[task.ID] = struct {
			projectID   string
			projectName string
		}{projectID: task.ProjectID, projectName: task.ProjectName}
	}
	want := map[string]struct {
		projectID   string
		projectName string
	}{
		f.tasks[0].ID:    {projectID: f.project.ID, projectName: "fixture"},
		otherTasks[0].ID: {projectID: otherProject.ID, projectName: "other"},
	}
	if len(got) != len(want) {
		t.Fatalf("attention task identities = %+v", got)
	}
	for taskID, wantIdentity := range want {
		if gotIdentity, ok := got[taskID]; !ok || gotIdentity != wantIdentity {
			t.Fatalf("attention task %s identity = %+v, want %+v", taskID, gotIdentity, wantIdentity)
		}
	}
}

type decisionViewResponse struct {
	domain.Decision
	ProjectID        string `json:"project_id"`
	GoalTitle        string `json:"goal_title"`
	SettledByDefault bool   `json:"settled_by_default"`
	DefaultOption    string `json:"default_option"`
	DefaultAfterMs   *int64 `json:"default_after_ms"`
}

func TestHTTPInboxIncludesGoalTitlePerDecision(t *testing.T) {
	f := newBareFixture(t)
	first, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:   f.goal.ID,
		Kind:     domain.DecisionKind("question"),
		Question: "First goal question",
	})
	if err != nil {
		t.Fatal(err)
	}
	otherProject, err := f.store.CreateProject(f.ctx, "other", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	otherGoal, err := f.store.CreateGoal(f.ctx, otherProject.ID, "Other goal", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:   otherGoal.ID,
		Kind:     domain.DecisionKind("question"),
		Question: "Second goal question",
	})
	if err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, srv.URL+"/api/inbox", nil)
	if status != http.StatusOK {
		t.Fatalf("inbox status = %d; body=%s", status, body)
	}
	var response struct {
		OpenDecisions []decisionViewResponse `json:"open_decisions"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	got := make(map[string]string, len(response.OpenDecisions))
	for _, decision := range response.OpenDecisions {
		got[decision.ID] = decision.GoalTitle
	}
	want := map[string]string{
		first.ID:  f.goal.Title,
		second.ID: otherGoal.Title,
	}
	if len(got) != len(want) {
		t.Fatalf("open decision goal titles = %+v, want %+v", got, want)
	}
	for decisionID, wantTitle := range want {
		if gotTitle := got[decisionID]; gotTitle != wantTitle {
			t.Fatalf("decision %s goal title = %q, want %q", decisionID, gotTitle, wantTitle)
		}
	}
}

func TestHTTPInboxIncludesTasksPerActiveGoalInOrder(t *testing.T) {
	f := newBareFixture(t)
	firstTasks, err := f.store.DeclareTasks(f.ctx, f.goal.ID, "first-agent", "first-declare", []string{"first task", "second task"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.UpdateTask(f.ctx, firstTasks[1].ID, domain.TaskDoing); err != nil {
		t.Fatal(err)
	}
	secondGoal, err := f.store.CreateGoal(f.ctx, f.project.ID, "Second goal", "")
	if err != nil {
		t.Fatal(err)
	}
	secondTasks, err := f.store.DeclareTasks(f.ctx, secondGoal.ID, "second-agent", "second-declare", []string{"other task"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:   f.goal.ID,
		Kind:     domain.DecisionKind("question"),
		Question: "Which first-goal path should we take?",
	}); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, srv.URL+"/api/inbox", nil)
	if status != http.StatusOK {
		t.Fatalf("inbox status = %d; body=%s", status, body)
	}
	var response struct {
		ActiveGoals []struct {
			ID               string `json:"id"`
			AwaitingDecision bool   `json:"awaiting_decision"`
			Tasks            []struct {
				ID     string `json:"id"`
				GoalID string `json:"goal_id"`
				Order  int    `json:"order"`
			} `json:"tasks"`
		} `json:"active_goals"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}

	goals := make(map[string][]struct {
		ID     string `json:"id"`
		GoalID string `json:"goal_id"`
		Order  int    `json:"order"`
	}, len(response.ActiveGoals))
	awaiting := make(map[string]bool, len(response.ActiveGoals))
	for _, goal := range response.ActiveGoals {
		tasks := make([]struct {
			ID     string `json:"id"`
			GoalID string `json:"goal_id"`
			Order  int    `json:"order"`
		}, len(goal.Tasks))
		for i, task := range goal.Tasks {
			tasks[i] = task
		}
		goals[goal.ID] = tasks
		awaiting[goal.ID] = goal.AwaitingDecision
	}
	if got, ok := awaiting[f.goal.ID]; !ok || !got {
		t.Fatalf("first goal awaiting_decision = %v (present=%v), want true", got, ok)
	}
	if got, ok := awaiting[secondGoal.ID]; !ok || got {
		t.Fatalf("second goal awaiting_decision = %v (present=%v), want false", got, ok)
	}

	firstGot, ok := goals[f.goal.ID]
	if !ok {
		t.Fatalf("active goal %s missing from response: %+v", f.goal.ID, response.ActiveGoals)
	}
	if len(firstGot) != len(firstTasks) {
		t.Fatalf("first goal tasks = %+v, want %d tasks", firstGot, len(firstTasks))
	}
	for i, wantTask := range firstTasks {
		gotTask := firstGot[i]
		if gotTask.ID != wantTask.ID || gotTask.GoalID != f.goal.ID || gotTask.Order != i {
			t.Fatalf("first goal task %d = %+v, want id=%s goal_id=%s order=%d", i, gotTask, wantTask.ID, f.goal.ID, i)
		}
	}

	secondGot, ok := goals[secondGoal.ID]
	if !ok {
		t.Fatalf("active goal %s missing from response: %+v", secondGoal.ID, response.ActiveGoals)
	}
	if len(secondGot) != len(secondTasks) || secondGot[0].ID != secondTasks[0].ID || secondGot[0].GoalID != secondGoal.ID {
		t.Fatalf("second goal tasks = %+v, want task %s owned by %s", secondGot, secondTasks[0].ID, secondGoal.ID)
	}
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
	if response.UnappliedDecisions[0].ProjectID != f.goal.ProjectID {
		t.Fatalf("project_id = %q, want %q", response.UnappliedDecisions[0].ProjectID, f.goal.ProjectID)
	}
	if !response.UnappliedDecisions[0].SettledByDefault {
		t.Fatalf("settled_by_default = false; response=%s", body)
	}
}

func TestHTTPGoalDetailDecisionHistoryRecordsSettlementSource(t *testing.T) {
	f := newBareFixture(t)
	afterMs := int64(1)
	defaultDecision, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:         f.goal.ID,
		Kind:           domain.DecisionKind("question"),
		Question:       "Which default should be applied?",
		Options:        []domain.Option{{Label: "A"}, {Label: "B"}},
		DefaultOption:  "A",
		DefaultAfterMs: &afterMs,
		RunID:          "default-run",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ApplyExpiredDefaults(f.ctx, defaultDecision.CreatedAt.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if applied, err := f.store.PollDecisions(f.ctx, "default-run", defaultDecision.ID); err != nil {
		t.Fatal(err)
	} else if len(applied) != 1 {
		t.Fatalf("default applied decisions = %+v", applied)
	}

	humanDecision, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:   f.goal.ID,
		Kind:     domain.DecisionKind("question"),
		Question: "Which option should a person choose?",
		Options:  []domain.Option{{Label: "A"}, {Label: "B"}},
		RunID:    "human-run",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.AnswerDecision(f.ctx, store.AnswerInput{
		DecisionID:  humanDecision.ID,
		AnswerLabel: "B",
	}); err != nil {
		t.Fatal(err)
	}
	if applied, err := f.store.PollDecisions(f.ctx, "human-run", humanDecision.ID); err != nil {
		t.Fatal(err)
	} else if len(applied) != 1 {
		t.Fatalf("human applied decisions = %+v", applied)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, srv.URL+"/api/goals/"+f.goal.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("goal status = %d; body=%s", status, body)
	}
	var response struct {
		DecisionHistory []struct {
			DecisionID       string     `json:"decision_id"`
			SettledByDefault bool       `json:"settled_by_default"`
			DefaultAppliedAt *time.Time `json:"default_applied_at"`
		} `json:"decision_history"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.DecisionHistory) != 2 {
		t.Fatalf("decision_history = %+v; body=%s", response.DecisionHistory, body)
	}
	var defaultHistory, humanHistory struct {
		SettledByDefault bool
		DefaultAppliedAt *time.Time
	}
	var defaultFound, humanFound bool
	for _, entry := range response.DecisionHistory {
		switch entry.DecisionID {
		case defaultDecision.ID:
			defaultFound = true
			defaultHistory.SettledByDefault = entry.SettledByDefault
			defaultHistory.DefaultAppliedAt = entry.DefaultAppliedAt
		case humanDecision.ID:
			humanFound = true
			humanHistory.SettledByDefault = entry.SettledByDefault
			humanHistory.DefaultAppliedAt = entry.DefaultAppliedAt
		}
	}
	if !defaultFound {
		t.Fatal("default decision history entry is missing")
	}
	if !humanFound {
		t.Fatal("human decision history entry is missing")
	}
	if !defaultHistory.SettledByDefault {
		t.Fatal("default decision history settled_by_default = false")
	}
	if defaultHistory.DefaultAppliedAt == nil {
		t.Fatal("default decision history default_applied_at = null")
	}
	if humanHistory.SettledByDefault {
		t.Fatal("human decision history settled_by_default = true")
	}
	if humanHistory.DefaultAppliedAt != nil {
		t.Fatalf("human decision history default_applied_at = %v, want null", humanHistory.DefaultAppliedAt)
	}
}

func TestHTTPDecisionRevisionPreservesSettledDecision(t *testing.T) {
	f := newBareFixture(t)
	afterMs := int64(1)
	original, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:         f.goal.ID,
		Kind:           domain.DecisionKind("question"),
		Question:       "Which deployment plan should be used?",
		Options:        []domain.Option{{Label: "A"}, {Label: "B"}},
		DefaultOption:  "A",
		DefaultAfterMs: &afterMs,
		RunID:          "revision-original-run",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ApplyExpiredDefaults(f.ctx, original.CreatedAt.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if applied, err := f.store.PollDecisions(f.ctx, "revision-original-run", original.ID); err != nil {
		t.Fatal(err)
	} else if len(applied) != 1 {
		t.Fatalf("applied decisions = %+v", applied)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, headers, body := doRequest(t, srv.Client(), http.MethodPost, srv.URL+"/api/decisions/"+original.ID+"/answer", mustJSON(t, map[string]string{
		"answer_label": "B",
	}))
	assertErrorObject(t, status, headers, body, http.StatusConflict)

	status, headers, body = doRequest(t, srv.Client(), http.MethodPost, srv.URL+"/api/decisions/"+original.ID+"/revise", mustJSON(t, struct {
		Options []domain.Option `json:"options"`
	}{
		Options: []domain.Option{{Label: "C"}, {Label: "D"}},
	}))
	if status != http.StatusCreated {
		t.Fatalf("revision status = %d; body=%s", status, body)
	}
	if !strings.HasPrefix(headers.Get("Content-Type"), "application/json") {
		t.Fatalf("revision content type = %q", headers.Get("Content-Type"))
	}
	var revised domain.Decision
	if err := json.Unmarshal(body, &revised); err != nil {
		t.Fatal(err)
	}
	if revised.ID == "" || revised.ID == original.ID {
		t.Fatalf("revised decision id = %q; original = %q", revised.ID, original.ID)
	}
	if !strings.Contains(revised.Question, original.Question) {
		t.Fatalf("revised question = %q; missing original question", revised.Question)
	}
	if !strings.Contains(revised.Question, "A") {
		t.Fatalf("revised question = %q; missing selected option", revised.Question)
	}
	if len(revised.Options) != 2 || revised.Options[0].Label != "C" || revised.Options[1].Label != "D" {
		t.Fatalf("revised options = %+v", revised.Options)
	}

	status, _, body = doRequest(t, srv.Client(), http.MethodGet, srv.URL+"/api/goals/"+f.goal.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("goal status = %d; body=%s", status, body)
	}
	var response struct {
		DecisionHistory []struct {
			DecisionID string     `json:"decision_id"`
			AppliedAt  *time.Time `json:"applied_at"`
		} `json:"decision_history"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.DecisionHistory) != 1 {
		t.Fatalf("decision_history = %+v; body=%s", response.DecisionHistory, body)
	}
	if response.DecisionHistory[0].DecisionID != original.ID {
		t.Fatalf("decision_history id = %q; want original %q", response.DecisionHistory[0].DecisionID, original.ID)
	}
	if response.DecisionHistory[0].AppliedAt == nil {
		t.Fatal("original decision is no longer applied")
	}
}

func TestHTTPInboxIncludesDefaultDecisionFieldsBeforeAnswer(t *testing.T) {
	f := newBareFixture(t)
	afterMs := int64(30 * 60 * 1000)
	decision, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:         f.goal.ID,
		Kind:           domain.DecisionKind("question"),
		Question:       "Which plan should be used?",
		Options:        []domain.Option{{Label: "A"}, {Label: "B"}},
		DefaultOption:  "A",
		DefaultAfterMs: &afterMs,
	})
	if err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, srv.URL+"/api/inbox", nil)
	if status != http.StatusOK {
		t.Fatalf("inbox status = %d; body=%s", status, body)
	}
	var response struct {
		OpenDecisions []decisionViewResponse `json:"open_decisions"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.OpenDecisions) != 1 || response.OpenDecisions[0].ID != decision.ID {
		t.Fatalf("open decisions = %+v", response.OpenDecisions)
	}
	got := response.OpenDecisions[0]
	if got.DefaultOption != "A" {
		t.Fatalf("default_option = %q, want A", got.DefaultOption)
	}
	if got.DefaultAfterMs == nil || *got.DefaultAfterMs != afterMs {
		t.Fatalf("default_after_ms = %v, want %d", got.DefaultAfterMs, afterMs)
	}
}

func TestHTTPInboxIncludesEmptyDefaultOption(t *testing.T) {
	f := newBareFixture(t)
	if _, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:   f.goal.ID,
		Kind:     domain.DecisionKind("question"),
		Question: "Should this irreversible action proceed?",
		Options:  []domain.Option{{Label: "Proceed"}, {Label: "Cancel"}},
	}); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, srv.URL+"/api/inbox", nil)
	if status != http.StatusOK {
		t.Fatalf("inbox status = %d; body=%s", status, body)
	}
	var response struct {
		OpenDecisions []json.RawMessage `json:"open_decisions"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.OpenDecisions) != 1 {
		t.Fatalf("open decisions = %d, want 1", len(response.OpenDecisions))
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(response.OpenDecisions[0], &fields); err != nil {
		t.Fatal(err)
	}
	raw, ok := fields["default_option"]
	if !ok {
		t.Fatal("default_option is missing")
	}
	var got string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("default_option = %q, want empty", got)
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
	decision, err := f.store.CompleteGoalWithReport(f.ctx, f.goal.ID, domain.CompletionReport{
		WorkDone:    "The work is ready",
		NowPossible: "The goal can be approved",
		HowToVerify: "Review the completion report",
		Surprises:   "なし",
		NeedsReview: "なし",
		NextSteps:   "なし",
	}, "completion-run")
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

func TestHTTPGoalDetailIncludesCompletionReportFields(t *testing.T) {
	f := newBareFixture(t)
	if _, err := f.store.CompleteGoalWithReport(f.ctx, f.goal.ID, domain.CompletionReport{
		WorkDone:    "The work is ready",
		NowPossible: "The goal can be approved",
		HowToVerify: "Review the completion report",
		Surprises:   "なし",
		NeedsReview: "なし",
		NextSteps:   "なし",
	}, "completion-run"); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, srv.URL+"/api/goals/"+f.goal.ID, nil)
	if status != http.StatusOK {
		t.Fatalf("goal status = %d; body=%s", status, body)
	}

	var payload struct {
		Goal map[string]json.RawMessage `json:"goal"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode goal detail: %v; body=%s", err, body)
	}
	for _, field := range []string{
		"result_summary",
		"work_done",
		"now_possible",
		"how_to_verify",
		"surprises",
		"needs_review",
		"next_steps",
	} {
		if _, ok := payload.Goal[field]; !ok {
			t.Errorf("goal JSON missing %q", field)
		}
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
	approveDecision, err := f.store.CompleteGoalWithReport(f.ctx, approveGoal.ID, domain.CompletionReport{
		WorkDone:    "finished",
		NowPossible: "The goal is ready for approval",
		HowToVerify: "Inspect the approved goal",
		Surprises:   "なし",
		NeedsReview: "なし",
		NextSteps:   "なし",
	}, "approve-run")
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
	rejectDecision, err := f.store.CompleteGoalWithReport(f.ctx, rejectGoal.ID, domain.CompletionReport{
		WorkDone:    "try again",
		NowPossible: "Rework can continue",
		HowToVerify: "Review the rejection reason",
		Surprises:   "なし",
		NeedsReview: "needs work",
		NextSteps:   "Revise the goal",
	}, "reject-run")
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

func openSSEStream(t *testing.T, ctx context.Context, client *http.Client, endpoint string) (*http.Response, *bufio.Reader) {
	t.Helper()
	response := make(chan struct {
		resp *http.Response
		err  error
	}, 1)
	go func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			response <- struct {
				resp *http.Response
				err  error
			}{err: err}
			return
		}
		resp, err := client.Do(req)
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
	if stream.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(stream.Body)
		t.Fatalf("SSE status = %d; body=%s", stream.StatusCode, body)
	}
	return stream, bufio.NewReader(stream.Body)
}

func eventsURL(baseURL, projectID string) string {
	query := url.Values{}
	query.Set("project_id", projectID)
	return baseURL + "/api/events?" + query.Encode()
}

func TestSSEFiltersDecisionEventsByProjectID(t *testing.T) {
	f := newBareFixture(t)
	otherProject, err := f.store.CreateProject(f.ctx, "other", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	currentGoal, err := f.store.CreateGoal(f.ctx, f.project.ID, "Current project", "")
	if err != nil {
		t.Fatal(err)
	}
	otherGoal, err := f.store.CreateGoal(f.ctx, otherProject.ID, "Other project", "")
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, f.store)
	defer srv.Close()
	streamCtx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	stream, reader := openSSEStream(t, streamCtx, srv.Client(), eventsURL(srv.URL, f.project.ID))
	defer stream.Body.Close()

	otherDecision, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:   otherGoal.ID,
		Kind:     domain.DecisionKind("question"),
		Question: "Other project event",
	})
	if err != nil {
		t.Fatal(err)
	}
	currentDecision, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:   currentGoal.ID,
		Kind:     domain.DecisionKind("question"),
		Question: "Current project event",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSSEDecision(t, reader, "decision.created", currentDecision)
	_ = otherDecision
}

func TestSSEWithoutProjectIDPublishesEventsFromAllProjects(t *testing.T) {
	f := newBareFixture(t)
	otherProject, err := f.store.CreateProject(f.ctx, "other", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	currentGoal, err := f.store.CreateGoal(f.ctx, f.project.ID, "Current project", "")
	if err != nil {
		t.Fatal(err)
	}
	otherGoal, err := f.store.CreateGoal(f.ctx, otherProject.ID, "Other project", "")
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, f.store)
	defer srv.Close()
	streamCtx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	stream, reader := openSSEStream(t, streamCtx, srv.Client(), srv.URL+"/api/events")
	defer stream.Body.Close()

	currentDecision, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:   currentGoal.ID,
		Kind:     domain.DecisionKind("question"),
		Question: "Current project event",
	})
	if err != nil {
		t.Fatal(err)
	}
	otherDecision, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:   otherGoal.ID,
		Kind:     domain.DecisionKind("question"),
		Question: "Other project event",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSSEDecision(t, reader, "decision.created", currentDecision)
	assertSSEDecision(t, reader, "decision.created", otherDecision)
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

	approveDecision, err := f.store.CompleteGoalWithReport(f.ctx, goals[0].ID, domain.CompletionReport{
		WorkDone:    "done",
		NowPossible: "The goal is approved",
		HowToVerify: "Check the approval event",
		Surprises:   "なし",
		NeedsReview: "なし",
		NextSteps:   "なし",
	}, "approve-run")
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

	rejectDecision, err := f.store.CompleteGoalWithReport(f.ctx, goals[1].ID, domain.CompletionReport{
		WorkDone:    "not yet",
		NowPossible: "Rework can continue",
		HowToVerify: "Check the rejection event",
		Surprises:   "なし",
		NeedsReview: "needs work",
		NextSteps:   "Revise and retry",
	}, "reject-run")
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
