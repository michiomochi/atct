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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
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
	otherGoal, err := f.store.CreateGoal(f.ctx, otherProject.ID, "Other project goal", "human")
	if err != nil {
		t.Fatal(err)
	}
	otherTasks, err := f.store.DeclareTasks(f.ctx, otherGoal.ID, "other-agent", "other-declare", []string{"needs"}, []string{"Complete the other project's work before answering its decision."})
	if err != nil {
		t.Fatal(err)
	}
	otherRunID := registerTestSession(t, f.store, "other-run", 0)
	if _, err := f.store.ClaimTask(f.ctx, otherTasks[0].ID, otherRunID); err != nil {
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
			ID          int64  `json:"id"`
			ProjectID   int64  `json:"project_id"`
			ProjectName string `json:"project_name"`
		} `json:"attention_tasks"`
	}
	if err := json.Unmarshal(body, &inbox); err != nil {
		t.Fatal(err)
	}
	if len(inbox.AttentionTasks) != 2 {
		t.Fatalf("attention tasks = %+v", inbox.AttentionTasks)
	}
	got := make(map[int64]struct {
		projectID   int64
		projectName string
	})
	for _, task := range inbox.AttentionTasks {
		got[task.ID] = struct {
			projectID   int64
			projectName string
		}{projectID: task.ProjectID, projectName: task.ProjectName}
	}
	want := map[int64]struct {
		projectID   int64
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
			t.Fatalf("attention task %d identity = %+v, want %+v", taskID, gotIdentity, wantIdentity)
		}
	}
}

type decisionViewResponse struct {
	domain.Decision
	ProjectID        int64  `json:"project_id"`
	GoalHeadline     string `json:"goal_headline"`
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
	otherGoal, err := f.store.CreateGoal(f.ctx, otherProject.ID, "Other goal", "human")
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
	got := make(map[int64]string, len(response.OpenDecisions))
	for _, decision := range response.OpenDecisions {
		got[decision.ID] = decision.GoalHeadline
	}
	want := map[int64]string{
		first.ID:  domain.Headline(f.goal.Content),
		second.ID: domain.Headline(otherGoal.Content),
	}
	if len(got) != len(want) {
		t.Fatalf("open decision goal headlines = %+v, want %+v", got, want)
	}
	for decisionID, wantHeadline := range want {
		if gotHeadline := got[decisionID]; gotHeadline != wantHeadline {
			t.Fatalf("decision %d goal headline = %q, want %q", decisionID, gotHeadline, wantHeadline)
		}
	}
}

func TestHTTPInboxIncludesTasksPerActiveGoalInOrder(t *testing.T) {
	f := newBareFixture(t)
	firstTasks, err := f.store.DeclareTasks(f.ctx, f.goal.ID, "first-agent", "first-declare", []string{"first task", "second task"}, []string{"Finish the first task in order.", "Finish the second task after the first task."})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.UpdateTask(f.ctx, firstTasks[1].ID, domain.TaskDoing, 0); err != nil {
		t.Fatal(err)
	}
	secondGoal, err := f.store.CreateGoal(f.ctx, f.project.ID, "Second goal", "human")
	if err != nil {
		t.Fatal(err)
	}
	secondTasks, err := f.store.DeclareTasks(f.ctx, secondGoal.ID, "second-agent", "second-declare", []string{"other task"}, []string{"Finish the task belonging to the second goal."})
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
			ID               int64 `json:"id"`
			AwaitingDecision bool  `json:"awaiting_decision"`
			Tasks            []struct {
				ID     int64 `json:"id"`
				GoalID int64 `json:"goal_id"`
				Order  int   `json:"order"`
			} `json:"tasks"`
		} `json:"active_goals"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}

	goals := make(map[int64][]struct {
		ID     int64 `json:"id"`
		GoalID int64 `json:"goal_id"`
		Order  int   `json:"order"`
	}, len(response.ActiveGoals))
	awaiting := make(map[int64]bool, len(response.ActiveGoals))
	for _, goal := range response.ActiveGoals {
		tasks := make([]struct {
			ID     int64 `json:"id"`
			GoalID int64 `json:"goal_id"`
			Order  int   `json:"order"`
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
		t.Fatalf("active goal %d missing from response: %+v", f.goal.ID, response.ActiveGoals)
	}
	if len(firstGot) != len(firstTasks) {
		t.Fatalf("first goal tasks = %+v, want %d tasks", firstGot, len(firstTasks))
	}
	for i, wantTask := range firstTasks {
		gotTask := firstGot[i]
		if gotTask.ID != wantTask.ID || gotTask.GoalID != f.goal.ID || gotTask.Order != i {
			t.Fatalf("first goal task %d = %+v, want id=%d goal_id=%d order=%d", i, gotTask, wantTask.ID, f.goal.ID, i)
		}
	}

	secondGot, ok := goals[secondGoal.ID]
	if !ok {
		t.Fatalf("active goal %d missing from response: %+v", secondGoal.ID, response.ActiveGoals)
	}
	if len(secondGot) != len(secondTasks) || secondGot[0].ID != secondTasks[0].ID || secondGot[0].GoalID != secondGoal.ID {
		t.Fatalf("second goal tasks = %+v, want task %d owned by %d", secondGot, secondTasks[0].ID, secondGoal.ID)
	}
}

func TestHTTPInboxProposedGoalsAreSeparateNonNilAndDisappearOnReject(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	client := srv.Client()
	readInbox := func() map[string]json.RawMessage {
		status, _, body := doRequest(t, client, http.MethodGet, srv.URL+"/api/inbox", nil)
		if status != http.StatusOK {
			t.Fatalf("inbox status = %d; body=%s", status, body)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(body, &raw); err != nil {
			t.Fatalf("decode inbox: %v; body=%s", err, body)
		}
		return raw
	}

	raw := readInbox()
	if got := string(raw["proposed_goals"]); got != "[]" {
		t.Fatalf("empty proposed_goals = %s, want []", got)
	}

	proposed, err := f.store.CreateGoal(f.ctx, f.project.ID, "Needs approval\n\nWait for human approval", "agent")
	if err != nil {
		t.Fatal(err)
	}
	raw = readInbox()
	var activeGoals []struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(raw["active_goals"], &activeGoals); err != nil {
		t.Fatalf("decode active_goals: %v", err)
	}
	for _, goal := range activeGoals {
		if goal.ID == proposed.ID {
			t.Fatalf("proposed goal leaked into active_goals: %s", string(raw["active_goals"]))
		}
	}
	var proposedGoals []struct {
		ID          int64     `json:"id"`
		Content     string    `json:"content"`
		CreatedAt   time.Time `json:"created_at"`
		ProjectName string    `json:"project_name"`
	}
	if err := json.Unmarshal(raw["proposed_goals"], &proposedGoals); err != nil {
		t.Fatalf("decode proposed_goals: %v", err)
	}
	if len(proposedGoals) != 1 || proposedGoals[0].ID != proposed.ID || proposedGoals[0].Content != proposed.Content || proposedGoals[0].ProjectName != "fixture" || proposedGoals[0].CreatedAt.IsZero() {
		t.Fatalf("proposed_goals = %+v; raw=%s", proposedGoals, string(raw["proposed_goals"]))
	}
	var proposedObjects []map[string]json.RawMessage
	if err := json.Unmarshal(raw["proposed_goals"], &proposedObjects); err != nil {
		t.Fatalf("decode proposed goal objects: %v", err)
	}
	if _, ok := proposedObjects[0]["tasks"]; ok {
		t.Fatalf("proposed goal unexpectedly includes tasks: %s", string(raw["proposed_goals"]))
	}

	decisions, err := f.store.ListOpenDecisions(f.ctx, proposed.ID)
	if err != nil {
		t.Fatalf("ListOpenDecisions: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("proposed goal decisions = %+v", decisions)
	}
	status, _, body := doRequest(t, client, http.MethodPost, urlID(srv.URL+"/api/decisions/", decisions[0].ID)+"/reject", mustJSON(t, map[string]string{"reason": "not approved"}))
	if status != http.StatusOK {
		t.Fatalf("reject status = %d; body=%s", status, body)
	}
	raw = readInbox()
	if got := string(raw["proposed_goals"]); got != "[]" {
		t.Fatalf("proposed_goals after reject = %s, want []", got)
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
	f.tasks, err = f.store.DeclareTasks(f.ctx, f.goal.ID, "fixture-agent", "fixture-declare", []string{"needs", "now", "next"}, []string{"Resolve the prerequisite work.", "Continue the current implementation work.", "Complete the remaining follow-up work."})
	if err != nil {
		t.Fatal(err)
	}
	fixtureRunID := registerTestSession(t, f.store, "fixture-run", 0)
	if _, err := f.store.ClaimTask(f.ctx, f.tasks[0].ID, fixtureRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.UpdateTask(f.ctx, f.tasks[1].ID, domain.TaskDoing, 0); err != nil {
		t.Fatal(err)
	}
	f.open, err = f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:         f.goal.ID,
		TaskID:         f.tasks[0].ID,
		Kind:           domain.DecisionKind("question"),
		Question:       "Which option?",
		AgentSessionID: fixtureRunID,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.answered, err = f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:         f.goal.ID,
		TaskID:         f.tasks[2].ID,
		Kind:           domain.DecisionKind("question"),
		Question:       "Already answered?",
		AgentSessionID: fixtureRunID,
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
	goal, err := db.CreateGoal(ctx, ns.ID, "Fixture goal\n\nFor HTTP API tests", "human")
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
		t.Fatalf("project_id = %d, want %d", response.UnappliedDecisions[0].ProjectID, f.goal.ProjectID)
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
		AgentSessionID: testSessionID("default-run"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ApplyExpiredDefaults(f.ctx, defaultDecision.CreatedAt.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if applied, err := f.store.PollDecisions(f.ctx, testSessionID("default-run"), defaultDecision.ID); err != nil {
		t.Fatal(err)
	} else if len(applied) != 1 {
		t.Fatalf("default applied decisions = %+v", applied)
	}

	humanDecision, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:         f.goal.ID,
		Kind:           domain.DecisionKind("question"),
		Question:       "Which option should a person choose?",
		Options:        []domain.Option{{Label: "A"}, {Label: "B"}},
		AgentSessionID: testSessionID("human-run"),
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
	if applied, err := f.store.PollDecisions(f.ctx, testSessionID("human-run"), humanDecision.ID); err != nil {
		t.Fatal(err)
	} else if len(applied) != 1 {
		t.Fatalf("human applied decisions = %+v", applied)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/goals/", f.goal.ID), nil)
	if status != http.StatusOK {
		t.Fatalf("goal status = %d; body=%s", status, body)
	}
	var response struct {
		DecisionHistory []struct {
			DecisionID       int64      `json:"decision_id"`
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
		AgentSessionID: testSessionID("revision-original-run"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ApplyExpiredDefaults(f.ctx, original.CreatedAt.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if applied, err := f.store.PollDecisions(f.ctx, testSessionID("revision-original-run"), original.ID); err != nil {
		t.Fatal(err)
	} else if len(applied) != 1 {
		t.Fatalf("applied decisions = %+v", applied)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, headers, body := doRequest(t, srv.Client(), http.MethodPost, urlID(srv.URL+"/api/decisions/", original.ID)+"/answer", mustJSON(t, map[string]string{
		"answer_label": "B",
	}))
	assertErrorObject(t, status, headers, body, http.StatusConflict)

	status, headers, body = doRequest(t, srv.Client(), http.MethodPost, urlID(srv.URL+"/api/decisions/", original.ID)+"/revise", mustJSON(t, struct {
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
	if revised.ID == 0 || revised.ID == original.ID {
		t.Fatalf("revised decision id = %d; original = %d", revised.ID, original.ID)
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

	status, _, body = doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/goals/", f.goal.ID), nil)
	if status != http.StatusOK {
		t.Fatalf("goal status = %d; body=%s", status, body)
	}
	var response struct {
		DecisionHistory []struct {
			DecisionID int64      `json:"decision_id"`
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
		t.Fatalf("decision_history id = %d; want original %d", response.DecisionHistory[0].DecisionID, original.ID)
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

	status, _, body = doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/goals/", f.goal.ID), nil)
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

func TestHTTPGoalDetailIncludesAllTasksWithoutCrossGoalMixing(t *testing.T) {
	f := newBareFixture(t)
	targetTasks, err := f.store.DeclareTasks(f.ctx, f.goal.ID, "target-agent", "target-declare", []string{"first task", "second task"}, []string{"Complete the first target task.", "Complete the second target task."})
	if err != nil {
		t.Fatal(err)
	}
	otherGoal, err := f.store.CreateGoal(f.ctx, f.project.ID, "Other goal", "human")
	if err != nil {
		t.Fatal(err)
	}
	otherTasks, err := f.store.DeclareTasks(f.ctx, otherGoal.ID, "other-agent", "other-declare", []string{"other task"}, []string{"Complete the task from the other goal."})
	if err != nil {
		t.Fatal(err)
	}
	emptyGoal, err := f.store.CreateGoal(f.ctx, f.project.ID, "Empty goal", "human")
	if err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/goals/", f.goal.ID), nil)
	if status != http.StatusOK {
		t.Fatalf("goal status = %d; body=%s", status, body)
	}
	var payload struct {
		Goal map[string]json.RawMessage `json:"goal"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode goal detail: %v; body=%s", err, body)
	}
	tasksRaw, ok := payload.Goal["tasks"]
	if !ok || bytes.Equal(bytes.TrimSpace(tasksRaw), []byte("null")) {
		t.Fatalf("goal.tasks is missing or null: %s", body)
	}
	var gotTasks []httpapi.TaskView
	if err := json.Unmarshal(tasksRaw, &gotTasks); err != nil {
		t.Fatalf("decode goal.tasks: %v; tasks=%s", err, tasksRaw)
	}
	if len(gotTasks) != len(targetTasks) {
		t.Fatalf("goal.tasks = %+v, want %d tasks", gotTasks, len(targetTasks))
	}
	for i, wantTask := range targetTasks {
		gotTask := gotTasks[i]
		if gotTask.ID == otherTasks[0].ID || gotTask.GoalID != f.goal.ID {
			t.Fatalf("goal.tasks[%d] crosses goal boundary: %+v", i, gotTask)
		}
		if gotTask.ID != wantTask.ID || gotTask.Order != wantTask.Order {
			t.Fatalf("goal.tasks[%d] = %+v, want id=%d order=%d", i, gotTask, wantTask.ID, wantTask.Order)
		}
	}

	status, _, body = doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/goals/", emptyGoal.ID), nil)
	if status != http.StatusOK {
		t.Fatalf("empty goal status = %d; body=%s", status, body)
	}
	var emptyPayload struct {
		Goal map[string]json.RawMessage `json:"goal"`
	}
	if err := json.Unmarshal(body, &emptyPayload); err != nil {
		t.Fatalf("decode empty goal detail: %v; body=%s", err, body)
	}
	emptyTasks, ok := emptyPayload.Goal["tasks"]
	if !ok || !bytes.Equal(bytes.TrimSpace(emptyTasks), []byte("[]")) {
		t.Fatalf("empty goal.tasks = %s, want []", emptyTasks)
	}
}

