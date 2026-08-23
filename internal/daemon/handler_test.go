package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
)

func ageAgentSessionForTest(t *testing.T, fixture goalListFixture, sessionID string) {
	t.Helper()
	old := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	if _, err := fixture.store.DB().ExecContext(context.Background(), `UPDATE agent_sessions SET registered_at = ? WHERE id = ?`, old, sessionID); err != nil {
		t.Fatalf("age agent session %q: %v", sessionID, err)
	}
}

func TestDaemonAssociationKeepsFirstSession(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	registerLiveGoalClaimSession(t, fixture, "daemon-first-run")
	ageAgentSessionForTest(t, fixture, "daemon-first-run")
	registerLiveGoalClaimSession(t, fixture, "daemon-second-run")
	listGoalForClaimTest(t, fixture, fixture.emptyTaskGoal.ID, "daemon-first-run")
	listGoalForClaimTest(t, fixture, fixture.emptyTaskGoal.ID, "daemon-second-run")

	var count int
	if err := fixture.store.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM agent_sessions WHERE project_id = ?`, fixture.project.ID).Scan(&count); err != nil {
		t.Fatalf("count daemon-associated sessions: %v", err)
	}
	if count != 2 {
		t.Fatalf("daemon-associated session count = %d, want 2", count)
	}
}

func TestProjectClaimRejectsLiveOtherSessionAfterDaemonAssociation(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	registerLiveGoalClaimSession(t, fixture, "daemon-project-owner-run")
	ageAgentSessionForTest(t, fixture, "daemon-project-owner-run")
	registerLiveGoalClaimSession(t, fixture, "daemon-project-other-run")
	if _, err := claimProjectForTest(t, fixture, fixture.project.ID, "daemon-project-owner-run"); err != nil {
		t.Fatalf("initial project.claim: %v", err)
	}
	if _, err := claimProjectForTest(t, fixture, fixture.project.ID, "daemon-project-other-run"); !errors.Is(err, ErrProjectAlreadyClaimed) {
		t.Fatalf("second project.claim error = %v, want ErrProjectAlreadyClaimed", err)
	}

	var claimedBy string
	if err := fixture.store.DB().QueryRowContext(context.Background(), `SELECT claimed_by FROM projects WHERE id = ?`, fixture.project.ID).Scan(&claimedBy); err != nil {
		t.Fatalf("read project claim: %v", err)
	}
	if claimedBy != "daemon-project-owner-run" {
		t.Fatalf("project claimed_by = %q, want %q", claimedBy, "daemon-project-owner-run")
	}
}

func TestProjectReleaseAllowsSecondDaemonSessionToClaim(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	registerLiveGoalClaimSession(t, fixture, "daemon-release-owner-run")
	registerLiveGoalClaimSession(t, fixture, "daemon-release-next-run")
	if _, err := claimProjectForTest(t, fixture, fixture.project.ID, "daemon-release-owner-run"); err != nil {
		t.Fatalf("initial project.claim: %v", err)
	}
	params, err := json.Marshal(map[string]string{"project_id": fixture.project.ID})
	if err != nil {
		t.Fatalf("marshal project.release params: %v", err)
	}
	if _, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "project.release", Params: params}); err != nil {
		t.Fatalf("project.release: %v", err)
	}
	if _, err := claimProjectForTest(t, fixture, fixture.project.ID, "daemon-release-next-run"); err != nil {
		t.Fatalf("second project.claim after release: %v", err)
	}
}

func TestProjectClaimTakesOverDeadDaemonSession(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	deadProcess := exec.Command("sleep", "60")
	if err := deadProcess.Start(); err != nil {
		t.Fatalf("start dead-session fixture: %v", err)
	}
	t.Cleanup(func() {
		if deadProcess.ProcessState == nil {
			_ = deadProcess.Process.Kill()
			_ = deadProcess.Wait()
		}
	})
	if err := fixture.store.RegisterAgentSession(context.Background(), "daemon-dead-run", deadProcess.Process.Pid); err != nil {
		t.Fatalf("RegisterAgentSession(dead): %v", err)
	}
	if err := deadProcess.Process.Kill(); err != nil {
		t.Fatalf("kill dead-session fixture: %v", err)
	}
	_ = deadProcess.Wait()
	registerLiveGoalClaimSession(t, fixture, "daemon-live-run")

	if _, err := claimProjectForTest(t, fixture, fixture.project.ID, "daemon-dead-run"); err != nil {
		t.Fatalf("initial dead project.claim: %v", err)
	}
	if _, err := claimProjectForTest(t, fixture, fixture.project.ID, "daemon-live-run"); err != nil {
		t.Fatalf("take over dead project.claim: %v", err)
	}

	var claimedBy string
	if err := fixture.store.DB().QueryRowContext(context.Background(), `SELECT claimed_by FROM projects WHERE id = ?`, fixture.project.ID).Scan(&claimedBy); err != nil {
		t.Fatalf("read project claim: %v", err)
	}
	if claimedBy != "daemon-live-run" {
		t.Fatalf("project claimed_by = %q, want %q", claimedBy, "daemon-live-run")
	}
}

func TestContractN1GoalListOmitsContentField(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	for _, goal := range listGoalPayloadsForContractTest(t, fixture) {
		if _, ok := goal["content"]; ok {
			t.Fatalf("goal.list returned content field: %s", goal["content"])
		}
	}
}

func TestContractN2GoalListUsesFirstNonEmptyLineAsTitle(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	const content = "12345678901234567890\nsecond line"
	created, err := fixture.store.CreateGoal(context.Background(), fixture.project.ID, content, "contract-test")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	goal := findGoalPayloadForContractTest(t, listGoalPayloadsForContractTest(t, fixture), created.ID)

	var title string
	if err := json.Unmarshal(goal["title"], &title); err != nil {
		t.Fatalf("decode title: %v", err)
	}
	if title != content[:20] {
		t.Fatalf("title = %q, want %q", title, content[:20])
	}
	if strings.Contains(title, "…") {
		t.Fatalf("short title unexpectedly contains ellipsis: %q", title)
	}
}

func TestContractN3GoalListTruncatesTitleAndCountsRunes(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	content := strings.Repeat("あ", 300)
	created, err := fixture.store.CreateGoal(context.Background(), fixture.project.ID, content, "contract-test")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	goal := findGoalPayloadForContractTest(t, listGoalPayloadsForContractTest(t, fixture), created.ID)

	var title string
	if err := json.Unmarshal(goal["title"], &title); err != nil {
		t.Fatalf("decode title: %v", err)
	}
	if want := strings.Repeat("あ", 120) + "…"; title != want {
		t.Fatalf("title rune truncation = %q, want %q", title, want)
	}
	var contentChars int
	if err := json.Unmarshal(goal["content_chars"], &contentChars); err != nil {
		t.Fatalf("decode content_chars: %v", err)
	}
	if contentChars != 300 {
		t.Fatalf("content_chars = %d, want 300", contentChars)
	}
}

func TestContractN4GoalListTaskCountsDistinguishEmptyAndDoneOnlyGoals(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	empty := findGoalPayloadForContractTest(t, listGoalPayloadsForContractTest(t, fixture), fixture.emptyTaskGoal.ID)
	doneOnly := findGoalPayloadForContractTest(t, listGoalPayloadsForContractTest(t, fixture), fixture.doneOnlyGoal.ID)
	if string(empty["id"]) == string(doneOnly["id"]) {
		t.Fatal("empty-task and done-only fixtures have the same goal id")
	}

	var emptyCounts, doneOnlyCounts struct {
		Todo    int `json:"todo"`
		Doing   int `json:"doing"`
		Done    int `json:"done"`
		Dropped int `json:"dropped"`
	}
	if err := json.Unmarshal(empty["task_counts"], &emptyCounts); err != nil {
		t.Fatalf("decode empty task_counts: %v", err)
	}
	if emptyCounts != (struct {
		Todo    int `json:"todo"`
		Doing   int `json:"doing"`
		Done    int `json:"done"`
		Dropped int `json:"dropped"`
	}{}) {
		t.Fatalf("empty task_counts = %+v, want all zero", emptyCounts)
	}
	if err := json.Unmarshal(doneOnly["task_counts"], &doneOnlyCounts); err != nil {
		t.Fatalf("decode done-only task_counts: %v", err)
	}
	if doneOnlyCounts.Done == 0 || doneOnlyCounts.Todo != 0 || doneOnlyCounts.Doing != 0 || doneOnlyCounts.Dropped != 0 {
		t.Fatalf("done-only task_counts = %+v, want done > 0 and other counts zero", doneOnlyCounts)
	}
}

func TestContractN5GoalGetReturnsContentAndAllTaskStatuses(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	wantTasks, err := fixture.store.ListTasks(context.Background(), fixture.doneOnlyGoal.ID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(wantTasks) == 0 {
		t.Fatal("done-only fixture has no tasks")
	}

	params, err := json.Marshal(map[string]string{"goal_id": fixture.doneOnlyGoal.ID})
	if err != nil {
		t.Fatalf("marshal goal.get params: %v", err)
	}
	raw, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "goal.get", Params: params})
	if err != nil {
		t.Fatalf("goal.get: %v", err)
	}
	var response struct {
		Goal struct {
			Content string `json:"content"`
		} `json:"goal"`
		Tasks []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode goal.get response: %v", err)
	}
	if response.Goal.Content == "" {
		t.Fatal("goal.get returned empty goal content")
	}
	if len(response.Tasks) != len(wantTasks) {
		t.Fatalf("goal.get task count = %d, want %d", len(response.Tasks), len(wantTasks))
	}
	seen := make(map[string]string, len(response.Tasks))
	for _, task := range response.Tasks {
		seen[task.ID] = task.Status
	}
	for _, task := range wantTasks {
		if got, ok := seen[task.ID]; !ok || got != string(task.Status) {
			t.Errorf("goal.get task %q = (%q, %v), want status %q", task.ID, got, ok, task.Status)
		}
	}
}

func TestContractN6GoalGetMissingGoalReturnsError(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	params, err := json.Marshal(map[string]string{"goal_id": "missing-goal"})
	if err != nil {
		t.Fatalf("marshal goal.get params: %v", err)
	}
	if _, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "goal.get", Params: params}); err == nil {
		t.Fatal("goal.get succeeded for a missing goal")
	}
}

func TestContractN7SessionStartHookOmitsGoalBody(t *testing.T) {
	output, err := runSessionStartHookForContractTest(t, `#!/usr/bin/env bash
if [[ "$1" == "context" && "$2" == "-brief" ]]; then
  printf 'ATCT: project atct / active goals 1 / todo tasks 1 / waiting answers 0\n'
else
  printf 'SECRET_GOAL_BODY\n'
fi
`)
	if err != nil {
		t.Fatalf("session-start hook: %v\noutput: %s", err, output)
	}
	if strings.Contains(output, "SECRET_GOAL_BODY") {
		t.Fatalf("session-start output contains goal body: %q", output)
	}
}

func TestContractN8GoalListHidesGoalsAwaitingCompletionApproval(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	for _, goalID := range []string{fixture.emptyTaskGoal.ID, fixture.taskGoal.ID} {
		askOpenDecisionForContractTest(t, fixture, goalID, domain.KindCompletion)
	}
	response := goalListResponseForContractTest(t, fixture)
	for _, goalID := range []string{fixture.emptyTaskGoal.ID, fixture.taskGoal.ID} {
		if goalPayloadExistsForContractTest(response.Goals, goalID) {
			t.Errorf("goal.list returned goal %q while completion approval is open", goalID)
		}
	}
}

func TestContractN9GoalListOmitsRedundantGoalFields(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	response := goalListResponseForContractTest(t, fixture)
	for _, goal := range response.Goals {
		if _, ok := goal["project_id"]; ok {
			t.Fatal("goal.list returned redundant project_id field")
		}
	}
	empty := findGoalPayloadForContractTest(t, response.Goals, fixture.emptyTaskGoal.ID)
	for _, key := range []string{"derived_from_goal_id", "claimed_by"} {
		if _, ok := empty[key]; ok {
			t.Fatalf("goal.list returned empty %s field", key)
		}
	}
}

func TestContractN10GoalListReturnsAwaitingApprovalCount(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	for _, goalID := range []string{fixture.emptyTaskGoal.ID, fixture.taskGoal.ID} {
		askOpenDecisionForContractTest(t, fixture, goalID, domain.KindCompletion)
	}
	response := goalListResponseForContractTest(t, fixture)
	if response.AwaitingApprovalCount != 2 {
		t.Fatalf("awaiting_approval_count = %d, want 2", response.AwaitingApprovalCount)
	}
	if len(response.AwaitingApprovalGoalIDs) != 0 {
		t.Fatalf("goal.list returned deprecated awaiting_approval_goal_ids: %s", response.AwaitingApprovalGoalIDs)
	}
}

func TestContractN11GoalListTruncatesTaskDescription(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	fullDescription := strings.Repeat("あ", 300)
	tasks, err := fixture.store.DeclareTasks(context.Background(), fixture.emptyTaskGoal.ID, "contract-test", "description-contract", []string{"description task"}, []string{fullDescription})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("DeclareTasks returned %d tasks, want 1", len(tasks))
	}
	goal := findGoalPayloadForContractTest(t, goalListResponseForContractTest(t, fixture).Goals, fixture.emptyTaskGoal.ID)
	var listed []struct {
		ID          string `json:"id"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(goal["tasks"], &listed); err != nil {
		t.Fatalf("decode listed tasks: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != tasks[0].ID {
		t.Fatalf("goal.list tasks = %+v, want task %s", listed, tasks[0].ID)
	}
	if want := strings.Repeat("あ", 120) + "…"; listed[0].Description != want {
		t.Fatalf("task description = %q, want %q", listed[0].Description, want)
	}
}

func TestContractB1GoalListKeepsActiveAndProposedGoals(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	proposed, err := fixture.store.CreateGoal(context.Background(), fixture.project.ID, "proposed contract goal", "contract-test")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	goals := listGoalPayloadsForContractTest(t, fixture)
	findGoalPayloadForContractTest(t, goals, fixture.emptyTaskGoal.ID)
	findGoalPayloadForContractTest(t, goals, proposed.ID)
}

func TestContractB2GoalListKeepsOnlyTodoAndDoingTasks(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	for _, goal := range listGoalPayloadsForContractTest(t, fixture) {
		var tasks []struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(goal["tasks"], &tasks); err != nil {
			t.Fatalf("decode tasks: %v", err)
		}
		for _, task := range tasks {
			if task.Status != "todo" && task.Status != "doing" {
				t.Errorf("goal.list returned non-active task status %q", task.Status)
			}
		}
	}
}

func TestContractB3GoalListKeepsDecisionResponseKeys(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	params, err := json.Marshal(map[string]string{"cwd": fixture.project.RootPath})
	if err != nil {
		t.Fatalf("marshal goal.list params: %v", err)
	}
	raw, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "goal.list", Params: params})
	if err != nil {
		t.Fatalf("goal.list: %v", err)
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode goal.list response: %v", err)
	}
	for _, key := range []string{"answered_decisions", "orphaned_decisions"} {
		if _, ok := response[key]; !ok {
			t.Errorf("goal.list response missing %q", key)
		}
	}
}

