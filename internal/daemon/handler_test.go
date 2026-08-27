package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/mcpshim"
	"github.com/michiomochi/atct/internal/rpc"
	"github.com/michiomochi/atct/internal/store"
)

func TestDispatchResolvesCanonicalNumericStringEntityIDs(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	params, err := json.Marshal(map[string]any{
		"project_id":       strconv.FormatInt(fixture.project.ID, 10),
		"agent_session_id": daemonTestSessionID(t, fixture.store, "dispatch-id-resolution-session"),
	})
	if err != nil {
		t.Fatalf("marshal project.claim params: %v", err)
	}
	if _, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "project.claim", Params: params}); err != nil {
		t.Fatalf("project.claim with numeric string: %v", err)
	}
}

func TestDispatchRejectsLegacyEntityIDsWithMigrationGuidance(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	params, err := json.Marshal(map[string]any{
		"project_id":       "1e082f2f",
		"agent_session_id": daemonTestSessionID(t, fixture.store, "dispatch-legacy-id-session"),
	})
	if err != nil {
		t.Fatalf("marshal project.claim params: %v", err)
	}
	if _, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "project.claim", Params: params}); err == nil {
		t.Fatal("project.claim with legacy ID unexpectedly succeeded")
	} else {
		if !strings.Contains(err.Error(), "id must be a number; UUID-style ids were removed in 0020.") {
			t.Fatalf("legacy ID error = %q, want migration guidance", err)
		}
		if !strings.Contains(err.Error(), "doc/specs/2026-08-27-uuid-to-integer-mapping.md") {
			t.Fatalf("legacy ID error = %q, want mapping path", err)
		}
	}
}

func TestSessionRoleDerivesFromClaims(t *testing.T) {
	wantBoundary := map[string]struct {
		does    []string
		doesNot []string
	}{
		"commander": {
			does:    []string{"triage incoming work", "split goals", "prepare a working area", "review landed changes", "publish", "resolve conflicts", "clean up"},
			doesNot: []string{"design the goal", "implement the goal", "edit executor deliverables"},
		},
		"subcommander": {
			does:    []string{"design the goal", "delegate the goal's work", "review implementation", "report completion for the goal", "issue decisions to the human", "commit the goal's work", "close a task its worker cannot"},
			doesNot: []string{"inspect or manage other goals", "publish", "create another subcommander", "claim the project"},
		},
		"executor": {
			does:    []string{"implement", "test", "close the task it was given"},
			doesNot: []string{"make design decisions", "re-delegate", "commit", "write internal version-control details"},
		},
	}

	tests := []struct {
		name         string
		claimProject bool
		claimGoal    bool
		goalIndex    int
		wantRole     string
	}{
		{name: "commander-project-only", claimProject: true, wantRole: "commander"},
		{name: "commander-project-and-goal", claimProject: true, claimGoal: true, wantRole: "commander"},
		{name: "subcommander-empty-task-goal", claimGoal: true, goalIndex: -1, wantRole: "subcommander"},
		{name: "subcommander-active-goal", claimGoal: true, goalIndex: 0, wantRole: "subcommander"},
		{name: "executor-unclaimed-session", wantRole: "executor"},
		{name: "executor-second-unclaimed-session", wantRole: "executor"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newGoalListFixture(t)
			defer fixture.store.Close()

			sessionID := daemonTestSessionID(t, fixture.store, "session-role-"+tt.name)
			if tt.claimProject {
				params, err := json.Marshal(map[string]any{
					"project_id":       fixture.project.ID,
					"agent_session_id": sessionID,
				})
				if err != nil {
					t.Fatalf("marshal project.claim params: %v", err)
				}
				if _, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "project.claim", Params: params}); err != nil {
					t.Fatalf("project.claim: %v", err)
				}
			}

			var goalID int64
			if tt.claimGoal {
				goalID = fixture.emptyTaskGoal.ID
				if tt.goalIndex >= 0 {
					goalID = fixture.active[tt.goalIndex].ID
				}
				params, err := json.Marshal(map[string]any{
					"goal_id":          goalID,
					"agent_session_id": sessionID,
				})
				if err != nil {
					t.Fatalf("marshal goal.claim params: %v", err)
				}
				if _, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "goal.claim", Params: params}); err != nil {
					t.Fatalf("goal.claim: %v", err)
				}
			}

			params, err := json.Marshal(map[string]any{"agent_session_id": sessionID})
			if err != nil {
				t.Fatalf("marshal session.role params: %v", err)
			}
			raw, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "session.role", Params: params})
			if err != nil {
				t.Fatalf("session.role: %v", err)
			}

			var gotRole string
			var gotDoes, gotDoesNot []string
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatalf("decode session.role fields %v: %v", raw, err)
			}
			switch tt.wantRole {
			case "commander":
				var got commanderRole
				if err := json.Unmarshal(raw, &got); err != nil {
					t.Fatalf("decode commander role response %v: %v", raw, err)
				}
				gotRole, gotDoes, gotDoesNot = got.Role, got.Does, got.DoesNot
				if got.ProjectID != fixture.project.ID {
					t.Fatalf("project_id = %v, want %v (response %v)", got.ProjectID, fixture.project.ID, raw)
				}
				if _, ok := fields["goal_id"]; ok {
					t.Fatalf("commander response contains goal_id: %v", raw)
				}
			case "subcommander":
				var got subcommanderRole
				if err := json.Unmarshal(raw, &got); err != nil {
					t.Fatalf("decode subcommander role response %v: %v", raw, err)
				}
				gotRole, gotDoes, gotDoesNot = got.Role, got.Does, got.DoesNot
				if got.GoalID != goalID {
					t.Fatalf("goal_id = %v, want %v (response %v)", got.GoalID, goalID, raw)
				}
				if _, ok := fields["project_id"]; ok {
					t.Fatalf("subcommander response contains project_id: %v", raw)
				}
			case "executor":
				var got executorRole
				if err := json.Unmarshal(raw, &got); err != nil {
					t.Fatalf("decode executor role response %v: %v", raw, err)
				}
				gotRole, gotDoes, gotDoesNot = got.Role, got.Does, got.DoesNot
				for _, field := range []string{"project_id", "goal_id"} {
					if _, ok := fields[field]; ok {
						t.Fatalf("executor response contains %s: %v", field, raw)
					}
				}
			}
			if gotRole != tt.wantRole {
				t.Fatalf("role = %v, want %v (response %v)", gotRole, tt.wantRole, raw)
			}
			want := wantBoundary[tt.wantRole]
			if !reflect.DeepEqual(gotDoes, want.does) {
				t.Fatalf("does = %#v, want %#v (response %v)", gotDoes, want.does, raw)
			}
			if !reflect.DeepEqual(gotDoesNot, want.doesNot) {
				t.Fatalf("does_not = %#v, want %#v (response %v)", gotDoesNot, want.doesNot, raw)
			}
		})
	}
}