func TestHTTPGoalDetailIncludesDerivedFromGoal(t *testing.T) {
	f := newBareFixture(t)
	parent, err := f.store.CreateGoal(f.ctx, f.project.ID, "Parent goal", "human")
	if err != nil {
		t.Fatal(err)
	}
	child, err := f.store.CreateGoal(f.ctx, f.project.ID, "Child goal", "human", parent.ID)
	if err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/goals/", child.ID), nil)
	if status != http.StatusOK {
		t.Fatalf("goal status = %d; body=%s", status, body)
	}
	var payload struct {
		DerivedFrom *struct {
			ID          int64  `json:"id"`
			Headline    string `json:"headline"`
			ProjectName string `json:"project_name"`
		} `json:"derived_from"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode goal detail: %v; body=%s", err, body)
	}
	if payload.DerivedFrom == nil {
		t.Fatalf("derived_from is missing: %s", body)
	}
	if payload.DerivedFrom.ID != parent.ID || payload.DerivedFrom.Headline != "Parent goal" {
		t.Fatalf("derived_from = %+v, want id=%d headline=%q", *payload.DerivedFrom, parent.ID, "Parent goal")
	}
}

func TestHTTPGoalDetailOmitsDerivedFromGoalWhenUnset(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/goals/", f.goal.ID), nil)
	if status != http.StatusOK {
		t.Fatalf("goal status = %d; body=%s", status, body)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode goal detail: %v; body=%s", err, body)
	}
	if _, ok := payload["derived_from"]; ok {
		t.Fatalf("derived_from should be omitted: %s", body)
	}
}

func TestHTTPSetGoalDerivedFromAndDistinguishesErrors(t *testing.T) {
	f := newBareFixture(t)
	parent, err := f.store.CreateGoal(f.ctx, f.project.ID, "Parent goal", "human")
	if err != nil {
		t.Fatal(err)
	}
	child, err := f.store.CreateGoal(f.ctx, f.project.ID, "Child goal", "human")
	if err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodPost, urlID(srv.URL+"/api/goals/", child.ID)+"/derived-from", mustJSON(t, map[string]string{
		"derived_from_goal_id": idText(parent.ID),
	}))
	if status != http.StatusOK {
		t.Fatalf("set derived-from status = %d; body=%s", status, body)
	}
	var updated domain.Goal
	if err := json.Unmarshal(body, &updated); err != nil {
		t.Fatalf("decode updated goal: %v; body=%s", err, body)
	}
	if updated.DerivedFromGoalID != parent.ID {
		t.Fatalf("updated DerivedFromGoalID = %d, want %d", updated.DerivedFromGoalID, parent.ID)
	}

	status, _, body = doRequest(t, srv.Client(), http.MethodPost, urlID(srv.URL+"/api/goals/", child.ID)+"/derived-from", mustJSON(t, map[string]string{
		"derived_from_goal_id": "missing-goal-id",
	}))
	if status != http.StatusNotFound || !strings.Contains(string(body), "id must be a number; UUID-style ids were removed in 0020.") || !strings.Contains(string(body), "doc/specs/2026-08-27-uuid-to-integer-mapping.md") {
		t.Fatalf("removed-format parent response = %d %s, want 404 with migration guidance", status, body)
	}

	status, _, body = doRequest(t, srv.Client(), http.MethodPost, urlID(srv.URL+"/api/goals/", child.ID)+"/derived-from", mustJSON(t, map[string]string{
		"derived_from_goal_id": idText(child.ID),
	}))
	if status != http.StatusBadRequest || !strings.Contains(string(body), "cannot be derived from itself") {
		t.Fatalf("self-reference response = %d %s, want 400 with self-reference error", status, body)
	}
}

func TestHTTPGoalDetailOmitsMissingDerivedFromGoal(t *testing.T) {
	f := newBareFixture(t)
	child, err := f.store.CreateGoal(f.ctx, f.project.ID, "Child goal", "human")
	if err != nil {
		t.Fatal(err)
	}
	missingParentID := "missing-parent-id"
	if _, err := f.store.DB().ExecContext(f.ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("disable foreign keys: %v", err)
	}
	if _, err := f.store.DB().ExecContext(f.ctx, "UPDATE goals SET derived_from_goal_id = ? WHERE id = ?", missingParentID, child.ID); err != nil {
		t.Fatalf("set missing parent: %v", err)
	}
	if _, err := f.store.DB().ExecContext(f.ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("restore foreign keys: %v", err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/goals/", child.ID), nil)
	if status != http.StatusOK {
		t.Fatalf("goal status = %d; body=%s", status, body)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode goal detail: %v; body=%s", err, body)
	}
	if _, ok := payload["derived_from"]; ok {
		t.Fatalf("derived_from should be omitted when parent is missing: %s", body)
	}
}

func TestHTTPGoalDetailIncludesDerivedGoals(t *testing.T) {
	f := newBareFixture(t)
	parent, err := f.store.CreateGoal(f.ctx, f.project.ID, "Parent goal", "human")
	if err != nil {
		t.Fatal(err)
	}
	first, err := f.store.CreateGoal(f.ctx, f.project.ID, "First derived goal", "human", parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.store.CreateGoal(f.ctx, f.project.ID, "Second derived goal", "human", parent.ID)
	if err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/goals/", parent.ID), nil)
	if status != http.StatusOK {
		t.Fatalf("goal status = %d; body=%s", status, body)
	}
	var payload struct {
		DerivedGoals []struct {
			ID       int64  `json:"id"`
			Headline string `json:"headline"`
		} `json:"derived_goals"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode goal detail: %v; body=%s", err, body)
	}
	if len(payload.DerivedGoals) != 2 {
		t.Fatalf("derived_goals = %+v, want 2 entries", payload.DerivedGoals)
	}
	got := make(map[int64]string, len(payload.DerivedGoals))
	for _, derived := range payload.DerivedGoals {
		got[derived.ID] = derived.Headline
	}
	if got[first.ID] != "First derived goal" || got[second.ID] != "Second derived goal" {
		t.Fatalf("derived_goals = %+v, want both derived goals", got)
	}
}

func TestHTTPGoalDetailReturnsEmptyDerivedGoalsArray(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/goals/", f.goal.ID), nil)
	if status != http.StatusOK {
		t.Fatalf("goal status = %d; body=%s", status, body)
	}
	var payload struct {
		DerivedGoals []json.RawMessage `json:"derived_goals"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode goal detail: %v; body=%s", err, body)
	}
	if payload.DerivedGoals == nil {
		t.Fatalf("derived_goals is missing or null: %s", body)
	}
	if len(payload.DerivedGoals) != 0 {
		t.Fatalf("derived_goals = %s, want []", body)
	}
}

func TestHTTPGoalDetailIncludesAllTaskCommitsInTaskOrder(t *testing.T) {
	f := newBareFixture(t)
	tasks, err := f.store.DeclareTasks(f.ctx, f.goal.ID, "commit-agent", "commit-declare", []string{"first task", "second task"}, []string{"Complete the first task.", "Complete the second task."})
	if err != nil {
		t.Fatal(err)
	}
	commitSpecs := [][]domain.TaskCommit{
		{
			{SHA: "1111111111111111111111111111111111111111", Subject: "first task commit one", FilesChanged: 1, Insertions: 2, Deletions: 0, CreatedAt: time.Now().UTC()},
			{SHA: "2222222222222222222222222222222222222222", Subject: "first task commit two", FilesChanged: 2, Insertions: 3, Deletions: 1, CreatedAt: time.Now().UTC()},
		},
		{
			{SHA: "3333333333333333333333333333333333333333", Subject: "second task commit one", FilesChanged: 3, Insertions: 4, Deletions: 2, CreatedAt: time.Now().UTC()},
			{SHA: "4444444444444444444444444444444444444444", Subject: "second task commit two", FilesChanged: 4, Insertions: 5, Deletions: 3, CreatedAt: time.Now().UTC()},
		},
	}
	for i, task := range tasks {
		for _, commit := range commitSpecs[i] {
			if err := f.store.LinkTaskCommit(f.ctx, task.ID, commit); err != nil {
				t.Fatal(err)
			}
		}
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/goals/", f.goal.ID), nil)
	if status != http.StatusOK {
		t.Fatalf("goal status = %d; body=%s", status, body)
	}
	var response struct {
		TaskCommits []struct {
			TaskID    int64  `json:"task_id"`
			TaskTitle string `json:"task_title"`
			Commits   []struct {
				SHA     string `json:"sha"`
				Subject string `json:"subject"`
			} `json:"commits"`
		} `json:"task_commits"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.TaskCommits) != 2 {
		t.Fatalf("task_commits = %+v, want two task entries", response.TaskCommits)
	}
	for i, task := range tasks {
		entry := response.TaskCommits[i]
		if entry.TaskID != task.ID || entry.TaskTitle != task.Title {
			t.Fatalf("task_commits[%d] = %+v, want task %d (%s)", i, entry, task.ID, task.Title)
		}
		if len(entry.Commits) != 2 {
			t.Fatalf("task_commits[%d].commits = %+v, want two commits", i, entry.Commits)
		}
		gotSubjects := make(map[string]bool, len(entry.Commits))
		for _, commit := range entry.Commits {
			gotSubjects[commit.SHA] = true
		}
		for _, want := range commitSpecs[i] {
			if !gotSubjects[want.SHA] {
				t.Fatalf("task_commits[%d].commits = %+v, missing %s", i, entry.Commits, want.SHA)
			}
		}
	}
}