func TestContractB4GoalGetKeepsCompleteGoalData(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	params, err := json.Marshal(map[string]string{"goal_id": fixture.doneOnlyGoal.ID})
	if err != nil {
		t.Fatalf("marshal goal.get params: %v", err)
	}
	raw, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "goal.get", Params: params})
	if err != nil {
		t.Fatalf("goal.get: %v", err)
	}
	var response struct {
		Goal json.RawMessage `json:"goal"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode goal.get response: %v", err)
	}
	var got, want map[string]any
	if err := json.Unmarshal(response.Goal, &got); err != nil {
		t.Fatalf("decode returned goal: %v", err)
	}
	wantGoal, err := fixture.store.GetGoal(context.Background(), fixture.doneOnlyGoal.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	wantJSON, err := json.Marshal(wantGoal)
	if err != nil {
		t.Fatalf("marshal stored goal: %v", err)
	}
	if err := json.Unmarshal(wantJSON, &want); err != nil {
		t.Fatalf("decode stored goal: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("goal.get returned %d goal fields, want %d", len(got), len(want))
	}
	for key, wantValue := range want {
		if got[key] == nil {
			t.Errorf("goal.get missing goal field %q (want %v)", key, wantValue)
		}
	}
}

func TestContractB5GoalListRemainsAvailableToContextConsumers(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()
	if len(listGoalPayloadsForContractTest(t, fixture)) == 0 {
		t.Fatal("goal.list returned no goals for a registered project")
	}
}

func TestContractB6SessionStartHookKeepsFixedInstructionBlock(t *testing.T) {
	output, err := runSessionStartHookForContractTest(t, `#!/usr/bin/env bash
if [[ "$1" == "context" && "$2" == "-brief" ]]; then
  printf 'ATCT: project atct / active goals 1 / todo tasks 1 / waiting answers 0\n'
  exit 0
fi
exit 1
`)
	if err != nil {
		t.Fatalf("session-start hook: %v\noutput: %s", err, output)
	}
	for _, marker := range []string{
		"This repository is registered with ATCT.",
		"An active goal is permission to work.",
		"See the `atct` skill for details.",
	} {
		if !strings.Contains(output, marker) {
			t.Errorf("session-start output missing fixed instruction %q", marker)
		}
	}
}

func TestContractB7SessionStartHookSilentlySkipsUnregisteredRepo(t *testing.T) {
	output, err := runSessionStartHookForContractTest(t, `#!/usr/bin/env bash
case "$1 $2" in
  "project list") printf 'atct\n' ;;
  "goal list") printf 'global goal\n' ;;
  "context -brief") exit 1 ;;
  *) exit 1 ;;