func ageAgentSessionForTest(t *testing.T, fixture goalListFixture, sessionID string) {
	t.Helper()
	old := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	if _, err := fixture.store.DB().ExecContext(context.Background(), `UPDATE agent_sessions SET registered_at = ? WHERE id = ?`, old, daemonTestSessionID(t, fixture.store, sessionID)); err != nil {
		t.Fatalf("age agent session %v: %v", sessionID, err)
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

	var claimedBy int64
	if err := fixture.store.DB().QueryRowContext(context.Background(), `SELECT claimed_by FROM projects WHERE id = ?`, fixture.project.ID).Scan(&claimedBy); err != nil {
		t.Fatalf("read project claim: %v", err)
	}
	wantClaimedBy := daemonTestSessionID(t, fixture.store, "daemon-project-owner-run")
	if claimedBy != wantClaimedBy {
		t.Fatalf("project claimed_by = %v, want %v", claimedBy, wantClaimedBy)
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
	params, err := json.Marshal(map[string]any{
		"project_id":       fixture.project.ID,
		"agent_session_id": daemonTestSessionID(t, fixture.store, "daemon-release-owner-run"),
	})
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

func TestProjectReleaseAllowsHolderSession(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	const sessionLabel = "daemon-release-holder-run"
	registerLiveGoalClaimSession(t, fixture, sessionLabel)
	sessionID := daemonTestSessionID(t, fixture.store, sessionLabel)
	if _, err := fixture.store.ClaimProject(context.Background(), fixture.project.ID, sessionID); err != nil {
		t.Fatalf("project.claim: %v", err)
	}

	params, err := json.Marshal(map[string]any{
		"project_id":       fixture.project.ID,
		"agent_session_id": sessionID,
	})
	if err != nil {
		t.Fatalf("marshal project.release params: %v", err)
	}
	if _, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "project.release", Params: params}); err != nil {
		t.Fatalf("project.release: %v", err)
	}
}

func TestProjectReleaseAllowsSessionBoundToProject(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	const (
		holderSession = "daemon-release-bound-holder-run"
		callerSession = "daemon-release-bound-caller-run"
	)
	registerLiveGoalClaimSession(t, fixture, holderSession)
	registerLiveGoalClaimSession(t, fixture, callerSession)
	if _, err := claimProjectForTest(t, fixture, fixture.project.ID, holderSession); err != nil {
		t.Fatalf("project.claim: %v", err)
	}
	if err := fixture.store.AssociateAgentSessionWithProject(context.Background(), daemonTestSessionID(t, fixture.store, callerSession), fixture.project.ID); err != nil {
		t.Fatalf("associate caller session with project: %v", err)
	}

	params, err := json.Marshal(map[string]any{
		"project_id":       fixture.project.ID,
		"agent_session_id": daemonTestSessionID(t, fixture.store, callerSession),
	})
	if err != nil {
		t.Fatalf("marshal project.release params: %v", err)
	}
	if _, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "project.release", Params: params}); err != nil {
		t.Fatalf("project.release: %v", err)
	}
}

func TestProjectReleaseRejectsEmptyAgentSession(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	const holderSession = "daemon-release-empty-holder-run"
	registerLiveGoalClaimSession(t, fixture, holderSession)
	if _, err := claimProjectForTest(t, fixture, fixture.project.ID, holderSession); err != nil {
		t.Fatalf("project.claim: %v", err)
	}

	params, err := json.Marshal(map[string]any{
		"project_id":       fixture.project.ID,
		"agent_session_id": "",
	})
	if err != nil {
		t.Fatalf("marshal project.release params: %v", err)
	}
	_, err = fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "project.release", Params: params})
	if err == nil {
		t.Fatal("project.release succeeded without agent_session_id")
	}
	if !strings.Contains(err.Error(), "requires agent_session_id") || !strings.Contains(err.Error(), fmt.Sprint(fixture.project.ID)) {
		t.Fatalf("project.release error = %v, want required agent_session_id and project %v", err, fixture.project.ID)
	}
}

func TestProjectReleaseRejectsSessionBoundToAnotherProject(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	const (
		holderSession  = "daemon-release-foreign-holder-run"
		foreignSession = "daemon-release-foreign-caller-run"
	)
	registerLiveGoalClaimSession(t, fixture, holderSession)
	registerLiveGoalClaimSession(t, fixture, foreignSession)
	if _, err := claimProjectForTest(t, fixture, fixture.project.ID, holderSession); err != nil {
		t.Fatalf("project.claim: %v", err)
	}
	foreignProject, err := fixture.store.CreateProject(context.Background(), "foreign-release-project", t.TempDir())
	if err != nil {
		t.Fatalf("create foreign project: %v", err)
	}
	if err := fixture.store.AssociateAgentSessionWithProject(context.Background(), daemonTestSessionID(t, fixture.store, foreignSession), foreignProject.ID); err != nil {
		t.Fatalf("associate foreign session with project: %v", err)
	}

	params, err := json.Marshal(map[string]any{
		"project_id":       fixture.project.ID,
		"agent_session_id": daemonTestSessionID(t, fixture.store, foreignSession),
	})
	if err != nil {
		t.Fatalf("marshal project.release params: %v", err)
	}
	_, err = fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "project.release", Params: params})
	if err == nil {
		t.Fatal("project.release succeeded for a session bound to another project")
	}
	foreignSessionID := daemonTestSessionID(t, fixture.store, foreignSession)
	if !strings.Contains(err.Error(), fmt.Sprint(foreignSessionID)) || !strings.Contains(err.Error(), fmt.Sprint(fixture.project.ID)) || !strings.Contains(err.Error(), fmt.Sprint(foreignProject.ID)) {
		t.Fatalf("project.release error = %v, want caller %v and projects %v/%v", err, foreignSessionID, foreignProject.ID, fixture.project.ID)
	}
}

func TestTaskReleaseAllowsHolderSession(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	const holderSession = "daemon-task-release-holder-run"
	registerLiveGoalClaimSession(t, fixture, holderSession)
	if _, err := fixture.store.ClaimTask(context.Background(), fixture.tasks[1].ID, daemonTestSessionID(t, fixture.store, holderSession)); err != nil {
		t.Fatalf("task.claim: %v", err)
	}

	params, err := json.Marshal(map[string]any{
		"task_id":          fixture.tasks[1].ID,
		"agent_session_id": daemonTestSessionID(t, fixture.store, holderSession),
	})
	if err != nil {
		t.Fatalf("marshal task.release params: %v", err)
	}
	result, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "task.release", Params: params})
	if err != nil {
		t.Fatalf("task.release: %v", err)
	}
	var released domain.Task
	if err := json.Unmarshal(result, &released); err != nil {
		t.Fatalf("unmarshal task.release result: %v", err)
	}
	if released.Status != domain.TaskTodo {
		t.Fatalf("released task status = %v, want %v", released.Status, domain.TaskTodo)
	}
	if handoff := openTaskHandoffForTest(t, fixture, fixture.tasks[1].GoalID, fixture.tasks[1].ID); handoff != nil {
		t.Fatalf("task handoff after release = %+v, want none", handoff)
	}
}