func TestHTTPGoalDetailReturnsEmptyTaskCommitsArrayWithoutCommits(t *testing.T) {
	f := newBareFixture(t)
	if _, err := f.store.DeclareTasks(f.ctx, f.goal.ID, "empty-commit-agent", "empty-commit-declare", []string{"first task", "second task"}, []string{"Complete the first task.", "Complete the second task."}); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/goals/", f.goal.ID), nil)
	if status != http.StatusOK {
		t.Fatalf("goal status = %d; body=%s", status, body)
	}
	var response struct {
		TaskCommits []json.RawMessage `json:"task_commits"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response.TaskCommits == nil {
		t.Fatalf("task_commits is null; body=%s", body)
	}
	if len(response.TaskCommits) != 0 {
		t.Fatalf("task_commits = %s, want []", body)
	}
}

func TestHTTPGoalDetailOmitsTasksWithoutCommits(t *testing.T) {
	f := newBareFixture(t)
	tasks, err := f.store.DeclareTasks(f.ctx, f.goal.ID, "mixed-commit-agent", "mixed-commit-declare", []string{"with commits", "without commits"}, []string{"Complete the first task.", "Complete the second task."})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.LinkTaskCommit(f.ctx, tasks[0].ID, domain.TaskCommit{
		SHA:          "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Subject:      "linked commit",
		FilesChanged: 1,
		Insertions:   1,
		CreatedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/goals/", f.goal.ID), nil)
	if status != http.StatusOK {
		t.Fatalf("goal status = %d; body=%s", status, body)
	}
	var response struct {
		TaskCommits []struct {
			TaskID int64 `json:"task_id"`
		} `json:"task_commits"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.TaskCommits) != 1 {
		t.Fatalf("task_commits = %+v, want one task entry", response.TaskCommits)
	}
	if response.TaskCommits[0].TaskID != tasks[0].ID {
		t.Fatalf("task_commits = %+v, want only task %d", response.TaskCommits, tasks[0].ID)
	}
}