esac
`)
	if err != nil {
		t.Fatalf("session-start hook: %v\noutput: %s", err, output)
	}
	t.Logf("unregistered repository hook output: %q", output)
	if output != "" {
		t.Fatalf("unregistered repository produced hook output: %q", output)
	}
}

func TestContractB8GoalListKeepsGoalsWithOpenDecision(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	for _, goalID := range []string{fixture.active[0].ID, fixture.active[1].ID} {
		askOpenDecisionForContractTest(t, fixture, goalID, domain.KindDecision)
	}
	goals := goalListResponseForContractTest(t, fixture).Goals
	for _, goalID := range []string{fixture.active[0].ID, fixture.active[1].ID} {
		if !goalPayloadExistsForContractTest(goals, goalID) {
			t.Errorf("goal.list omitted goal %q with an open decision", goalID)
		}
	}
}

func TestContractB9GoalListKeepsGoalsWithoutOpenDecision(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	findGoalPayloadForContractTest(t, goalListResponseForContractTest(t, fixture).Goals, fixture.emptyTaskGoal.ID)
}

func TestContractB10GoalListKeepsNonEmptyOptionalFields(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	derived, err := fixture.store.CreateGoal(context.Background(), fixture.project.ID, "derived contract goal", "contract-test", fixture.emptyTaskGoal.ID)
	if err != nil {
		t.Fatalf("CreateGoal derived: %v", err)
	}
	if _, err := fixture.store.ClaimGoal(context.Background(), fixture.emptyTaskGoal.ID, "contract-claimed"); err != nil {
		t.Fatalf("ClaimGoal: %v", err)
	}
	goals := goalListResponseForContractTest(t, fixture).Goals
	derivedPayload := findGoalPayloadForContractTest(t, goals, derived.ID)
	claimedPayload := findGoalPayloadForContractTest(t, goals, fixture.emptyTaskGoal.ID)
	var gotDerived, gotClaimed string
	if err := json.Unmarshal(derivedPayload["derived_from_goal_id"], &gotDerived); err != nil {
		t.Fatalf("decode derived_from_goal_id: %v", err)
	}
	if gotDerived != fixture.emptyTaskGoal.ID {
		t.Fatalf("derived_from_goal_id = %q, want %q", gotDerived, fixture.emptyTaskGoal.ID)
	}
	if err := json.Unmarshal(claimedPayload["claimed_by"], &gotClaimed); err != nil {
		t.Fatalf("decode claimed_by: %v", err)
	}
	if gotClaimed != "contract-claimed" {
		t.Fatalf("claimed_by = %q, want contract-claimed", gotClaimed)
	}
}

func TestContractB11GoalListKeepsShortTaskDescription(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	const fullDescription = "first line\nsecond line is not part of the list preview"
	tasks, err := fixture.store.DeclareTasks(context.Background(), fixture.emptyTaskGoal.ID, "contract-test", "short-description-contract", []string{"short description task"}, []string{fullDescription})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	goal := findGoalPayloadForContractTest(t, goalListResponseForContractTest(t, fixture).Goals, fixture.emptyTaskGoal.ID)
	var listed []struct {
		ID          string `json:"id"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(goal["tasks"], &listed); err != nil {
		t.Fatalf("decode listed tasks: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != tasks[0].ID {
		t.Fatalf("goal.list tasks = %+v, want task %s", listed, tasks[0].ID)
	}
	if listed[0].Description != "first line" {
		t.Fatalf("short task description = %q, want first line without ellipsis", listed[0].Description)
	}
}