func TestTaskReleaseAllowsSessionBoundToProject(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	const (
		holderSession = "daemon-task-release-bound-holder-run"
		callerSession = "daemon-task-release-bound-caller-run"
	)
	registerLiveGoalClaimSession(t, fixture, holderSession)
	registerLiveGoalClaimSession(t, fixture, callerSession)
	if err := fixture.store.AssociateAgentSessionWithProject(context.Background(), daemonTestSessionID(t, fixture.store, callerSession), fixture.project.ID); err != nil {
		t.Fatalf("associate caller session with project: %v", err)
	}
	if _, err := fixture.store.ClaimTask(context.Background(), fixture.tasks[1].ID, daemonTestSessionID(t, fixture.store, holderSession)); err != nil {
		t.Fatalf("task.claim: %v", err)
	}

	params, err := json.Marshal(map[string]any{
		"task_id":          fixture.tasks[1].ID,
		"agent_session_id": daemonTestSessionID(t, fixture.store, callerSession),
	})
	if err != nil {
		t.Fatalf("marshal task.release params: %v", err)
	}
	result, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "task.release", Params: params})
	if err != nil {
		t.Fatalf("task.release: %v", err)
	}
	var released domain.Task
	if err := json.Unmarshal(result, &released); err != nil {
		t.Fatalf("unmarshal task.release result: %v", err)
	}
	if released.Status != domain.TaskTodo {
		t.Fatalf("released task status = %v, want %v", released.Status, domain.TaskTodo)
	}
	if handoff := openTaskHandoffForTest(t, fixture, fixture.tasks[1].GoalID, fixture.tasks[1].ID); handoff != nil {
		t.Fatalf("task handoff after release = %+v, want none", handoff)
	}
}

func TestTaskReleaseRejectsEmptyAgentSession(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	const holderSession = "daemon-task-release-empty-holder-run"
	registerLiveGoalClaimSession(t, fixture, holderSession)
	if _, err := fixture.store.ClaimTask(context.Background(), fixture.tasks[1].ID, daemonTestSessionID(t, fixture.store, holderSession)); err != nil {
		t.Fatalf("task.claim: %v", err)
	}

	params, err := json.Marshal(map[string]any{
		"task_id":          fixture.tasks[1].ID,
		"agent_session_id": "",
	})
	if err != nil {
		t.Fatalf("marshal task.release params: %v", err)
	}
	if _, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "task.release", Params: params}); err == nil {
		t.Fatal("task.release succeeded without agent_session_id")
	}
}

func TestTaskReleaseRejectsSessionBoundToAnotherProject(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	const (
		holderSession  = "daemon-task-release-foreign-holder-run"
		foreignSession = "daemon-task-release-foreign-caller-run"
	)
	registerLiveGoalClaimSession(t, fixture, holderSession)
	registerLiveGoalClaimSession(t, fixture, foreignSession)
	foreignProject, err := fixture.store.CreateProject(context.Background(), "foreign-task-release-project", t.TempDir())
	if err != nil {
		t.Fatalf("create foreign project: %v", err)
	}
	if err := fixture.store.AssociateAgentSessionWithProject(context.Background(), daemonTestSessionID(t, fixture.store, foreignSession), foreignProject.ID); err != nil {
		t.Fatalf("associate foreign session with project: %v", err)
	}
	if _, err := fixture.store.ClaimTask(context.Background(), fixture.tasks[1].ID, daemonTestSessionID(t, fixture.store, holderSession)); err != nil {
		t.Fatalf("task.claim: %v", err)
	}

	params, err := json.Marshal(map[string]any{
		"task_id":          fixture.tasks[1].ID,
		"agent_session_id": daemonTestSessionID(t, fixture.store, foreignSession),
	})
	if err != nil {
		t.Fatalf("marshal task.release params: %v", err)
	}
	_, err = fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "task.release", Params: params})
	if err == nil {
		t.Fatal("task.release succeeded for a session bound to another project")
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
	_ = daemonTestSessionIDWithPID(t, fixture.store, "daemon-dead-run", deadProcess.Process.Pid)
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

	var claimedBy int64
	if err := fixture.store.DB().QueryRowContext(context.Background(), `SELECT claimed_by FROM projects WHERE id = ?`, fixture.project.ID).Scan(&claimedBy); err != nil {
		t.Fatalf("read project claim: %v", err)
	}
	wantClaimedBy := daemonTestSessionID(t, fixture.store, "daemon-live-run")
	if claimedBy != wantClaimedBy {
		t.Fatalf("project claimed_by = %v, want %v", claimedBy, wantClaimedBy)
	}
}

func TestContractN1GoalListOmitsContentField(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	for _, goal := range listGoalPayloadsForContractTest(t, fixture) {
		if _, ok := goal["content"]; ok {
			t.Fatalf("goal.list returned content field: %v", goal["content"])
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
		t.Fatalf("title = %v, want %v", title, content[:20])
	}
	if strings.Contains(title, "…") {
		t.Fatalf("short title unexpectedly contains ellipsis: %v", title)
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
		t.Fatalf("title rune truncation = %v, want %v", title, want)
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

	params, err := json.Marshal(map[string]any{"goal_id": fixture.doneOnlyGoal.ID})
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
			ID     int64  `json:"id"`
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
	seen := make(map[int64]any, len(response.Tasks))
	for _, task := range response.Tasks {
		seen[task.ID] = task.Status
	}
	for _, task := range wantTasks {
		if got, ok := seen[task.ID]; !ok || got != string(task.Status) {
			t.Errorf("goal.get task %v = (%v, %v), want status %v", task.ID, got, ok, task.Status)
		}
	}
}

func TestContractN6GoalGetMissingGoalReturnsError(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	params, err := json.Marshal(map[string]any{"goal_id": "missing-goal"})
	if err != nil {
		t.Fatalf("marshal goal.get params: %v", err)
	}
	if _, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "goal.get", Params: params}); err == nil {
		t.Fatal("goal.get succeeded for a missing goal")
	}
}