func TestHTTPGoalDetailDecisionHistoryIncludesTaskIDs(t *testing.T) {
	f := newBareFixture(t)
	tasks, err := f.store.DeclareTasks(f.ctx, f.goal.ID, "history-agent", "history-declare", []string{"first task", "second task"}, []string{"Complete the first task before recording its decision.", "Complete the second task before recording its decision."})
	if err != nil {
		t.Fatal(err)
	}

	decisionsByTask := make(map[int64]domain.Decision, len(tasks))
	for i, task := range tasks {
		decision, err := f.store.AskDecision(f.ctx, store.AskInput{
			GoalID:         f.goal.ID,
			TaskID:         task.ID,
			Kind:           domain.DecisionKind("question"),
			Question:       fmt.Sprintf("Question for task %d", i),
			AgentSessionID: testSessionID(fmt.Sprintf("history-run-%d", i)),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.AnswerDecision(f.ctx, store.AnswerInput{
			DecisionID:  decision.ID,
			AnswerLabel: fmt.Sprintf("answer-%d", i),
		}); err != nil {
			t.Fatal(err)
		}
		if applied, err := f.store.PollDecisions(f.ctx, decision.AgentSessionID, decision.ID); err != nil {
			t.Fatal(err)
		} else if len(applied) != 1 {
			t.Fatalf("applied decisions for task %d = %+v", task.ID, applied)
		}
		decisionsByTask[task.ID] = decision
	}

	taskless, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:         f.goal.ID,
		Kind:           domain.DecisionKind("question"),
		Question:       "Question without a task",
		AgentSessionID: testSessionID("history-taskless-run"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.AnswerDecision(f.ctx, store.AnswerInput{
		DecisionID: taskless.ID,
		AnswerText: "taskless answer",
	}); err != nil {
		t.Fatal(err)
	}
	if applied, err := f.store.PollDecisions(f.ctx, taskless.AgentSessionID, taskless.ID); err != nil {
		t.Fatal(err)
	} else if len(applied) != 1 {
		t.Fatalf("taskless applied decisions = %+v", applied)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/goals/", f.goal.ID), nil)
	if status != http.StatusOK {
		t.Fatalf("goal status = %d; body=%s", status, body)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode goal detail: %v; body=%s", err, body)
	}
	historyRaw, ok := payload["decision_history"]
	if !ok || bytes.Equal(bytes.TrimSpace(historyRaw), []byte("null")) {
		t.Fatalf("decision_history is missing or null: %s", body)
	}
	var history []json.RawMessage
	if err := json.Unmarshal(historyRaw, &history); err != nil {
		t.Fatalf("decode decision_history: %v; history=%s", err, historyRaw)
	}

	entriesByDecision := make(map[int64]map[string]json.RawMessage, len(history))
	for _, raw := range history {
		var entry map[string]json.RawMessage
		if err := json.Unmarshal(raw, &entry); err != nil {
			t.Fatalf("decode history entry: %v; entry=%s", err, raw)
		}
		decisionIDRaw, ok := entry["decision_id"]
		if !ok {
			t.Fatalf("history entry has no decision_id: %s", raw)
		}
		var decisionID int64
		if err := json.Unmarshal(decisionIDRaw, &decisionID); err != nil {
			t.Fatalf("decode history decision_id: %v; entry=%s", err, raw)
		}
		entriesByDecision[decisionID] = entry
	}

	for taskID, decision := range decisionsByTask {
		entry, ok := entriesByDecision[decision.ID]
		if !ok {
			t.Fatalf("decision %d missing from history", decision.ID)
		}
		taskIDRaw, ok := entry["task_id"]
		if !ok || bytes.Equal(bytes.TrimSpace(taskIDRaw), []byte("null")) {
			t.Fatalf("history entry task_id is missing or null: %v", entry)
		}
		var gotTaskID int64
		if err := json.Unmarshal(taskIDRaw, &gotTaskID); err != nil {
			t.Fatalf("decode history task_id: %v; entry=%s", err, entry)
		}
		if gotTaskID != taskID {
			t.Fatalf("history task_id = %d, want %d; entry=%v", gotTaskID, taskID, entry)
		}
	}

	tasklessEntry, ok := entriesByDecision[taskless.ID]
	if !ok {
		t.Fatalf("taskless decision %d missing from history", taskless.ID)
	}
	tasklessTaskID, ok := tasklessEntry["task_id"]
	if !ok || bytes.Equal(bytes.TrimSpace(tasklessTaskID), []byte("null")) {
		t.Fatalf("taskless history task_id is missing or null: %v", tasklessEntry)
	}
	var gotTasklessTaskID int64
	if err := json.Unmarshal(tasklessTaskID, &gotTasklessTaskID); err != nil {
		t.Fatalf("decode taskless history task_id: %v; entry=%s", err, tasklessEntry)
	}
	if gotTasklessTaskID != 0 {
		t.Fatalf("taskless history task_id = %d, want zero", gotTasklessTaskID)
	}

	firstHistory := entriesByDecision[decisionsByTask[tasks[0].ID].ID]
	var firstTaskID int64
	if err := json.Unmarshal(firstHistory["task_id"], &firstTaskID); err != nil {
		t.Fatal(err)
	}
	if firstTaskID == tasks[1].ID {
		t.Fatalf("first task history points to second task: %d", firstTaskID)
	}
}

func TestHTTPTaskDetailReturnsTaskAndDecisionData(t *testing.T) {
	f := newBareFixture(t)
	tasks, err := f.store.DeclareTasks(
		f.ctx,
		f.goal.ID,
		"fixture-agent",
		"task-detail-declare",
		[]string{"Target task", "Other task"},
		[]string{"The task shown on the detail page.", "A different task in the same goal."},
	)
	if err != nil {
		t.Fatal(err)
	}

	targetApplied, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:         f.goal.ID,
		TaskID:         tasks[0].ID,
		Kind:           domain.DecisionKind("question"),
		Question:       "Which target path should be used?",
		AgentSessionID: testSessionID("target-run"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.AnswerDecision(f.ctx, store.AnswerInput{
		DecisionID:  targetApplied.ID,
		AnswerLabel: "Use target path",
		AnswerText:  "The target path is the one to use.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.PollDecisions(f.ctx, testSessionID("target-run"), targetApplied.ID); err != nil {
		t.Fatal(err)
	}

	otherApplied, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:         f.goal.ID,
		TaskID:         tasks[1].ID,
		Kind:           domain.DecisionKind("question"),
		Question:       "Which other path should be used?",
		AgentSessionID: testSessionID("other-run"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.AnswerDecision(f.ctx, store.AnswerInput{
		DecisionID:  otherApplied.ID,
		AnswerLabel: "Use other path",
		AnswerText:  "The other path is not part of the target task.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.PollDecisions(f.ctx, testSessionID("other-run"), otherApplied.ID); err != nil {
		t.Fatal(err)
	}

	targetOpen, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:         f.goal.ID,
		TaskID:         tasks[0].ID,
		Kind:           domain.DecisionKind("question"),
		Question:       "Which target option is still open?",
		AgentSessionID: testSessionID("target-run"),
	})
	if err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, headers, body := doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/tasks/", tasks[0].ID), nil)
	if status != http.StatusOK {
		t.Fatalf("task detail status = %d; body=%s", status, body)
	}
	if !strings.HasPrefix(headers.Get("Content-Type"), "application/json") {
		t.Fatalf("task detail content type = %q", headers.Get("Content-Type"))
	}
	t.Logf("task detail JSON: %s", strings.TrimSpace(string(body)))

	var response struct {
		Task domain.Task `json:"task"`
		Goal struct {
			ID          int64  `json:"id"`
			Headline    string `json:"headline"`
			ProjectName string `json:"project_name"`
		} `json:"goal"`
		OpenDecisions   []domain.Decision `json:"open_decisions"`
		DecisionHistory []struct {
			DecisionID  int64  `json:"decision_id"`
			TaskID      int64  `json:"task_id"`
			Question    string `json:"question"`
			AnswerLabel string `json:"answer_label"`
			AnswerText  string `json:"answer_text"`
		} `json:"decision_history"`
		DecisionHistoryOmitted int `json:"decision_history_omitted"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode task detail: %v; body=%s", err, body)
	}
	if response.Task.ID != tasks[0].ID || response.Task.GoalID != f.goal.ID || response.Task.Title != tasks[0].Title || response.Task.Description != tasks[0].Description {
		t.Fatalf("task = %+v, want target task %+v", response.Task, tasks[0])
	}
	if response.Goal.ID != f.goal.ID || response.Goal.Headline != domain.Headline(f.goal.Content) || response.Goal.ProjectName != "fixture" {
		t.Fatalf("goal = %+v, want id=%d headline=%q project_name=%q", response.Goal, f.goal.ID, domain.Headline(f.goal.Content), "fixture")
	}
	if len(response.OpenDecisions) != 1 || response.OpenDecisions[0].ID != targetOpen.ID || response.OpenDecisions[0].TaskID != tasks[0].ID {
		t.Fatalf("open_decisions = %+v, want only target decision %d", response.OpenDecisions, targetOpen.ID)
	}
	if len(response.DecisionHistory) != 1 {
		t.Fatalf("decision_history = %+v, want only target history", response.DecisionHistory)
	}
	history := response.DecisionHistory[0]
	if history.DecisionID != targetApplied.ID || history.TaskID != tasks[0].ID || history.Question != targetApplied.Question || history.AnswerLabel != "Use target path" || history.AnswerText != "The target path is the one to use." {
		t.Fatalf("decision_history[0] = %+v, want target decision %d", history, targetApplied.ID)
	}
	if history.DecisionID == otherApplied.ID || history.TaskID == tasks[1].ID {
		t.Fatalf("other task history leaked into response: %+v", history)
	}
	if response.DecisionHistoryOmitted != 0 {
		t.Fatalf("decision_history_omitted = %d, want 0", response.DecisionHistoryOmitted)
	}
}

func TestHTTPTaskDetailDoesNotCapHistoryByAnotherTask(t *testing.T) {
	f := newBareFixture(t)
	tasks, err := f.store.DeclareTasks(
		f.ctx,
		f.goal.ID,
		"fixture-agent",
		"task-history-cap-declare",
		[]string{"Target task", "Other task"},
		[]string{"The task whose history is requested.", "A different task in the same goal."},
	)
	if err != nil {
		t.Fatal(err)
	}

	target, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:         f.goal.ID,
		TaskID:         tasks[0].ID,
		Kind:           domain.DecisionKind("question"),
		Question:       "Target history",
		AgentSessionID: testSessionID("target-history-run"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.AnswerDecision(f.ctx, store.AnswerInput{
		DecisionID:  target.ID,
		AnswerLabel: "Target answer",
		AnswerText:  "The target answer.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.PollDecisions(f.ctx, testSessionID("target-history-run"), target.ID); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 20; i++ {
		decision, err := f.store.AskDecision(f.ctx, store.AskInput{
			GoalID:         f.goal.ID,
			TaskID:         tasks[1].ID,
			Kind:           domain.DecisionKind("question"),
			Question:       "Other task history",
			AgentSessionID: testSessionID("other-history-run"),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.AnswerDecision(f.ctx, store.AnswerInput{
			DecisionID:  decision.ID,
			AnswerLabel: "Other answer",
			AnswerText:  "The other task answer.",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.PollDecisions(f.ctx, testSessionID("other-history-run"), decision.ID); err != nil {
			t.Fatal(err)
		}
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/tasks/", tasks[0].ID), nil)
	if status != http.StatusOK {
		t.Fatalf("task detail status = %d; body=%s", status, body)
	}
	var response struct {
		DecisionHistory []struct {
			DecisionID int64 `json:"decision_id"`
			TaskID     int64 `json:"task_id"`
		} `json:"decision_history"`
		DecisionHistoryOmitted int `json:"decision_history_omitted"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode task detail: %v; body=%s", err, body)
	}
	if len(response.DecisionHistory) != 1 || response.DecisionHistory[0].DecisionID != target.ID || response.DecisionHistory[0].TaskID != tasks[0].ID {
		t.Fatalf("decision_history = %+v, want only target decision %d", response.DecisionHistory, target.ID)
	}
	if response.DecisionHistoryOmitted != 0 {
		t.Fatalf("decision_history_omitted = %d, want 0", response.DecisionHistoryOmitted)
	}
}

func TestHTTPTaskDetailReportsOmittedHistoryPerTask(t *testing.T) {
	f := newBareFixture(t)
	tasks, err := f.store.DeclareTasks(
		f.ctx,
		f.goal.ID,
		"fixture-agent",
		"task-history-omitted-declare",
		[]string{"Target task", "Other task"},
		[]string{"The task whose history is requested.", "A different task in the same goal."},
	)
	if err != nil {
		t.Fatal(err)
	}

	createApplied := func(taskID int64, sessionID string) domain.Decision {
		decision, err := f.store.AskDecision(f.ctx, store.AskInput{
			GoalID:         f.goal.ID,
			TaskID:         taskID,
			Kind:           domain.DecisionKind("question"),
			Question:       "Task history",
			AgentSessionID: testSessionID(sessionID),
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.AnswerDecision(f.ctx, store.AnswerInput{
			DecisionID:  decision.ID,
			AnswerLabel: "Answer",
			AnswerText:  "The answer.",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.PollDecisions(f.ctx, testSessionID(sessionID), decision.ID); err != nil {
			t.Fatal(err)
		}
		return decision
	}

	for i := 0; i < 3; i++ {
		createApplied(tasks[1].ID, "other-history-run")
	}
	for i := 0; i < 23; i++ {
		createApplied(tasks[0].ID, "target-history-run")
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/tasks/", tasks[0].ID), nil)
	if status != http.StatusOK {
		t.Fatalf("task detail status = %d; body=%s", status, body)
	}
	var response struct {
		DecisionHistory []struct {
			TaskID int64 `json:"task_id"`
		} `json:"decision_history"`
		DecisionHistoryOmitted int `json:"decision_history_omitted"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode task detail: %v; body=%s", err, body)
	}
	if len(response.DecisionHistory) != 20 {
		t.Fatalf("decision_history length = %d, want 20", len(response.DecisionHistory))
	}
	for _, history := range response.DecisionHistory {
		if history.TaskID != tasks[0].ID {
			t.Fatalf("history task_id = %d, want %d", history.TaskID, tasks[0].ID)
		}
	}
	if response.DecisionHistoryOmitted != 3 {
		t.Fatalf("decision_history_omitted = %d, want 3", response.DecisionHistoryOmitted)
	}
}

func TestHTTPTaskDetailExcludesOtherProjectDecisionWithSameTaskID(t *testing.T) {
	f := newBareFixture(t)
	tasks, err := f.store.DeclareTasks(
		f.ctx,
		f.goal.ID,
		"fixture-agent",
		"task-history-project-declare",
		[]string{"Target task"},
		[]string{"The task whose history is requested."},
	)
	if err != nil {
		t.Fatal(err)
	}

	target, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:         f.goal.ID,
		TaskID:         tasks[0].ID,
		Kind:           domain.DecisionKind("question"),
		Question:       "Target project history",
		AgentSessionID: testSessionID("target-project-history-run"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.AnswerDecision(f.ctx, store.AnswerInput{
		DecisionID:  target.ID,
		AnswerLabel: "Target answer",
		AnswerText:  "The target answer.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.PollDecisions(f.ctx, testSessionID("target-project-history-run"), target.ID); err != nil {
		t.Fatal(err)
	}

	otherProject, err := f.store.CreateProject(f.ctx, "other", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	otherGoal, err := f.store.CreateGoal(f.ctx, otherProject.ID, "Other project goal", "human")
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:         otherGoal.ID,
		TaskID:         tasks[0].ID,
		Kind:           domain.DecisionKind("question"),
		Question:       "Orphaned other project history",
		AgentSessionID: testSessionID("other-project-history-run"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.AnswerDecision(f.ctx, store.AnswerInput{
		DecisionID:  other.ID,
		AnswerLabel: "Other answer",
		AnswerText:  "The other project answer.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.PollDecisions(f.ctx, testSessionID("other-project-history-run"), other.ID); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/tasks/", tasks[0].ID), nil)
	if status != http.StatusOK {
		t.Fatalf("task detail status = %d; body=%s", status, body)
	}
	var response struct {
		DecisionHistory []struct {
			DecisionID int64 `json:"decision_id"`
		} `json:"decision_history"`
		DecisionHistoryOmitted int `json:"decision_history_omitted"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode task detail: %v; body=%s", err, body)
	}
	if len(response.DecisionHistory) != 1 || response.DecisionHistory[0].DecisionID != target.ID {
		t.Fatalf("decision_history = %+v, want only target decision %d", response.DecisionHistory, target.ID)
	}
	if response.DecisionHistoryOmitted != 0 {
		t.Fatalf("decision_history_omitted = %d, want 0", response.DecisionHistoryOmitted)
	}
}

func TestHTTPTaskDetailReturnsNotFoundForUnknownTask(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()

	status, headers, body := doRequest(t, srv.Client(), http.MethodGet, srv.URL+"/api/tasks/missing-task", nil)
	assertErrorObject(t, status, headers, body, http.StatusNotFound)
}

func TestHTTPTaskDetailReturnsEmptyCommitsArray(t *testing.T) {
	f := newBareFixture(t)
	tasks, err := f.store.DeclareTasks(
		f.ctx,
		f.goal.ID,
		"fixture-agent",
		"task-commits-empty",
		[]string{"Task without commits"},
		[]string{"A task without linked commits."},
	)
	if err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/tasks/", tasks[0].ID), nil)
	if status != http.StatusOK {
		t.Fatalf("task detail status = %d; body=%s", status, body)
	}

	var response struct {
		Commits []json.RawMessage `json:"commits"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode task detail: %v; body=%s", err, body)
	}
	if response.Commits == nil {
		t.Fatal("commits is null, want []")
	}
	if len(response.Commits) != 0 {
		t.Fatalf("commits = %s, want []", body)
	}
}

func TestHTTPTaskDetailMarksMissingCommitOutOfHistory(t *testing.T) {
	f := newBareFixture(t)
	tasks, err := f.store.DeclareTasks(
		f.ctx,
		f.goal.ID,
		"fixture-agent",
		"task-commits-missing",
		[]string{"Task with missing commit"},
		[]string{"A task with a commit no longer in history."},
	)
	if err != nil {
		t.Fatal(err)
	}
	sha := "0000000000000000000000000000000000000000"
	if err := f.store.LinkTaskCommit(f.ctx, tasks[0].ID, domain.TaskCommit{
		SHA:          sha,
		Subject:      "subject saved before rebase",
		FilesChanged: 3,
		Insertions:   5,
		Deletions:    2,
	}); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/tasks/", tasks[0].ID), nil)
	if status != http.StatusOK {
		t.Fatalf("task detail status = %d; body=%s", status, body)
	}

	var response struct {
		Commits []struct {
			SHA          string `json:"sha"`
			ShortSHA     string `json:"short_sha"`
			Subject      string `json:"subject"`
			FilesChanged int    `json:"files_changed"`
			Insertions   int    `json:"insertions"`
			Deletions    int    `json:"deletions"`
			InHistory    bool   `json:"in_history"`
		} `json:"commits"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode task detail: %v; body=%s", err, body)
	}
	if len(response.Commits) != 1 {
		t.Fatalf("commits = %+v, want one commit", response.Commits)
	}
	commit := response.Commits[0]
	if commit.SHA != sha || commit.ShortSHA != sha[:7] || commit.Subject != "subject saved before rebase" || commit.FilesChanged != 3 || commit.Insertions != 5 || commit.Deletions != 2 || commit.InHistory {
		t.Fatalf("commit = %+v, want saved data with in_history=false", commit)
	}
}

func TestHTTPTaskDetailDoesNotMixCommitsFromOtherTasks(t *testing.T) {
	f := newBareFixture(t)
	tasks, err := f.store.DeclareTasks(
		f.ctx,
		f.goal.ID,
		"fixture-agent",
		"task-commits-isolated",
		[]string{"Target task", "Other task"},
		[]string{"The task being requested.", "A different task."},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.LinkTaskCommit(f.ctx, tasks[1].ID, domain.TaskCommit{
		SHA:     "1111111111111111111111111111111111111111",
		Subject: "other task commit",
	}); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/tasks/", tasks[0].ID), nil)
	if status != http.StatusOK {
		t.Fatalf("task detail status = %d; body=%s", status, body)
	}

	var response struct {
		Commits []json.RawMessage `json:"commits"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode task detail: %v; body=%s", err, body)
	}
	if response.Commits == nil {
		t.Fatal("commits is null, want []")
	}
	if len(response.Commits) != 0 {
		t.Fatalf("commits = %s, want target task to have no commits", body)
	}
}

func TestHTTPGoalDetailIncludesTasklessOpenDecision(t *testing.T) {
	f := newBareFixture(t)
	decision, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:         f.goal.ID,
		Kind:           domain.DecisionKind("question"),
		Question:       "Which direction should we take?",
		AgentSessionID: testSessionID("taskless-run"),
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
		GoalID:         f.goal.ID,
		Kind:           domain.DecisionKind("question"),
		Question:       "A decision without a task",
		AgentSessionID: testSessionID("taskless-run"),
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
	}, testSessionID("completion-run"))
	if err != nil {
		t.Fatal(err)
	}

	detail := fetchGoalDetail(t, f)
	if len(detail.UnattachedDecisions) != 1 {
		t.Fatalf("unattached_decisions = %+v", detail.UnattachedDecisions)
	}
	got := detail.UnattachedDecisions[0]
	if got.ID != decision.ID || got.Kind != domain.DecisionKind("completion") || got.TaskID != 0 {
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
	}, testSessionID("completion-run")); err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/goals/", f.goal.ID), nil)
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
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/goals/", f.goal.ID), nil)
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

	status, headers, body := doRequest(t, client, http.MethodPost, urlID(srv.URL+"/api/decisions/", f.open.ID)+"/answer", mustJSON(t, map[string]string{}))
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

	status, headers, body = doRequest(t, client, http.MethodPost, urlID(srv.URL+"/api/decisions/", f.open.ID)+"/answer", mustJSON(t, map[string]string{"answer_text": "yes"}))
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
	status, headers, body = doRequest(t, client, http.MethodPost, urlID(srv.URL+"/api/decisions/", f.open.ID)+"/answer", mustJSON(t, map[string]string{"answer_text": "again"}))
	assertErrorObject(t, status, headers, body, http.StatusConflict)

	status, _, body = doRequest(t, client, http.MethodPost, urlID(srv.URL+"/api/tasks/", f.tasks[0].ID)+"/release", nil)
	if status != http.StatusOK {
		t.Fatalf("release status = %d; body=%s", status, body)
	}
	var released httpapi.TaskView
	if err := json.Unmarshal(body, &released); err != nil {
		t.Fatal(err)
	}
	if released.ClaimedBy != 0 || released.ClaimedAt != nil {
		t.Fatalf("released task = %+v", released)
	}
	status, headers, body = doRequest(t, client, http.MethodPost, srv.URL+"/api/tasks/missing/release", nil)
	assertErrorObject(t, status, headers, body, http.StatusNotFound)

	status, headers, body = doRequest(t, client, http.MethodGet, urlID(srv.URL+"/api/goals/", f.goal.ID)+"/extra", nil)
	assertErrorObject(t, status, headers, body, http.StatusBadRequest)
}

func TestHTTPSnoozeSetsAbsoluteDeadlineWithoutChangingStatus(t *testing.T) {
	f := newFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()

	const wantSnoozedUntil = "2026-09-01T00:00:00Z"
	status, _, body := doRequest(t, srv.Client(), http.MethodPost,
		urlID(srv.URL+"/api/tasks/", f.tasks[0].ID)+"/snooze",
		mustJSON(t, map[string]string{"snoozed_until": wantSnoozedUntil}))
	if status != http.StatusOK {
		t.Fatalf("snooze status = %d; body=%s", status, body)
	}

	var response struct {
		Status       string  `json:"status"`
		SnoozedUntil *string `json:"snoozed_until"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode snooze response: %v; body=%s", err, body)
	}
	if response.SnoozedUntil == nil || *response.SnoozedUntil != wantSnoozedUntil {
		t.Fatalf("snoozed_until = %v, want %q", response.SnoozedUntil, wantSnoozedUntil)
	}
	if response.Status != "todo" {
		t.Fatalf("status = %q, want todo", response.Status)
	}
}

func TestHTTPSnoozeNullClearsAbsoluteDeadline(t *testing.T) {
	f := newFixture(t)
	until, err := time.Parse(time.RFC3339, "2026-09-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SnoozeTask(f.ctx, f.tasks[0].ID, &until); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, f.store)
	defer srv.Close()

	status, _, body := doRequest(t, srv.Client(), http.MethodPost,
		urlID(srv.URL+"/api/tasks/", f.tasks[0].ID)+"/snooze",
		mustJSON(t, map[string]any{"snoozed_until": nil}))
	if status != http.StatusOK {
		t.Fatalf("clear snooze status = %d; body=%s", status, body)
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode clear snooze response: %v; body=%s", err, body)
	}
	if _, ok := response["snoozed_until"]; ok {
		t.Fatalf("snoozed_until is present after clearing: %s", body)
	}
}

func TestHTTPSnoozeEmptyBodyClearsAbsoluteDeadline(t *testing.T) {
	f := newFixture(t)
	until, err := time.Parse(time.RFC3339, "2026-09-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.SnoozeTask(f.ctx, f.tasks[0].ID, &until); err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, f.store)
	defer srv.Close()

	status, _, body := doRequest(t, srv.Client(), http.MethodPost,
		urlID(srv.URL+"/api/tasks/", f.tasks[0].ID)+"/snooze", nil)
	if status != http.StatusOK {
		t.Fatalf("clear snooze with empty body status = %d; body=%s", status, body)
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode empty-body snooze response: %v; body=%s", err, body)
	}
	if _, ok := response["snoozed_until"]; ok {
		t.Fatalf("snoozed_until is present after empty-body clear: %s", body)
	}
}

func TestHTTPSnoozeRejectsInvalidRFC3339(t *testing.T) {
	f := newFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()

	status, headers, body := doRequest(t, srv.Client(), http.MethodPost,
		urlID(srv.URL+"/api/tasks/", f.tasks[0].ID)+"/snooze",
		mustJSON(t, map[string]string{"snoozed_until": "tomorrow"}))
	assertErrorObject(t, status, headers, body, http.StatusBadRequest)
}

func TestHTTPSnoozeReturnsNotFoundForUnknownTask(t *testing.T) {
	f := newFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()

	status, headers, body := doRequest(t, srv.Client(), http.MethodPost, srv.URL+"/api/tasks/missing/snooze", nil)
	assertErrorObject(t, status, headers, body, http.StatusNotFound)
}

func TestHTTPSnoozePreservesReleaseEndpoint(t *testing.T) {
	f := newFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()

	status, _, body := doRequest(t, srv.Client(), http.MethodPost,
		urlID(srv.URL+"/api/tasks/", f.tasks[0].ID)+"/release", nil)
	if status != http.StatusOK {
		t.Fatalf("release status = %d; body=%s", status, body)
	}
	var released httpapi.TaskView
	if err := json.Unmarshal(body, &released); err != nil {
		t.Fatal(err)
	}
	if released.ClaimedBy != 0 || released.ClaimedAt != nil {
		t.Fatalf("released task = %+v", released)
	}
}

func TestHTTPSnoozePreservesTaskGetEndpoint(t *testing.T) {
	f := newFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()

	status, _, body := doRequest(t, srv.Client(), http.MethodGet,
		urlID(srv.URL+"/api/tasks/", f.tasks[0].ID), nil)
	if status != http.StatusOK {
		t.Fatalf("task GET status = %d; body=%s", status, body)
	}
	var detail struct {
		Task httpapi.TaskView `json:"task"`
	}
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Task.ID != f.tasks[0].ID {
		t.Fatalf("task id = %d, want %d", detail.Task.ID, f.tasks[0].ID)
	}
}

func TestHTTPSnoozeRejectsGetMethod(t *testing.T) {
	f := newFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()

	status, headers, body := doRequest(t, srv.Client(), http.MethodGet,
		urlID(srv.URL+"/api/tasks/", f.tasks[0].ID)+"/snooze", nil)
	assertErrorObject(t, status, headers, body, http.StatusBadRequest)
}

func TestHTTPSnoozeRejectsEmptyTaskID(t *testing.T) {
	f := newFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()

	status, headers, body := doRequest(t, srv.Client(), http.MethodPost, srv.URL+"/api/tasks//snooze", nil)
	assertErrorObject(t, status, headers, body, http.StatusBadRequest)
}

func TestHTTPSnoozeControlsWakeupForSnoozedAndUnsnoozedTasks(t *testing.T) {
	f := newBareFixture(t)
	tasks := declareWakeupTestTasks(t, f)
	srv := newTestServer(t, f.store)
	defer srv.Close()

	future := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	status, _, body := doRequest(t, srv.Client(), http.MethodPost,
		urlID(srv.URL+"/api/tasks/", tasks[0].ID)+"/snooze",
		mustJSON(t, map[string]string{"snoozed_until": future}))
	if status != http.StatusOK {
		t.Fatalf("snooze status = %d; body=%s", status, body)
	}

	state, err := f.store.DetectWakeup(f.ctx, f.project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if state.UnstartedTaskCount != 1 || len(state.Tasks) != 1 {
		t.Fatalf("wakeup state = %+v, want one unstarted task", state)
	}
	seen := make(map[int64]bool, len(state.Tasks))
	for _, task := range state.Tasks {
		seen[task.ID] = true
	}
	if seen[tasks[0].ID] {
		t.Fatalf("future-snoozed task %d was included in wakeup tasks", tasks[0].ID)
	}
	if !seen[tasks[1].ID] {
		t.Fatalf("unsnoozed task %d was omitted from wakeup tasks", tasks[1].ID)
	}

	counted, err := f.store.CountUnstartedTasks(f.ctx, f.project.ID)
	if err != nil {
		t.Fatalf("CountUnstartedTasks: %v", err)
	}
	if counted != 1 {
		t.Fatalf("counted unstarted tasks = %d, want 1", counted)
	}
}

func TestHTTPSnoozeClearingDeadlineRestoresWakeup(t *testing.T) {
	f := newBareFixture(t)
	tasks := declareWakeupTestTasks(t, f)
	srv := newTestServer(t, f.store)
	defer srv.Close()

	future := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	status, _, body := doRequest(t, srv.Client(), http.MethodPost,
		urlID(srv.URL+"/api/tasks/", tasks[0].ID)+"/snooze",
		mustJSON(t, map[string]string{"snoozed_until": future}))
	if status != http.StatusOK {
		t.Fatalf("snooze status = %d; body=%s", status, body)
	}
	status, _, body = doRequest(t, srv.Client(), http.MethodPost,
		urlID(srv.URL+"/api/tasks/", tasks[0].ID)+"/snooze",
		mustJSON(t, map[string]any{"snoozed_until": nil}))
	if status != http.StatusOK {
		t.Fatalf("clear snooze status = %d; body=%s", status, body)
	}

	state, err := f.store.DetectWakeup(f.ctx, f.project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if len(state.Tasks) != 2 || state.UnstartedTaskCount != 2 {
		t.Fatalf("wakeup state after clear = %+v, want two unstarted tasks", state)
	}
	counted, err := f.store.CountUnstartedTasks(f.ctx, f.project.ID)
	if err != nil {
		t.Fatalf("CountUnstartedTasks: %v", err)
	}
	if counted != 2 {
		t.Fatalf("counted unstarted tasks after clear = %d, want 2", counted)
	}
}

func TestHTTPSnoozeExpiredDeadlineRestoresWakeup(t *testing.T) {
	f := newBareFixture(t)
	tasks := declareWakeupTestTasks(t, f)
	srv := newTestServer(t, f.store)
	defer srv.Close()

	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	status, _, body := doRequest(t, srv.Client(), http.MethodPost,
		urlID(srv.URL+"/api/tasks/", tasks[0].ID)+"/snooze",
		mustJSON(t, map[string]string{"snoozed_until": past}))
	if status != http.StatusOK {
		t.Fatalf("expired snooze status = %d; body=%s", status, body)
	}

	state, err := f.store.DetectWakeup(f.ctx, f.project.ID)
	if err != nil {
		t.Fatalf("DetectWakeup: %v", err)
	}
	if state.UnstartedTaskCount != 2 || len(state.Tasks) != 2 {
		t.Fatalf("wakeup state after expiry = %+v, want two unstarted tasks", state)
	}
	seen := make(map[int64]bool, len(state.Tasks))
	for _, task := range state.Tasks {
		seen[task.ID] = true
	}
	if !seen[tasks[0].ID] {
		t.Fatalf("expired task %d was omitted from wakeup tasks", tasks[0].ID)
	}

	counted, err := f.store.CountUnstartedTasks(f.ctx, f.project.ID)
	if err != nil {
		t.Fatalf("CountUnstartedTasks: %v", err)
	}
	if counted != 2 {
		t.Fatalf("counted unstarted tasks after expiry = %d, want 2", counted)
	}
}

func declareWakeupTestTasks(t *testing.T, f *fixture) []domain.Task {
	t.Helper()
	tasks, err := f.store.DeclareTasks(f.ctx, f.goal.ID, "wakeup-test-agent", "wakeup-http", []string{
		"Deferred task",
		"Actionable task",
	}, []string{
		"Stay deferred until the HTTP deadline.",
		"Remain actionable while the other task is deferred.",
	})
	if err != nil {
		t.Fatal(err)
	}
	return tasks
}

func TestHTTPAnswerRejectsCompletionDecision(t *testing.T) {
	f := newFixture(t)
	completion, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:   f.goal.ID,
		TaskID:   f.tasks[0].ID,
		Kind:     domain.KindCompletion,
		Question: "May this task be completed?",
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, f.store)
	defer srv.Close()

	status, headers, body := doRequest(t, srv.Client(), http.MethodPost, urlID(srv.URL+"/api/decisions/", completion.ID)+"/answer", mustJSON(t, map[string]string{
		"answer_text": "yes",
	}))
	assertErrorObject(t, status, headers, body, http.StatusBadRequest)
	var response map[string]string
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response["error"] != "use approve or reject for this decision" {
		t.Fatalf("unexpected answer guard error = %q", response["error"])
	}
	got, err := f.store.GetDecision(f.ctx, completion.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.DecisionOpen {
		t.Fatalf("completion decision status = %q, want open", got.Status)
	}
}

func TestHTTPAnswerRejectsGoalApprovalDecision(t *testing.T) {
	f := newFixture(t)
	approval, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:   f.goal.ID,
		Kind:     domain.KindGoalApproval,
		Question: "Approve this goal?",
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, f.store)
	defer srv.Close()

	status, headers, body := doRequest(t, srv.Client(), http.MethodPost, urlID(srv.URL+"/api/decisions/", approval.ID)+"/answer", mustJSON(t, map[string]string{
		"answer_label": "maybe",
	}))
	assertErrorObject(t, status, headers, body, http.StatusBadRequest)
	var response map[string]string
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response["error"] != "use approve or reject for this decision" {
		t.Fatalf("unexpected answer guard error = %q", response["error"])
	}
	got, err := f.store.GetDecision(f.ctx, approval.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.DecisionOpen {
		t.Fatalf("goal approval decision status = %q, want open", got.Status)
	}
}

func TestHTTPAnswerAllowsDecisionKind(t *testing.T) {
	f := newFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()

	status, headers, body := doRequest(t, srv.Client(), http.MethodPost, urlID(srv.URL+"/api/decisions/", f.open.ID)+"/answer", mustJSON(t, map[string]string{
		"answer_text": "yes",
	}))
	if status != http.StatusOK {
		t.Fatalf("answer status = %d; body=%s", status, body)
	}
	if headers.Get("Content-Type") == "" {
		t.Fatal("answer response has no content type")
	}
	got, err := f.store.GetDecision(f.ctx, f.open.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.DecisionStatus("answered") || got.AnswerText != "yes" {
		t.Fatalf("answered decision = %+v", got)
	}
}

func TestHTTPApproveAndRejectCompletionEndpoints(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	client := srv.Client()

	approveGoal, err := f.store.CreateGoal(f.ctx, f.project.ID, "Approve me", "human")
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
	}, testSessionID("approve-run"))
	if err != nil {
		t.Fatal(err)
	}
	status, headers, body := doRequest(t, client, http.MethodPost, urlID(srv.URL+"/api/decisions/", approveDecision.ID)+"/approve", mustJSON(t, map[string]string{}))
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

	rejectGoal, err := f.store.CreateGoal(f.ctx, f.project.ID, "Reject me", "human")
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
	}, testSessionID("reject-run"))
	if err != nil {
		t.Fatal(err)
	}
	status, _, body = doRequest(t, client, http.MethodPost, urlID(srv.URL+"/api/decisions/", rejectDecision.ID)+"/reject", mustJSON(t, map[string]string{"reason": "needs work"}))
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
	status, headers, body = doRequest(t, client, http.MethodPost, urlID(srv.URL+"/api/decisions/", rejectDecision.ID)+"/reject", mustJSON(t, map[string]string{}))
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

func eventsURLWithGoal(baseURL, goalID string) string {
	query := url.Values{}
	query.Set("goal_id", goalID)
	return baseURL + "/api/events?" + query.Encode()
}

func urlID(prefix string, id int64) string { return prefix + strconv.FormatInt(id, 10) }

func idText(id int64) string { return strconv.FormatInt(id, 10) }

type wsTestFrame struct {
	Name string          `json:"name"`
	Data json.RawMessage `json:"data"`
}

func openWebSocket(t *testing.T, endpoint string, options *websocket.DialOptions) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, endpoint, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	return conn
}

func readWebSocketFrame(t *testing.T, conn *websocket.Conn) wsTestFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	typ, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("WebSocket message type = %v, want text", typ)
	}
	var frame wsTestFrame
	if err := json.Unmarshal(payload, &frame); err != nil {
		t.Fatalf("WebSocket frame is not JSON: %v; payload=%q", err, payload)
	}
	return frame
}

func websocketURL(baseURL string) string {
	return strings.Replace(baseURL, "http://", "ws://", 1) + "/api/ws"
}

func websocketURLWithQuery(baseURL string, query url.Values) string {
	return websocketURL(baseURL) + "?" + query.Encode()
}

func TestSSEFiltersDecisionEventsByProjectID(t *testing.T) {
	f := newBareFixture(t)
	otherProject, err := f.store.CreateProject(f.ctx, "other", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	currentGoal, err := f.store.CreateGoal(f.ctx, f.project.ID, "Current project", "human")
	if err != nil {
		t.Fatal(err)
	}
	otherGoal, err := f.store.CreateGoal(f.ctx, otherProject.ID, "Other project", "human")
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, f.store)
	defer srv.Close()
	streamCtx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	stream, reader := openSSEStream(t, streamCtx, srv.Client(), eventsURL(srv.URL, idText(f.project.ID)))
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

func TestSSEFiltersDetectionEventsByProjectID(t *testing.T) {
	f := newBareFixture(t)
	otherProject, err := f.store.CreateProject(f.ctx, "other", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, f.store)
	defer srv.Close()
	streamCtx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	stream, reader := openSSEStream(t, streamCtx, srv.Client(), eventsURL(srv.URL, idText(f.project.ID)))
	defer stream.Body.Close()

	other := &store.DetectionEvent{DetectionID: "other-detection", ProjectID: otherProject.ID, GoalID: 2}
	current := store.DetectionEvent{DetectionID: "current-detection", ProjectID: f.project.ID, GoalID: 1}
	f.store.PublishEvent(store.DecisionEvent{Name: store.EventDetectionCompletionReportMissing, Data: other})
	f.store.PublishEvent(store.DecisionEvent{Name: store.EventDetectionCompletionReportMissing, Data: current})

	frame := readSSEFrame(t, reader)
	if frame.event != store.EventDetectionCompletionReportMissing {
		t.Fatalf("SSE detection event = %q, want %q; lines=%v", frame.event, store.EventDetectionCompletionReportMissing, frame.lines)
	}
	var got store.DetectionEvent
	if err := json.Unmarshal([]byte(frame.data), &got); err != nil {
		t.Fatalf("SSE detection data is not a DetectionEvent: %v; data=%q", err, frame.data)
	}
	if got.DetectionID != current.DetectionID {
		t.Fatalf("SSE detection id = %q, want %q", got.DetectionID, current.DetectionID)
	}
}

func TestSSEFiltersDetectionEventsByGoalID(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	streamCtx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	stream, reader := openSSEStream(t, streamCtx, srv.Client(), eventsURLWithGoal(srv.URL, "1"))
	defer stream.Body.Close()

	f.store.PublishEvent(store.DecisionEvent{
		Name: store.EventDetectionCompletionReportMissing,
		Data: store.DetectionEvent{DetectionID: "other-detection", GoalID: 2},
	})
	f.store.PublishEvent(store.DecisionEvent{
		Name: store.EventDetectionCompletionReportMissing,
		Data: store.DetectionEvent{DetectionID: "goal-less-detection"},
	})
	f.store.PublishEvent(store.DecisionEvent{
		Name: store.EventDetectionCompletionReportMissing,
		Data: store.DetectionEvent{DetectionID: "current-detection", GoalID: 1},
	})

	frame := readSSEFrame(t, reader)
	if frame.event != store.EventDetectionCompletionReportMissing {
		t.Fatalf("SSE detection event = %q, want %q; lines=%v", frame.event, store.EventDetectionCompletionReportMissing, frame.lines)
	}
	var got store.DetectionEvent
	if err := json.Unmarshal([]byte(frame.data), &got); err != nil {
		t.Fatalf("SSE detection data is not a DetectionEvent: %v; data=%q", err, frame.data)
	}
	if got.DetectionID != "current-detection" || got.GoalID != 1 {
		t.Fatalf("SSE detection = %+v, want current goal detection", got)
	}
}

func TestSSEPublishesEvaluateFailureForGoalSubscription(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	streamCtx, cancel := context.WithTimeout(f.ctx, time.Second)
	defer cancel()
	stream, reader := openSSEStream(t, streamCtx, srv.Client(), eventsURLWithGoal(srv.URL, "1"))
	defer stream.Body.Close()

	want := store.WakeupEvaluateFailedEvent{WakeupID: "failure-1", Reason: "database unavailable"}
	f.store.PublishEvent(store.DecisionEvent{Name: store.EventWakeupEvaluateFailed, Data: want})

	frame := readSSEFrame(t, reader)
	if frame.event != store.EventWakeupEvaluateFailed {
		t.Fatalf("SSE evaluate failure event = %q, want %q", frame.event, store.EventWakeupEvaluateFailed)
	}
	var got store.WakeupEvaluateFailedEvent
	if err := json.Unmarshal([]byte(frame.data), &got); err != nil {
		t.Fatalf("SSE evaluate failure data is not WakeupEvaluateFailedEvent: %v; data=%q", err, frame.data)
	}
	if got != want {
		t.Fatalf("SSE evaluate failure = %+v, want %+v", got, want)
	}
}

func TestSSEPublishesDecisionEventsForGoalID(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	streamCtx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	stream, reader := openSSEStream(t, streamCtx, srv.Client(), eventsURLWithGoal(srv.URL, "1"))
	defer stream.Body.Close()

	current := &domain.Decision{ID: 1, GoalID: 1}
	f.store.PublishEvent(store.DecisionEvent{Name: "decision.created", Data: current})

	frame := readSSEFrame(t, reader)
	if frame.event != "decision.created" {
		t.Fatalf("SSE decision event = %q, want %q; lines=%v", frame.event, "decision.created", frame.lines)
	}
	var got domain.Decision
	if err := json.Unmarshal([]byte(frame.data), &got); err != nil {
		t.Fatalf("SSE decision data is not a Decision: %v; data=%q", err, frame.data)
	}
	if got.ID != current.ID || got.GoalID != current.GoalID {
		t.Fatalf("SSE decision = %+v, want current goal decision", got)
	}
}

func TestSSEFiltersOtherGoalDecisionEventsByGoalID(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	streamCtx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	stream, reader := openSSEStream(t, streamCtx, srv.Client(), eventsURLWithGoal(srv.URL, "1"))
	defer stream.Body.Close()

	other := domain.Decision{ID: 2, GoalID: 2}
	current := domain.Decision{ID: 1, GoalID: 1}
	f.store.PublishEvent(store.DecisionEvent{Name: "decision.created", Data: other})
	f.store.PublishEvent(store.DecisionEvent{Name: "decision.created", Data: current})

	frame := readSSEFrame(t, reader)
	if frame.event != "decision.created" {
		t.Fatalf("SSE decision event = %q, want %q; lines=%v", frame.event, "decision.created", frame.lines)
	}
	var got domain.Decision
	if err := json.Unmarshal([]byte(frame.data), &got); err != nil {
		t.Fatalf("SSE decision data is not a Decision: %v; data=%q", err, frame.data)
	}
	if got.ID != current.ID || got.GoalID != current.GoalID {
		t.Fatalf("SSE decision = %+v, want current goal decision", got)
	}
}

func TestWebSocketPublishesDecisionCreated(t *testing.T) {
	f := newBareFixture(t)
	goal, err := f.store.CreateGoal(f.ctx, f.project.ID, "WebSocket goal", "human")
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, f.store)
	defer srv.Close()
	conn := openWebSocket(t, websocketURL(srv.URL), nil)

	decision, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:   goal.ID,
		Kind:     domain.DecisionKind("question"),
		Question: "WebSocket decision",
	})
	if err != nil {
		t.Fatal(err)
	}
	frame := readWebSocketFrame(t, conn)
	if frame.Name != "decision.created" {
		t.Fatalf("WebSocket event = %q, want %q", frame.Name, "decision.created")
	}
	if got, want := string(frame.Data), string(mustJSON(t, decision)); got != want {
		t.Fatalf("WebSocket data = %s, want exact %s", got, want)
	}
}

func TestWebSocketPublishesKeepalive(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	conn := openWebSocket(t, websocketURL(srv.URL), nil)

	keepalive := store.KeepaliveEvent{At: time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC)}
	f.store.PublishEvent(store.DecisionEvent{Name: store.EventKeepalive, Data: keepalive})

	frame := readWebSocketFrame(t, conn)
	if frame.Name != store.EventKeepalive {
		t.Fatalf("WebSocket event = %q, want %q", frame.Name, store.EventKeepalive)
	}
	if got, want := string(frame.Data), string(mustJSON(t, keepalive)); got != want {
		t.Fatalf("WebSocket data = %s, want exact %s", got, want)
	}
}

func TestWebSocketFiltersByGoalID(t *testing.T) {
	f := newBareFixture(t)
	targetGoal, err := f.store.CreateGoal(f.ctx, f.project.ID, "Target goal", "human")
	if err != nil {
		t.Fatal(err)
	}
	otherGoal, err := f.store.CreateGoal(f.ctx, f.project.ID, "Other goal", "human")
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, f.store)
	defer srv.Close()
	query := url.Values{}
	query.Set("goal_id", idText(targetGoal.ID))
	conn := openWebSocket(t, websocketURLWithQuery(srv.URL, query), nil)

	other := store.DetectionEvent{DetectionID: "other-goal", GoalID: otherGoal.ID}
	target := store.DetectionEvent{DetectionID: "target-goal", GoalID: targetGoal.ID}
	f.store.PublishEvent(store.DecisionEvent{Name: store.EventDetectionCompletionReportMissing, Data: other})
	f.store.PublishEvent(store.DecisionEvent{Name: store.EventDetectionCompletionReportMissing, Data: target})

	frame := readWebSocketFrame(t, conn)
	if frame.Name != store.EventDetectionCompletionReportMissing {
		t.Fatalf("WebSocket event = %q, want %q", frame.Name, store.EventDetectionCompletionReportMissing)
	}
	if got, want := string(frame.Data), string(mustJSON(t, target)); got != want {
		t.Fatalf("WebSocket data = %s, want target %s", got, want)
	}
}

func TestWebSocketFiltersByProjectID(t *testing.T) {
	f := newBareFixture(t)
	otherProject, err := f.store.CreateProject(f.ctx, "other", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, f.store)
	defer srv.Close()
	query := url.Values{}
	query.Set("project_id", idText(f.project.ID))
	conn := openWebSocket(t, websocketURLWithQuery(srv.URL, query), nil)

	other := store.DetectionEvent{DetectionID: "other-project", ProjectID: otherProject.ID}
	target := store.DetectionEvent{DetectionID: "target-project", ProjectID: f.project.ID}
	f.store.PublishEvent(store.DecisionEvent{Name: store.EventDetectionCompletionReportMissing, Data: other})
	f.store.PublishEvent(store.DecisionEvent{Name: store.EventDetectionCompletionReportMissing, Data: target})

	frame := readWebSocketFrame(t, conn)
	if frame.Name != store.EventDetectionCompletionReportMissing {
		t.Fatalf("WebSocket event = %q, want %q", frame.Name, store.EventDetectionCompletionReportMissing)
	}
	if got, want := string(frame.Data), string(mustJSON(t, target)); got != want {
		t.Fatalf("WebSocket data = %s, want target %s", got, want)
	}
}

func TestWebSocketInvalidGoalIDReturnsHTTPError(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	// resolveID accepts any positive integer without checking that the row
	// exists, so a bogus number is a valid filter that simply matches nothing.
	// An unparseable id is the value that actually reaches the error path.
	query := url.Values{}
	query.Set("goal_id", "not-a-number")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, websocketURLWithQuery(srv.URL, query), nil)
	if err == nil {
		t.Fatal("WebSocket dial unexpectedly succeeded")
	}
	if resp == nil {
		t.Fatal("WebSocket dial returned no HTTP response")
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusBadRequest {
		t.Fatalf("invalid goal status = %d, want HTTP error", resp.StatusCode)
	}
}

func TestWebSocketRejectsPost(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	status, _, _ := doRequest(t, srv.Client(), http.MethodPost, srv.URL+"/api/ws", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("POST /api/ws status = %d, want %d", status, http.StatusBadRequest)
	}
}

func TestWebSocketRejectsMismatchedOrigin(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	options := &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"http://evil.example"}},
	}
	_, resp, err := websocket.Dial(ctx, websocketURL(srv.URL), options)
	if err == nil {
		t.Fatal("WebSocket dial with mismatched Origin unexpectedly succeeded")
	}
	if resp == nil {
		t.Fatal("mismatched Origin returned no HTTP response")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusSwitchingProtocols {
		t.Fatalf("mismatched Origin status = %d, must not upgrade", resp.StatusCode)
	}
}

func TestWebSocketAndSSEReceiveSameEvent(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	// CreateGoal publishes goal.created, so create it before subscribing or
	// both streams see that event ahead of the decision this test asserts on.
	goal, err := f.store.CreateGoal(f.ctx, f.project.ID, "Shared event goal", "human")
	if err != nil {
		t.Fatal(err)
	}
	streamCtx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	stream, reader := openSSEStream(t, streamCtx, srv.Client(), srv.URL+"/api/events")
	defer stream.Body.Close()
	conn := openWebSocket(t, websocketURL(srv.URL), nil)

	decision, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:   goal.ID,
		Kind:     domain.DecisionKind("question"),
		Question: "Shared event",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSSEDecision(t, reader, "decision.created", decision)
	frame := readWebSocketFrame(t, conn)
	if frame.Name != "decision.created" {
		t.Fatalf("WebSocket event = %q, want %q", frame.Name, "decision.created")
	}
	if got, want := string(frame.Data), string(mustJSON(t, decision)); got != want {
		t.Fatalf("WebSocket data = %s, want exact %s", got, want)
	}
}

func TestSSEGoalIDPublishesKeepaliveButNotWakeup(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	streamCtx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	stream, reader := openSSEStream(t, streamCtx, srv.Client(), eventsURLWithGoal(srv.URL, "1"))
	defer stream.Body.Close()

	f.store.PublishEvent(store.DecisionEvent{
		Name: store.EventWakeup,
		Data: store.WakeupEvent{WakeupID: "wakeup-should-be-filtered", ProjectID: f.project.ID},
	})
	keepalive := store.KeepaliveEvent{At: time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC)}
	f.store.PublishEvent(store.DecisionEvent{Name: store.EventKeepalive, Data: keepalive})

	frame := readSSEFrame(t, reader)
	if frame.event != store.EventKeepalive {
		t.Fatalf("SSE event = %q, want %q; lines=%v", frame.event, store.EventKeepalive, frame.lines)
	}
	if frame.data != string(mustJSON(t, keepalive)) {
		t.Fatalf("SSE keepalive data = %s, want exact %s", frame.data, mustJSON(t, keepalive))
	}
}

func TestSSENoGoalIDKeepsPublishingDetectionEvents(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	streamCtx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	stream, reader := openSSEStream(t, streamCtx, srv.Client(), srv.URL+"/api/events")
	defer stream.Body.Close()

	detection := store.DetectionEvent{DetectionID: "unscoped-detection", GoalID: 2}
	f.store.PublishEvent(store.DecisionEvent{Name: store.EventDetectionCompletionReportMissing, Data: detection})

	frame := readSSEFrame(t, reader)
	if frame.event != store.EventDetectionCompletionReportMissing {
		t.Fatalf("SSE event = %q, want %q; lines=%v", frame.event, store.EventDetectionCompletionReportMissing, frame.lines)
	}
	if frame.data != string(mustJSON(t, detection)) {
		t.Fatalf("SSE detection data = %s, want exact %s", frame.data, mustJSON(t, detection))
	}
}

func TestSSEPublishesGenericWakeupAndKeepaliveEvents(t *testing.T) {
	f := newBareFixture(t)
	otherProject, err := f.store.CreateProject(f.ctx, "other", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(t, f.store)
	defer srv.Close()
	streamCtx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	stream, reader := openSSEStream(t, streamCtx, srv.Client(), eventsURL(srv.URL, idText(f.project.ID)))
	defer stream.Body.Close()

	other := store.WakeupEvent{
		WakeupID:            "other-wakeup",
		ProjectID:           otherProject.ID,
		ActionableGoalCount: 2,
		UnstartedTaskCount:  3,
		WaitingAnswerCount:  1,
	}
	current := store.WakeupEvent{
		WakeupID:            "current-wakeup",
		ProjectID:           f.project.ID,
		ActionableGoalCount: 1,
		UnstartedTaskCount:  2,
		WaitingAnswerCount:  0,
	}
	f.store.PublishEvent(store.DecisionEvent{Name: store.EventWakeup, Data: other})
	f.store.PublishEvent(store.DecisionEvent{Name: store.EventWakeup, Data: current})

	frame := readSSEFrame(t, reader)
	if frame.event != store.EventWakeup {
		t.Fatalf("wakeup SSE event = %q, want %q; lines=%v", frame.event, store.EventWakeup, frame.lines)
	}
	wantWakeupJSON := string(mustJSON(t, current))
	if frame.data != wantWakeupJSON {
		t.Fatalf("wakeup SSE data = %s, want exact %s", frame.data, wantWakeupJSON)
	}

	keepalive := store.KeepaliveEvent{At: time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC)}
	f.store.PublishEvent(store.DecisionEvent{Name: store.EventKeepalive, Data: keepalive})
	frame = readSSEFrame(t, reader)
	if frame.event != store.EventKeepalive {
		t.Fatalf("keepalive SSE event = %q, want %q; lines=%v", frame.event, store.EventKeepalive, frame.lines)
	}
	wantKeepaliveJSON := string(mustJSON(t, keepalive))
	if frame.data != wantKeepaliveJSON {
		t.Fatalf("keepalive SSE data = %s, want exact %s", frame.data, wantKeepaliveJSON)
	}
}

func TestSSEWithoutProjectIDPublishesEventsFromAllProjects(t *testing.T) {
	f := newBareFixture(t)
	otherProject, err := f.store.CreateProject(f.ctx, "other", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	currentGoal, err := f.store.CreateGoal(f.ctx, f.project.ID, "Current project", "human")
	if err != nil {
		t.Fatal(err)
	}
	otherGoal, err := f.store.CreateGoal(f.ctx, otherProject.ID, "Other project", "human")
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
		goal, err := f.store.CreateGoal(f.ctx, f.project.ID, title, "human")
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
		GoalID:         f.goal.ID,
		Kind:           domain.DecisionKind("question"),
		Question:       "Need an answer",
		AgentSessionID: testSessionID("poll-run"),
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

	applied, err := f.store.PollDecisions(f.ctx, testSessionID("poll-run"), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 {
		t.Fatalf("applied decisions = %+v", applied)
	}
	assertSSEDecision(t, reader, "decision.applied", applied[0])

	withdrawn, err := f.store.AskDecision(f.ctx, store.AskInput{
		GoalID:         f.goal.ID,
		Kind:           domain.DecisionKind("question"),
		Question:       "Withdraw me",
		AgentSessionID: testSessionID("withdraw-run"),
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
	}, testSessionID("approve-run"))
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
	}, testSessionID("reject-run"))
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
	status, _, body := doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/goals/", f.goal.ID), nil)
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
		"project_id": idText(f.project.ID),
		"content":    "Created in inbox\n\nCreated through the human UI endpoint",
		"creator":    "human",
	}))
	if status != http.StatusOK {
		t.Fatalf("create goal status = %d; body=%s", status, body)
	}
	var created domain.Goal
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created goal: %v; body=%s", err, body)
	}
	if created.ProjectID != f.project.ID || created.Content != "Created in inbox\n\nCreated through the human UI endpoint" || created.Creator != "human" || created.Status != domain.GoalActive {
		t.Fatalf("created goal = %+v", created)
	}

	status, _, body = doRequest(t, client, http.MethodPost, srv.URL+"/api/goals", mustJSON(t, map[string]string{
		"project_id": idText(f.project.ID),
		"content":    "Created by an agent\n\nNeeds human approval",
	}))
	if status != http.StatusOK {
		t.Fatalf("agent goal status = %d; body=%s", status, body)
	}
	var proposed domain.Goal
	if err := json.Unmarshal(body, &proposed); err != nil {
		t.Fatalf("decode agent goal: %v; body=%s", err, body)
	}
	if proposed.Creator != "agent" || proposed.Status != domain.GoalProposed {
		t.Fatalf("agent goal = %+v, want agent/proposed", proposed)
	}
	decisions, err := f.store.ListOpenDecisions(f.ctx, proposed.ID)
	if err != nil {
		t.Fatalf("ListOpenDecisions: %v", err)
	}
	if len(decisions) != 1 || decisions[0].DefaultOption != "" || decisions[0].DefaultAfterMs != nil {
		t.Fatalf("agent goal decisions = %+v, want one decision without a default", decisions)
	}

	status, headers, body = doRequest(t, client, http.MethodPost, srv.URL+"/api/goals", mustJSON(t, map[string]string{
		"project_id": idText(f.project.ID),
	}))
	assertErrorObject(t, status, headers, body, http.StatusBadRequest)

	status, headers, body = doRequest(t, client, http.MethodPost, srv.URL+"/api/goals", mustJSON(t, map[string]string{
		"project_id": "missing-project",
		"content":    "Unknown project",
	}))
	assertErrorObject(t, status, headers, body, http.StatusNotFound)
}