func TestContractB12GoalGetReturnsFullTaskDescription(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	fullDescription := strings.Repeat("あ", 300)
	tasks, err := fixture.store.DeclareTasks(context.Background(), fixture.emptyTaskGoal.ID, "contract-test", "full-description-contract", []string{"full description task"}, []string{fullDescription})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	params, err := json.Marshal(map[string]string{"goal_id": fixture.emptyTaskGoal.ID})
	if err != nil {
		t.Fatalf("marshal goal.get params: %v", err)
	}
	raw, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "goal.get", Params: params})
	if err != nil {
		t.Fatalf("goal.get: %v", err)
	}
	var response struct {
		Tasks []struct {
			ID          string `json:"id"`
			Description string `json:"description"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode goal.get response: %v", err)
	}
	for _, task := range response.Tasks {
		if task.ID == tasks[0].ID {
			if task.Description != fullDescription {
				t.Fatalf("goal.get task description rune count = %d, want %d", len([]rune(task.Description)), len([]rune(fullDescription)))
			}
			return
		}
	}
	t.Fatalf("goal.get did not return task %q", tasks[0].ID)
}

type contractGoalListResponse struct {
	Goals                   []map[string]json.RawMessage `json:"goals"`
	AwaitingApprovalCount   int                          `json:"awaiting_approval_count"`
	AwaitingApprovalGoalIDs json.RawMessage              `json:"awaiting_approval_goal_ids"`
}

func goalListResponseForContractTest(t *testing.T, fixture goalListFixture) contractGoalListResponse {
	t.Helper()
	params, err := json.Marshal(map[string]string{"cwd": fixture.project.RootPath})
	if err != nil {
		t.Fatalf("marshal goal.list params: %v", err)
	}
	raw, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "goal.list", Params: params})
	if err != nil {
		t.Fatalf("goal.list: %v", err)
	}
	var response contractGoalListResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode goal.list response: %v", err)
	}
	return response
}

func listGoalPayloadsForContractTest(t *testing.T, fixture goalListFixture) []map[string]json.RawMessage {
	t.Helper()
	return goalListResponseForContractTest(t, fixture).Goals
}

func findGoalPayloadForContractTest(t *testing.T, goals []map[string]json.RawMessage, id string) map[string]json.RawMessage {
	t.Helper()
	for _, goal := range goals {
		var gotID string
		if err := json.Unmarshal(goal["id"], &gotID); err != nil {
			t.Fatalf("decode goal id: %v", err)
		}
		if gotID == id {
			return goal
		}
	}
	t.Fatalf("goal.list did not return goal %q", id)
	return nil
}

func goalPayloadExistsForContractTest(goals []map[string]json.RawMessage, id string) bool {
	for _, goal := range goals {
		var gotID string
		if err := json.Unmarshal(goal["id"], &gotID); err != nil {
			continue
		}
		if gotID == id {
			return true
		}
	}
	return false
}

func askOpenDecisionForContractTest(t *testing.T, fixture goalListFixture, goalID string, kind domain.DecisionKind) {
	t.Helper()
	input := store.AskInput{
		GoalID:   goalID,
		Kind:     kind,
		Question: "contract test open decision",
	}
	if kind == domain.KindDecision {
		tasks, err := fixture.store.ListTasks(context.Background(), goalID)
		if err != nil {
			t.Fatalf("ListTasks(%s): %v", goalID, err)
		}
		if len(tasks) == 0 {
			declared, err := fixture.store.DeclareTasks(context.Background(), goalID, "contract-test", "decision-contract", []string{"decision task"}, []string{"decision task"})
			if err != nil {
				t.Fatalf("DeclareTasks(%s): %v", goalID, err)
			}
			tasks = declared
		}
		input.TaskID = tasks[0].ID
	}
	_, err := fixture.store.AskDecision(context.Background(), input)
	if err != nil {
		t.Fatalf("AskDecision(%s): %v", kind, err)
	}
}

func runSessionStartHookForContractTest(t *testing.T, atctScript string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	hookDir := filepath.Join(dir, "hooks")
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatalf("MkdirAll hooks: %v", err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll bin: %v", err)
	}
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	source, err := os.ReadFile(filepath.Join(repoRoot, "plugin", "hooks", "session-start"))
	if err != nil {
		t.Fatalf("read session-start hook: %v", err)
	}
	hookPath := filepath.Join(hookDir, "session-start")
	if err := os.WriteFile(hookPath, source, 0o755); err != nil {
		t.Fatalf("write session-start hook: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "atct"), []byte(atctScript), 0o755); err != nil {
		t.Fatalf("write fake atct: %v", err)
	}
	cmd := exec.Command("bash", hookPath)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	return string(output), err
}