func TestTaskDeclareReturnsOnlyTasksDeclaredByThisCall(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	sessionID := "task-declare-contract-session"
	registerLiveGoalClaimSession(t, fixture, sessionID)
	before, err := fixture.store.ListTasks(context.Background(), fixture.emptyTaskGoal.ID)
	if err != nil {
		t.Fatalf("ListTasks before declarations: %v", err)
	}

	var responseSize int
	declaredIDs := make(map[int64]struct{}, 3)
	for _, key := range []string{"declare-contract-key-1", "declare-contract-key-2", "declare-contract-key-3"} {
		raw, response := dispatchTaskDeclareForContractTest(t, fixture, sessionID, key, "declared task", "declared description")
		if len(response) != 1 {
			t.Fatalf("task.declare %v returned %d tasks, want exactly 1; response bytes=%d: %v", key, len(response), len(raw), raw)
		}
		if response[0].GoalID != fixture.emptyTaskGoal.ID {
			t.Fatalf("task.declare %v returned goal_id %v, want %v", key, response[0].GoalID, fixture.emptyTaskGoal.ID)
		}
		if response[0].Title != "declared task" {
			t.Fatalf("task.declare %v returned title %v, want declared task", key, response[0].Title)
		}
		if _, duplicate := declaredIDs[response[0].ID]; duplicate {
			t.Fatalf("task.declare %v returned a duplicate task id %v", key, response[0].ID)
		}
		declaredIDs[response[0].ID] = struct{}{}
		if responseSize == 0 {
			responseSize = len(raw)
		} else if len(raw) != responseSize {
			t.Fatalf("task.declare response grew across repeated declarations: first=%d current=%d; response=%v", responseSize, len(raw), raw)
		}
	}

	goalParams, err := json.Marshal(map[string]any{"goal_id": fixture.emptyTaskGoal.ID})
	if err != nil {
		t.Fatalf("marshal goal.get params: %v", err)
	}
	raw, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "goal.get", Params: goalParams})
	if err != nil {
		t.Fatalf("goal.get after task.declare: %v", err)
	}
	var goalResponse struct {
		Tasks []struct {
			ID int64 `json:"id"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(raw, &goalResponse); err != nil {
		t.Fatalf("decode goal.get response: %v", err)
	}
	if len(goalResponse.Tasks) != len(before)+len(declaredIDs) {
		t.Fatalf("goal.get task count = %d, want %d", len(goalResponse.Tasks), len(before)+len(declaredIDs))
	}
	for _, task := range goalResponse.Tasks {
		delete(declaredIDs, task.ID)
	}
	if len(declaredIDs) != 0 {
		t.Fatalf("goal.get did not retain declared task ids: %v", declaredIDs)
	}
}

func TestTaskDeclareIdempotencyReplayReturnsExistingTask(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	sessionID := "task-declare-idempotency-contract-session"
	registerLiveGoalClaimSession(t, fixture, sessionID)
	_, first := dispatchTaskDeclareForContractTest(t, fixture, sessionID, "idempotency-contract-key", "first declaration", "first description")
	_, replay := dispatchTaskDeclareForContractTest(t, fixture, sessionID, "idempotency-contract-key", "replayed declaration", "replayed description")
	if len(first) != 1 {
		t.Fatalf("first task.declare returned %d tasks, want 1", len(first))
	}
	if len(replay) != 1 {
		t.Fatalf("idempotent task.declare replay returned %d tasks, want 1 existing task: %+v", len(replay), replay)
	}
	if replay[0].ID != first[0].ID || replay[0].Title != first[0].Title {
		t.Fatalf("idempotent task.declare replay returned %+v, want existing task %+v", replay[0], first[0])
	}
	tasks, err := fixture.store.ListTasks(context.Background(), fixture.emptyTaskGoal.ID)
	if err != nil {
		t.Fatalf("ListTasks after idempotent task.declare: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("idempotent task.declare created %d tasks, want 1", len(tasks))
	}
}

func TestDecisionAskClaimableTasksKeepsIdentityFieldsWithoutDescription(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	sessionID := "decision-ask-contract-session"
	registerLiveGoalClaimSession(t, fixture, sessionID)
	goals, err := fixture.store.ListGoals(context.Background(), fixture.project.ID)
	if err != nil {
		t.Fatalf("ListGoals: %v", err)
	}
	for _, goal := range goals {
		tasks, err := fixture.store.ListTasks(context.Background(), goal.ID)
		if err != nil {
			t.Fatalf("ListTasks(%v): %v", goal.ID, err)
		}
		handoffs, err := fixture.store.ListOpenTaskHandoffsForGoal(context.Background(), goal.ID)
		if err != nil {
			t.Fatalf("ListOpenTaskHandoffsForGoal(%v): %v", goal.ID, err)
		}
		for _, task := range tasks {
			if task.Status != domain.TaskTodo || handoffs[task.ID] != nil {
				continue
			}
			if _, err := fixture.store.ClaimTask(context.Background(), task.ID, daemonTestSessionID(t, fixture.store, sessionID)); err != nil {
				t.Fatalf("ClaimTask(%v): %v", task.ID, err)
			}
		}
	}
	declared, err := fixture.store.DeclareTasks(
		context.Background(), fixture.emptyTaskGoal.ID, "contract-test", "decision-claimable-key",
		[]string{"decision task", "claimable task"}, []string{"decision description", "claimable description"},
	)
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if len(declared) != 2 {
		t.Fatalf("DeclareTasks returned %d tasks, want 2", len(declared))
	}

	raw := dispatchDecisionAskForContractTest(t, fixture, sessionID, fixture.emptyTaskGoal.ID, declared[0].ID)
	var response struct {
		ClaimableTasks []map[string]json.RawMessage `json:"claimable_tasks"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode decision.ask response: %v", err)
	}
	if len(response.ClaimableTasks) == 0 {
		t.Fatalf("decision.ask dropped claimable_tasks; response: %v", raw)
	}
	foundDeclared := false
	for _, task := range response.ClaimableTasks {
		for _, key := range []string{"id", "title", "goal_id"} {
			if _, ok := task[key]; !ok {
				t.Errorf("claimable_tasks item missing %v: %v", key, task)
			}
		}
		if _, ok := task["description"]; ok {
			t.Errorf("claimable_tasks item contains description: %v", task)
		}
		var id, goalID int64
		var title string
		if err := json.Unmarshal(task["id"], &id); err != nil {
			t.Errorf("decode claimable task id: %v", err)
		}
		if err := json.Unmarshal(task["title"], &title); err != nil {
			t.Errorf("decode claimable task title: %v", err)
		}
		if err := json.Unmarshal(task["goal_id"], &goalID); err != nil {
			t.Errorf("decode claimable task goal_id: %v", err)
		}
		if id == declared[1].ID && title == "claimable task" && goalID == fixture.emptyTaskGoal.ID {
			foundDeclared = true
		}
	}
	if !foundDeclared {
		t.Fatalf("claimable_tasks omitted declared task %v: %v", declared[1].ID, raw)
	}
}

func TestDecisionAskOmitsEmptyClaimableTasks(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	sessionID := "decision-ask-empty-contract-session"
	registerLiveGoalClaimSession(t, fixture, sessionID)
	decisionTasks, err := fixture.store.ListTasks(context.Background(), fixture.emptyTaskGoal.ID)
	if err != nil {
		t.Fatalf("ListTasks(%v): %v", fixture.emptyTaskGoal.ID, err)
	}
	if len(decisionTasks) == 0 {
		decisionTasks, err = fixture.store.DeclareTasks(
			context.Background(), fixture.emptyTaskGoal.ID, "contract-test", "decision-empty-contract",
			[]string{"decision task"}, []string{"decision task"},
		)
		if err != nil {
			t.Fatalf("DeclareTasks(%v): %v", fixture.emptyTaskGoal.ID, err)
		}
	}
	decisionTaskID := decisionTasks[0].ID
	goals, err := fixture.store.ListGoals(context.Background(), fixture.project.ID)
	if err != nil {
		t.Fatalf("ListGoals: %v", err)
	}
	for _, goal := range goals {
		tasks, err := fixture.store.ListTasks(context.Background(), goal.ID)
		if err != nil {
			t.Fatalf("ListTasks(%v): %v", goal.ID, err)
		}
		handoffs, err := fixture.store.ListOpenTaskHandoffsForGoal(context.Background(), goal.ID)
		if err != nil {
			t.Fatalf("ListOpenTaskHandoffsForGoal(%v): %v", goal.ID, err)
		}
		for _, task := range tasks {
			if task.Status != domain.TaskTodo || handoffs[task.ID] != nil {
				continue
			}
			if _, err := fixture.store.ClaimTask(context.Background(), task.ID, daemonTestSessionID(t, fixture.store, sessionID)); err != nil {
				t.Fatalf("ClaimTask(%v): %v", task.ID, err)
			}
		}
	}

	raw := dispatchDecisionAskForContractTest(t, fixture, sessionID, fixture.emptyTaskGoal.ID, decisionTaskID)
	var response map[string]json.RawMessage
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode decision.ask response: %v", err)
	}
	if _, ok := response["claimable_tasks"]; ok {
		t.Fatalf("decision.ask changed empty claimable_tasks response: %v", raw)
	}
}