func TestHTTPGoalCreationDefaultsToProposedWithoutCreator(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()

	status, _, body := doRequest(t, srv.Client(), http.MethodPost, srv.URL+"/api/goals", mustJSON(t, map[string]string{
		"project_id": idText(f.project.ID),
		"content":    "Created without creator",
	}))
	if status != http.StatusOK {
		t.Fatalf("create goal status = %d; body=%s", status, body)
	}
	var created domain.Goal
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created goal: %v; body=%s", err, body)
	}
	if created.Creator != "agent" || created.Status != domain.GoalProposed {
		t.Fatalf("created goal = %+v, want agent/proposed", created)
	}
}

func TestHTTPGoalCreationWithHumanCreatorIsActive(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()

	status, _, body := doRequest(t, srv.Client(), http.MethodPost, srv.URL+"/api/goals", mustJSON(t, map[string]string{
		"project_id": idText(f.project.ID),
		"content":    "Created by a human",
		"creator":    "human",
	}))
	if status != http.StatusOK {
		t.Fatalf("create goal status = %d; body=%s", status, body)
	}
	var created domain.Goal
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created goal: %v; body=%s", err, body)
	}
	if created.Creator != "human" || created.Status != domain.GoalActive {
		t.Fatalf("created goal = %+v, want human/active", created)
	}
}