type taskDeclareResponseForContractTest struct {
	ID          int64  `json:"id"`
	GoalID      int64  `json:"goal_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

func dispatchTaskDeclareForContractTest(t *testing.T, fixture goalListFixture, sessionID, idempotencyKey, title, description string) ([]byte, []taskDeclareResponseForContractTest) {
	t.Helper()
	params, err := json.Marshal(map[string]any{
		"goal_id":          fixture.emptyTaskGoal.ID,
		"agent":            "contract-test",
		"idempotency_key":  idempotencyKey,
		"titles":           []string{title},
		"descriptions":     []string{description},
		"agent_session_id": daemonTestSessionID(t, fixture.store, sessionID),
	})
	if err != nil {
		t.Fatalf("marshal task.declare params: %v", err)
	}
	raw, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "task.declare", Params: params})
	if err != nil {
		t.Fatalf("task.declare %v: %v", idempotencyKey, err)
	}
	var response []taskDeclareResponseForContractTest
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode task.declare %v response: %v; raw=%v", idempotencyKey, err, raw)
	}
	return raw, response
}

func dispatchDecisionAskForContractTest(t *testing.T, fixture goalListFixture, sessionID string, goalID, taskID int64) []byte {
	t.Helper()
	params, err := json.Marshal(map[string]any{
		"goal_id":                   goalID,
		"task_id":                   taskID,
		"question":                  "contract test decision",
		"wait_ms":                   0,
		"agent_session_id":          daemonTestSessionID(t, fixture.store, sessionID),
		"include_unapplied_answers": true,
	})
	if err != nil {
		t.Fatalf("marshal decision.ask params: %v", err)
	}
	raw, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "decision.ask", Params: params})
	if err != nil {
		t.Fatalf("decision.ask: %v", err)
	}
	return raw
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
		t.Fatalf("session-start hook: %v\noutput: %v", err, output)
	}
	if strings.Contains(output, "SECRET_GOAL_BODY") {
		t.Fatalf("session-start output contains goal body: %v", output)
	}
}

func TestContractN8GoalListHidesGoalsAwaitingCompletionApproval(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	for _, goalID := range []int64{fixture.emptyTaskGoal.ID, fixture.taskGoal.ID} {
		askOpenDecisionForContractTest(t, fixture, goalID, domain.KindCompletion)
	}
	response := goalListResponseForContractTest(t, fixture)
	for _, goalID := range []int64{fixture.emptyTaskGoal.ID, fixture.taskGoal.ID} {
		if goalPayloadExistsForContractTest(response.Goals, goalID) {
			t.Errorf("goal.list returned goal %v while completion approval is open", goalID)
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
			t.Fatalf("goal.list returned empty %v field", key)
		}
	}
}

func TestContractN10GoalListReturnsAwaitingApprovalCount(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	for _, goalID := range []int64{fixture.emptyTaskGoal.ID, fixture.taskGoal.ID} {
		askOpenDecisionForContractTest(t, fixture, goalID, domain.KindCompletion)
	}
	response := goalListResponseForContractTest(t, fixture)
	if response.AwaitingApprovalCount != 2 {
		t.Fatalf("awaiting_approval_count = %d, want 2", response.AwaitingApprovalCount)
	}
	if len(response.AwaitingApprovalGoalIDs) != 0 {
		t.Fatalf("goal.list returned deprecated awaiting_approval_goal_ids: %v", response.AwaitingApprovalGoalIDs)
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
		ID          int64  `json:"id"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(goal["tasks"], &listed); err != nil {
		t.Fatalf("decode listed tasks: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != tasks[0].ID {
		t.Fatalf("goal.list tasks = %+v, want task %v", listed, tasks[0].ID)
	}
	if want := strings.Repeat("あ", 120) + "…"; listed[0].Description != want {
		t.Fatalf("task description = %v, want %v", listed[0].Description, want)
	}
}

func TestContractN12TaskUpdateTruncatesDescription(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	sessionID := "task-update-description-contract-session"
	registerLiveGoalClaimSession(t, fixture, sessionID)
	fullDescription := strings.Repeat("あ", 300)
	tasks, err := fixture.store.DeclareTasks(
		context.Background(),
		fixture.emptyTaskGoal.ID,
		"contract-test",
		"task-update-description-contract",
		[]string{"task.update without answers", "task.update with answers"},
		[]string{fullDescription, fullDescription},
	)
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("DeclareTasks returned %d tasks, want 2", len(tasks))
	}

	wantDescription := strings.Repeat("あ", 120) + "…"
	for i, includeUnappliedAnswers := range []bool{false, true} {
		params, err := json.Marshal(map[string]any{
			"task_id":                   tasks[i].ID,
			"status":                    "todo",
			"agent_session_id":          daemonTestSessionID(t, fixture.store, sessionID),
			"include_unapplied_answers": includeUnappliedAnswers,
		})
		if err != nil {
			t.Fatalf("marshal task.update params: %v", err)
		}
		raw, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "task.update", Params: params})
		if err != nil {
			t.Fatalf("task.update include_unapplied_answers=%t: %v", includeUnappliedAnswers, err)
		}
		t.Logf("task.update include_unapplied_answers=%t len(raw)=%d", includeUnappliedAnswers, len(raw))

		payload := raw
		if includeUnappliedAnswers {
			var wrapped struct {
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(raw, &wrapped); err != nil {
				t.Fatalf("decode task.update wrapper: %v", err)
			}
			payload = wrapped.Data
		}

		var response struct {
			ID          int64  `json:"id"`
			GoalID      int64  `json:"goal_id"`
			Status      string `json:"status"`
			UpdatedAt   string `json:"updated_at"`
			Description string `json:"description"`
		}
		if err := json.Unmarshal(payload, &response); err != nil {
			t.Fatalf("decode task.update response: %v", err)
		}
		if response.Description != wantDescription {
			t.Errorf("task.update include_unapplied_answers=%t description rune count = %d, want 121", includeUnappliedAnswers, len([]rune(response.Description)))
		}
		if strings.Contains(response.Description, fullDescription) {
			t.Errorf("task.update include_unapplied_answers=%t response still contains the full description", includeUnappliedAnswers)
		}
		if response.ID != tasks[i].ID {
			t.Errorf("task.update include_unapplied_answers=%t id = %v, want %v", includeUnappliedAnswers, response.ID, tasks[i].ID)
		}
		if response.GoalID != fixture.emptyTaskGoal.ID {
			t.Errorf("task.update include_unapplied_answers=%t goal_id = %v, want %v", includeUnappliedAnswers, response.GoalID, fixture.emptyTaskGoal.ID)
		}
		if response.Status != "todo" {
			t.Errorf("task.update include_unapplied_answers=%t status = %v, want todo", includeUnappliedAnswers, response.Status)
		}
		if response.UpdatedAt == "" {
			t.Errorf("task.update include_unapplied_answers=%t updated_at is empty", includeUnappliedAnswers)
		}
	}
}

func TestContractN13GoalGetResponseSizeBreakdown(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	titles := []string{
		"goal.get size task 1",
		"goal.get size task 2",
		"goal.get size task 3",
		"goal.get size task 4",
		"goal.get size task 5",
		"goal.get size task 6",
		"goal.get size task 7",
		"goal.get size task 8",
	}
	fullDescription := strings.Repeat("あ", 300)
	descriptions := make([]string, len(titles))
	for i := range descriptions {
		descriptions[i] = fullDescription
	}
	tasks, err := fixture.store.DeclareTasks(context.Background(), fixture.emptyTaskGoal.ID, "contract-test", "goal-get-size-contract", titles, descriptions)
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if len(tasks) != len(titles) {
		t.Fatalf("DeclareTasks returned %d tasks, want %d", len(tasks), len(titles))
	}

	measure := func(label string, goalID int64) {
		t.Helper()
		params, err := json.Marshal(map[string]any{"goal_id": goalID})
		if err != nil {
			t.Fatalf("marshal goal.get params: %v", err)
		}
		raw, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "goal.get", Params: params})
		if err != nil {
			t.Fatalf("goal.get %v: %v", label, err)
		}
		var response struct {
			Goal  json.RawMessage `json:"goal"`
			Tasks json.RawMessage `json:"tasks"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			t.Fatalf("decode goal.get %v response: %v", label, err)
		}
		var returnedTasks []struct {
			Description string `json:"description"`
		}
		if err := json.Unmarshal(response.Tasks, &returnedTasks); err != nil {
			t.Fatalf("decode goal.get %v tasks: %v", label, err)
		}
		if len(returnedTasks) != len(titles) {
			t.Fatalf("goal.get %v returned %d tasks, want %d", label, len(returnedTasks), len(titles))
		}
		for i, task := range returnedTasks {
			if task.Description != fullDescription {
				t.Fatalf("goal.get %v task %d description rune count = %d, want %d", label, i, len([]rune(task.Description)), len([]rune(fullDescription)))
			}
		}
		goalOnly, err := json.Marshal(map[string]json.RawMessage{"goal": response.Goal})
		if err != nil {
			t.Fatalf("marshal goal.get %v goal-only payload: %v", label, err)
		}
		tasksOnly, err := json.Marshal(map[string]json.RawMessage{"tasks": response.Tasks})
		if err != nil {
			t.Fatalf("marshal goal.get %v tasks-only payload: %v", label, err)
		}
		t.Logf("goal.get shape=%v len(raw)=%d len({\"goal\":...})=%d len({\"tasks\":[...]})=%d", label, len(raw), len(goalOnly), len(tasksOnly))
	}

	measure("short-content", fixture.emptyTaskGoal.ID)

	longContent := strings.Repeat("goal body line\n", 350)
	longGoal, err := fixture.store.CreateGoal(context.Background(), fixture.project.ID, longContent, "human")
	if err != nil {
		t.Fatalf("CreateGoal long-content: %v", err)
	}
	longTasks, err := fixture.store.DeclareTasks(context.Background(), longGoal.ID, "contract-test", "goal-get-size-long-contract", titles, descriptions)
	if err != nil {
		t.Fatalf("DeclareTasks long-content: %v", err)
	}
	if len(longTasks) != len(titles) {
		t.Fatalf("DeclareTasks long-content returned %d tasks, want %d", len(longTasks), len(titles))
	}
	measure("long-content", longGoal.ID)
}

func TestContractB13TaskClaimReturnsFullTaskDescription(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	sessionID := "task-claim-description-contract-session"
	registerLiveGoalClaimSession(t, fixture, sessionID)
	fullDescription := strings.Repeat("あ", 300)
	tasks, err := fixture.store.DeclareTasks(context.Background(), fixture.emptyTaskGoal.ID, "contract-test", "task-claim-description-contract", []string{"full task.claim description"}, []string{fullDescription})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("DeclareTasks returned %d tasks, want 1", len(tasks))
	}
	params, err := json.Marshal(map[string]any{"task_id": tasks[0].ID, "agent_session_id": daemonTestSessionID(t, fixture.store, sessionID)})
	if err != nil {
		t.Fatalf("marshal task.claim params: %v", err)
	}
	raw, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "task.claim", Params: params})
	if err != nil {
		t.Fatalf("task.claim: %v", err)
	}
	var response struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode task.claim response: %v", err)
	}
	if response.Description != fullDescription {
		t.Fatalf("task.claim description rune count = %d, want %d", len([]rune(response.Description)), len([]rune(fullDescription)))
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
				t.Errorf("goal.list returned non-active task status %v", task.Status)
			}
		}
	}
}

func TestContractB3GoalListKeepsDecisionResponseKeys(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	params, err := json.Marshal(map[string]any{"cwd": fixture.project.RootPath})
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
			t.Errorf("goal.list response missing %v", key)
		}
	}
}

func TestContractB4GoalGetKeepsCompleteGoalData(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	params, err := json.Marshal(map[string]any{"goal_id": fixture.doneOnlyGoal.ID})
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
			t.Errorf("goal.get missing goal field %v (want %v)", key, wantValue)
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

func TestContractB6SessionStartHookMovesFixedInstructionsToMCP(t *testing.T) {
	output, err := runSessionStartHookForContractTest(t, `#!/usr/bin/env bash
case "$1 $2" in
  "daemon start") printf 'atct daemon ready: pid 123, http 127.0.0.1:8788\n' ;;
  "context -brief") printf 'ATCT: project atct / active goals 1 / todo tasks 1 / waiting answers 0\n' ;;
  *) exit 1 ;;