func TestHTTPGoalApprovalEndpointsTransitionProposedGoal(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()
	client := srv.Client()

	status, _, body := doRequest(t, client, http.MethodPost, srv.URL+"/api/goals", mustJSON(t, map[string]string{
		"project_id": idText(f.project.ID),
		"content":    "Approve through HTTP",
		"creator":    "agent",
	}))
	if status != http.StatusOK {
		t.Fatalf("create proposed goal status = %d; body=%s", status, body)
	}
	var proposed domain.Goal
	if err := json.Unmarshal(body, &proposed); err != nil {
		t.Fatalf("decode proposed goal: %v", err)
	}
	decisions, err := f.store.ListOpenDecisions(f.ctx, proposed.ID)
	if err != nil {
		t.Fatalf("ListOpenDecisions: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("open decisions = %+v", decisions)
	}

	status, _, body = doRequest(t, client, http.MethodPost, urlID(srv.URL+"/api/decisions/", decisions[0].ID)+"/approve", mustJSON(t, map[string]string{}))
	if status != http.StatusOK {
		t.Fatalf("approve goal status = %d; body=%s", status, body)
	}
	var approved domain.Goal
	if err := json.Unmarshal(body, &approved); err != nil {
		t.Fatalf("decode approved goal: %v", err)
	}
	if approved.ID != proposed.ID || approved.Status != domain.GoalActive {
		t.Fatalf("approved goal = %+v, want active", approved)
	}

	status, _, body = doRequest(t, client, http.MethodPost, srv.URL+"/api/goals", mustJSON(t, map[string]string{
		"project_id": idText(f.project.ID),
		"content":    "Reject through HTTP",
		"creator":    "agent",
	}))
	if status != http.StatusOK {
		t.Fatalf("create reject goal status = %d; body=%s", status, body)
	}
	var toReject domain.Goal
	if err := json.Unmarshal(body, &toReject); err != nil {
		t.Fatalf("decode reject goal: %v", err)
	}
	decisions, err = f.store.ListOpenDecisions(f.ctx, toReject.ID)
	if err != nil {
		t.Fatalf("ListOpenDecisions reject: %v", err)
	}
	status, _, body = doRequest(t, client, http.MethodPost, urlID(srv.URL+"/api/decisions/", decisions[0].ID)+"/reject", mustJSON(t, map[string]string{"reason": "scope is not approved"}))
	if status != http.StatusOK {
		t.Fatalf("reject goal status = %d; body=%s", status, body)
	}
	var rejected domain.Decision
	if err := json.Unmarshal(body, &rejected); err != nil {
		t.Fatalf("decode rejected decision: %v", err)
	}
	if rejected.Status != domain.DecisionStatus("answered") || rejected.AnswerLabel != "reject" || !strings.Contains(rejected.AnswerText, "scope is not approved") {
		t.Fatalf("rejected decision = %+v", rejected)
	}
	dropped, err := f.store.GetGoal(f.ctx, toReject.ID)
	if err != nil {
		t.Fatalf("GetGoal dropped: %v", err)
	}
	if dropped.Status != domain.GoalDropped {
		t.Fatalf("dropped goal status = %q, want %q", dropped.Status, domain.GoalDropped)
	}
}

func TestHTTPGoalContentUpdatesProposedGoal(t *testing.T) {
	f := newBareFixture(t)
	proposed, err := f.store.CreateGoal(f.ctx, f.project.ID, "Original proposed content", "agent")
	if err != nil {
		t.Fatal(err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	updatedContent := "Updated proposed content\n\nwith details"
	status, _, body := doRequest(t, srv.Client(), http.MethodPost, urlID(srv.URL+"/api/goals/", proposed.ID)+"/content", mustJSON(t, map[string]string{
		"content": updatedContent,
	}))
	if status != http.StatusOK {
		t.Fatalf("update content status = %d; body=%s", status, body)
	}
	var got domain.Goal
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode updated goal: %v; body=%s", err, body)
	}
	if got.ID != proposed.ID || got.Content != updatedContent || got.Status != domain.GoalProposed {
		t.Fatalf("updated goal = %+v, want id %d, content %q, status %q", got, proposed.ID, updatedContent, domain.GoalProposed)
	}
}

func TestHTTPGoalContentRejectsBlankContent(t *testing.T) {
	for i, content := range []string{"", " \t\n"} {
		t.Run(fmt.Sprintf("blank_%d", i), func(t *testing.T) {
			f := newBareFixture(t)
			proposed, err := f.store.CreateGoal(f.ctx, f.project.ID, "Original proposed content", "agent")
			if err != nil {
				t.Fatal(err)
			}

			srv := newTestServer(t, f.store)
			defer srv.Close()
			status, headers, body := doRequest(t, srv.Client(), http.MethodPost, urlID(srv.URL+"/api/goals/", proposed.ID)+"/content", mustJSON(t, map[string]string{
				"content": content,
			}))
			assertErrorObject(t, status, headers, body, http.StatusBadRequest)
		})
	}
}

func TestHTTPGoalContentReturnsNotFoundForUnknownGoal(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()

	status, headers, body := doRequest(t, srv.Client(), http.MethodPost, srv.URL+"/api/goals/missing-goal/content", mustJSON(t, map[string]string{
		"content": "new content",
	}))
	assertErrorObject(t, status, headers, body, http.StatusNotFound)
}

func TestHTTPGoalContentRejectsActiveGoalWithStatus(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()

	status, headers, body := doRequest(t, srv.Client(), http.MethodPost, urlID(srv.URL+"/api/goals/", f.goal.ID)+"/content", mustJSON(t, map[string]string{
		"content": "new content",
	}))
	assertErrorObject(t, status, headers, body, http.StatusConflict)
	var response map[string]string
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(response["error"], string(domain.GoalActive)) {
		t.Fatalf("error = %q, want it to contain %q", response["error"], domain.GoalActive)
	}
}

func TestHTTPGoalContentRejectsDoneAndDroppedGoals(t *testing.T) {
	f := newBareFixture(t)
	done, err := f.store.CreateGoal(f.ctx, f.project.ID, "Done goal", "human")
	if err != nil {
		t.Fatal(err)
	}
	completion, err := f.store.CompleteGoalWithReport(f.ctx, done.ID, domain.CompletionReport{
		WorkDone:    "done",
		NowPossible: "none",
		HowToVerify: "verify",
		Surprises:   "none",
		NeedsReview: "none",
		NextSteps:   "none",
	}, testSessionID("done-run"))
	if err != nil {
		t.Fatalf("complete done goal: %v", err)
	}
	if _, err := f.store.ApproveCompletion(f.ctx, completion.ID); err != nil {
		t.Fatalf("approve done goal: %v", err)
	}

	dropped, err := f.store.CreateGoal(f.ctx, f.project.ID, "Dropped goal", "agent")
	if err != nil {
		t.Fatal(err)
	}
	decisions, err := f.store.ListOpenDecisions(f.ctx, dropped.ID)
	if err != nil {
		t.Fatalf("list dropped goal decisions: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("dropped goal open decisions = %d, want 1", len(decisions))
	}
	if err := f.store.RejectGoal(f.ctx, decisions[0].ID, "not approved"); err != nil {
		t.Fatalf("reject dropped goal: %v", err)
	}

	srv := newTestServer(t, f.store)
	defer srv.Close()
	for _, test := range []struct {
		name   string
		goalID int64
		status domain.GoalStatus
	}{
		{name: "done", goalID: done.ID, status: domain.GoalDone},
		{name: "dropped", goalID: dropped.ID, status: domain.GoalDropped},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, headers, body := doRequest(t, srv.Client(), http.MethodPost, urlID(srv.URL+"/api/goals/", test.goalID)+"/content", mustJSON(t, map[string]string{
				"content": "new content",
			}))
			assertErrorObject(t, status, headers, body, http.StatusConflict)
			var response map[string]string
			if err := json.Unmarshal(body, &response); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(response["error"], string(test.status)) {
				t.Fatalf("error = %q, want it to contain %q", response["error"], test.status)
			}
		})
	}
}

func TestHTTPGoalContentDoesNotChangeRejectedGoal(t *testing.T) {
	f := newBareFixture(t)
	srv := newTestServer(t, f.store)
	defer srv.Close()

	status, headers, body := doRequest(t, srv.Client(), http.MethodPost, urlID(srv.URL+"/api/goals/", f.goal.ID)+"/content", mustJSON(t, map[string]string{
		"content": "this must not be saved",
	}))
	assertErrorObject(t, status, headers, body, http.StatusConflict)

	status, _, body = doRequest(t, srv.Client(), http.MethodGet, urlID(srv.URL+"/api/goals/", f.goal.ID), nil)
	if status != http.StatusOK {
		t.Fatalf("get rejected goal status = %d; body=%s", status, body)
	}
	var response goalDetailResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode rejected goal: %v; body=%s", err, body)
	}
	if response.Goal.Content != f.goal.Content {
		t.Fatalf("rejected goal content = %q, want unchanged %q", response.Goal.Content, f.goal.Content)
	}
}