esac
`)
	if err != nil {
		t.Fatalf("session-start hook: %v\noutput: %v", err, output)
	}
	for _, line := range strings.Split(mcpshim.Instructions, "\n") {
		if line == "" {
			continue
		}
		if strings.Contains(output, line) {
			t.Errorf("session-start output contains MCP instruction %v: %v", line, output)
		}
	}
	const warning = "ATCT warning: daemon is listening at 127.0.0.1:8788; MCP endpoint is fixed at http://127.0.0.1:8787/mcp."
	if !strings.Contains(output, warning) {
		t.Fatalf("session-start output missing daemon address warning %v: %v", warning, output)
	}

	fixture := newMCPHTTPTestServer(t)
	defer fixture.server.Close()
	client := newMCPHTTPTestClient(fixture.server.URL + "/mcp")
	payload := client.initialize(t)
	result := mcpResult(t, payload)
	instructionsValue, ok := result["instructions"]
	if !ok {
		t.Fatalf("MCP initialize result has no instructions: %#v", result)
	}
	instructions, ok := instructionsValue.(string)
	if !ok {
		t.Fatalf("MCP initialize instructions = %#v, want string", instructionsValue)
	}
	if instructions != mcpshim.Instructions {
		t.Fatalf("MCP initialize instructions = %v, want shared instructions", instructions)
	}
	for _, marker := range []string{
		"This repository is registered with ATCT.",
		"An active goal is permission to work.",
		"See the `atct` skill for details.",
	} {
		if !strings.Contains(instructions, marker) {
			t.Errorf("MCP initialize instructions missing fixed instruction %v", marker)
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
		t.Fatalf("session-start hook: %v\noutput: %v", err, output)
	}
	t.Logf("unregistered repository hook output: %v", output)
	if output != "" {
		t.Fatalf("unregistered repository produced hook output: %v", output)
	}
}

func TestContractB8GoalListKeepsGoalsWithOpenDecision(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	for _, goalID := range []int64{fixture.active[0].ID, fixture.active[1].ID} {
		askOpenDecisionForContractTest(t, fixture, goalID, domain.KindDecision)
	}
	goals := goalListResponseForContractTest(t, fixture).Goals
	for _, goalID := range []int64{fixture.active[0].ID, fixture.active[1].ID} {
		if !goalPayloadExistsForContractTest(goals, goalID) {
			t.Errorf("goal.list omitted goal %v with an open decision", goalID)
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
	contractClaimedSessionID := daemonTestSessionID(t, fixture.store, "contract-claimed")
	if _, err := fixture.store.ClaimGoal(context.Background(), fixture.emptyTaskGoal.ID, contractClaimedSessionID); err != nil {
		t.Fatalf("ClaimGoal: %v", err)
	}
	goals := goalListResponseForContractTest(t, fixture).Goals
	derivedPayload := findGoalPayloadForContractTest(t, goals, derived.ID)
	claimedPayload := findGoalPayloadForContractTest(t, goals, fixture.emptyTaskGoal.ID)
	var gotDerived int64
	var gotClaimed int64
	if err := json.Unmarshal(derivedPayload["derived_from_goal_id"], &gotDerived); err != nil {
		t.Fatalf("decode derived_from_goal_id: %v", err)
	}
	if gotDerived != fixture.emptyTaskGoal.ID {
		t.Fatalf("derived_from_goal_id = %v, want %v", gotDerived, fixture.emptyTaskGoal.ID)
	}
	if err := json.Unmarshal(claimedPayload["claimed_by"], &gotClaimed); err != nil {
		t.Fatalf("decode claimed_by: %v", err)
	}
	if gotClaimed != contractClaimedSessionID {
		t.Fatalf("claimed_by = %v, want %v", gotClaimed, contractClaimedSessionID)
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
		ID          int64  `json:"id"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(goal["tasks"], &listed); err != nil {
		t.Fatalf("decode listed tasks: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != tasks[0].ID {
		t.Fatalf("goal.list tasks = %+v, want task %v", listed, tasks[0].ID)
	}
	if listed[0].Description != "first line" {
		t.Fatalf("short task description = %v, want first line without ellipsis", listed[0].Description)
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
	params, err := json.Marshal(map[string]any{"goal_id": fixture.emptyTaskGoal.ID})
	if err != nil {
		t.Fatalf("marshal goal.get params: %v", err)
	}
	raw, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "goal.get", Params: params})
	if err != nil {
		t.Fatalf("goal.get: %v", err)
	}
	var response struct {
		Tasks []struct {
			ID          int64  `json:"id"`
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
	t.Fatalf("goal.get did not return task %v", tasks[0].ID)
}

type contractGoalListResponse struct {
	Goals                   []map[string]json.RawMessage `json:"goals"`
	AwaitingApprovalCount   int                          `json:"awaiting_approval_count"`
	AwaitingApprovalGoalIDs json.RawMessage              `json:"awaiting_approval_goal_ids"`
}

func goalListResponseForContractTest(t *testing.T, fixture goalListFixture) contractGoalListResponse {
	t.Helper()
	params, err := json.Marshal(map[string]any{"cwd": fixture.project.RootPath})
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

func findGoalPayloadForContractTest(t *testing.T, goals []map[string]json.RawMessage, id int64) map[string]json.RawMessage {
	t.Helper()
	for _, goal := range goals {
		var gotID int64
		if err := json.Unmarshal(goal["id"], &gotID); err != nil {
			t.Fatalf("decode goal id: %v", err)
		}
		if gotID == id {
			return goal
		}
	}
	t.Fatalf("goal.list did not return goal %v", id)
	return nil
}

func goalPayloadExistsForContractTest(goals []map[string]json.RawMessage, id int64) bool {
	for _, goal := range goals {
		var gotID int64
		if err := json.Unmarshal(goal["id"], &gotID); err != nil {
			continue
		}
		if gotID == id {
			return true
		}
	}
	return false
}

func askOpenDecisionForContractTest(t *testing.T, fixture goalListFixture, goalID int64, kind domain.DecisionKind) {
	t.Helper()
	input := store.AskInput{
		GoalID:   goalID,
		Kind:     kind,
		Question: "contract test open decision",
	}
	if kind == domain.KindDecision {
		tasks, err := fixture.store.ListTasks(context.Background(), goalID)
		if err != nil {
			t.Fatalf("ListTasks(%v): %v", goalID, err)
		}
		if len(tasks) == 0 {
			declared, err := fixture.store.DeclareTasks(context.Background(), goalID, "contract-test", "decision-contract", []string{"decision task"}, []string{"decision task"})
			if err != nil {
				t.Fatalf("DeclareTasks(%v): %v", goalID, err)
			}
			tasks = declared
		}
		input.TaskID = tasks[0].ID
	}
	_, err := fixture.store.AskDecision(context.Background(), input)
	if err != nil {
		t.Fatalf("AskDecision(%v): %v", kind, err)
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
	source, err := os.ReadFile(filepath.Join(repoRoot, "hooks", "session-start"))
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

func TestHandoffSequenceRequiresReceiveBeforeRole(t *testing.T) {
	ctx := context.Background()

	dispatch := func(t *testing.T, fixture goalListFixture, method string, params map[string]any) (json.RawMessage, error) {
		t.Helper()
		rawParams, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal %v params: %v", method, err)
		}
		return fixture.daemon.dispatch(ctx, rpc.Request{Method: method, Params: rawParams})
	}
	register := func(t *testing.T, fixture goalListFixture, sessionIDs ...string) {
		t.Helper()
		for _, sessionID := range sessionIDs {
			_ = daemonTestSessionID(t, fixture.store, sessionID)
		}
	}
	role := func(t *testing.T, fixture goalListFixture, sessionID string) string {
		t.Helper()
		raw, err := dispatch(t, fixture, "session.role", map[string]any{"agent_session_id": daemonTestSessionID(t, fixture.store, sessionID)})
		if err != nil {
			t.Fatalf("session.role %v: %v", sessionID, err)
		}
		var response struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			t.Fatalf("decode session.role %v response %v: %v", sessionID, raw, err)
		}
		return response.Role
	}
	expectError := func(t *testing.T, fixture goalListFixture, method string, params map[string]any, wantMessage string) {
		t.Helper()
		_, err := dispatch(t, fixture, method, params)
		if err == nil {
			t.Fatalf("%v unexpectedly succeeded", method)
		}
		if !strings.Contains(err.Error(), wantMessage) {
			t.Fatalf("%v error = %v, want message containing %v", method, err, wantMessage)
		}
	}

	t.Run("positive-receive-before-role", func(t *testing.T) {
		fixture := newGoalListFixture(t)
		defer fixture.store.Close()

		commander := "handoff-sequence-a"
		subcommander := "handoff-sequence-b"
		executor := "handoff-sequence-c"
		register(t, fixture, commander, subcommander, executor)

		if _, err := dispatch(t, fixture, "project.claim", map[string]any{
			"project_id":       fixture.project.ID,
			"agent_session_id": daemonTestSessionID(t, fixture.store, commander),
		}); err != nil {
			t.Fatalf("project.claim: %v", err)
		}
		if got := role(t, fixture, commander); got != "commander" {
			t.Fatalf("commander role = %v, want %v", got, "commander")
		}

		if _, err := dispatch(t, fixture, "goal.handoff.request", map[string]any{
			"handoff_id":     "handoff-sequence-goal",
			"goal_id":        fixture.taskGoal.ID,
			"requested_by":   daemonTestSessionID(t, fixture.store, commander),
			"request_report": "delegate goal",
		}); err != nil {
			t.Fatalf("goal.handoff.request: %v", err)
		}
		if _, err := dispatch(t, fixture, "goal.handoff.receive", map[string]any{
			"goal_id":     fixture.taskGoal.ID,
			"received_by": daemonTestSessionID(t, fixture.store, subcommander),
		}); err != nil {
			t.Fatalf("goal.handoff.receive: %v", err)
		}
		if got := role(t, fixture, subcommander); got != "subcommander" {
			t.Fatalf("subcommander role = %v, want %v", got, "subcommander")
		}

		if _, err := dispatch(t, fixture, "handoff.request", map[string]any{
			"handoff_id":     "handoff-sequence-task",
			"task_id":        fixture.tasks[1].ID,
			"requested_by":   daemonTestSessionID(t, fixture.store, subcommander),
			"request_report": "delegate task",
		}); err != nil {
			t.Fatalf("handoff.request: %v", err)
		}
		if _, err := dispatch(t, fixture, "handoff.receive", map[string]any{
			"task_id":     fixture.tasks[1].ID,
			"received_by": daemonTestSessionID(t, fixture.store, executor),
		}); err != nil {
			t.Fatalf("handoff.receive: %v", err)
		}
		if got := role(t, fixture, executor); got != "executor" {
			t.Fatalf("executor role = %v, want %v", got, "executor")
		}
	})

	t.Run("n1-goal-claim-before-handoff-request", func(t *testing.T) {
		fixture := newGoalListFixture(t)
		defer fixture.store.Close()

		commander := "handoff-sequence-n1-a"
		register(t, fixture, commander)
		if _, err := dispatch(t, fixture, "project.claim", map[string]any{
			"project_id":       fixture.project.ID,
			"agent_session_id": daemonTestSessionID(t, fixture.store, commander),
		}); err != nil {
			t.Fatalf("project.claim: %v", err)
		}
		if _, err := dispatch(t, fixture, "goal.claim", map[string]any{
			"goal_id":          fixture.taskGoal.ID,
			"agent_session_id": daemonTestSessionID(t, fixture.store, commander),
		}); err != nil {
			t.Fatalf("goal.claim: %v", err)
		}
		expectError(t, fixture, "goal.handoff.request", map[string]any{
			"handoff_id":   "handoff-sequence-n1-goal",
			"goal_id":      fixture.taskGoal.ID,
			"requested_by": daemonTestSessionID(t, fixture.store, commander),
		}, "already open")
	})

	t.Run("n2-role-before-goal-handoff-receive", func(t *testing.T) {
		fixture := newGoalListFixture(t)
		defer fixture.store.Close()

		commander := "handoff-sequence-n2-a"
		subcommander := "handoff-sequence-n2-b"
		register(t, fixture, commander, subcommander)
		if _, err := dispatch(t, fixture, "project.claim", map[string]any{
			"project_id":       fixture.project.ID,
			"agent_session_id": daemonTestSessionID(t, fixture.store, commander),
		}); err != nil {
			t.Fatalf("project.claim: %v", err)
		}
		if _, err := dispatch(t, fixture, "goal.handoff.request", map[string]any{
			"handoff_id":   "handoff-sequence-n2-goal",
			"goal_id":      fixture.taskGoal.ID,
			"requested_by": daemonTestSessionID(t, fixture.store, commander),
		}); err != nil {
			t.Fatalf("goal.handoff.request: %v", err)
		}
		if got := role(t, fixture, subcommander); got != "executor" {
			t.Fatalf("pre-receive role = %v, want %v", got, "executor")
		}
	})

	t.Run("n3-unclaimed-goal-handoff-request", func(t *testing.T) {
		fixture := newGoalListFixture(t)
		defer fixture.store.Close()

		commander := "handoff-sequence-n3-a"
		sessionID := "handoff-sequence-n3-d"
		register(t, fixture, commander, sessionID)
		if _, err := dispatch(t, fixture, "project.claim", map[string]any{
			"project_id":       fixture.project.ID,
			"agent_session_id": daemonTestSessionID(t, fixture.store, commander),
		}); err != nil {
			t.Fatalf("project.claim: %v", err)
		}
		expectError(t, fixture, "goal.handoff.request", map[string]any{
			"handoff_id":   "handoff-sequence-n3-goal",
			"goal_id":      fixture.taskGoal.ID,
			"requested_by": daemonTestSessionID(t, fixture.store, sessionID),
		}, "caller does not hold a live claim on project")
	})

	t.Run("n4-unclaimed-task-handoff-request", func(t *testing.T) {
		fixture := newGoalListFixture(t)
		defer fixture.store.Close()

		sessionID := "handoff-sequence-n4-d"
		register(t, fixture, sessionID)
		expectError(t, fixture, "handoff.request", map[string]any{
			"handoff_id":   "handoff-sequence-n4-task",
			"task_id":      fixture.tasks[1].ID,
			"requested_by": daemonTestSessionID(t, fixture.store, sessionID),
		}, "caller does not hold an open received handoff for goal")
	})
}
