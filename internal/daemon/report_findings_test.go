package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/mcpshim"
	"github.com/michiomochi/atct/internal/store"
)

const findingsHeading = "ATCT 検出"

func TestAppendFindings(t *testing.T) {
	if got := appendFindings("report", ""); got != "report" {
		t.Fatalf("empty note: got %q", got)
	}
	if got := appendFindings("  \n", "- x"); got != "  \n" {
		t.Fatalf("blank report: got %q", got)
	}
	got := appendFindings("report", "- x")
	want := "report\n\n---\nATCT 検出（daemon 記録。報告者の記述ではない）:\n- x"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func projectRootOf(t *testing.T, s *store.Store, goalID int64) string {
	t.Helper()
	ctx := context.Background()
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatal(err)
	}
	projects, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range projects {
		if p.ID == goal.ProjectID {
			return p.RootPath
		}
	}
	t.Fatalf("project for goal %d not found", goalID)
	return ""
}

// addGoalWorktree checks out the goal branch as <root>/.worktrees/<goalID>.
func addGoalWorktree(t *testing.T, root string, goalID int64) string {
	t.Helper()
	wt := filepath.Join(root, ".worktrees", strconv.FormatInt(goalID, 10))
	gitIn(t, root, "worktree", "add", "-q", wt, goalBranchName(goalID))
	return wt
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// goalReviewRequest runs request -> receive -> review.request for the claimed
// goal, returning the review request error and the stored report.
func goalReviewRequest(t *testing.T, fixture goalHandoffRPCTestFixture, report string) (error, string) {
	t.Helper()
	ctx := context.Background()
	client := mcpshim.NewClient(fixture.socketPath)
	id := "findings"
	var h store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.request", map[string]any{"handoff_id": id, "goal_id": fixture.claimedGoalID, "requested_by": fixture.requesterID}, &h); err != nil {
		t.Fatal(err)
	}
	var received handoffReceiveResponse
	if err := client.Call(ctx, "goal.handoff.receive", map[string]any{"handoff_id": id, "goal_id": fixture.claimedGoalID, "received_by": fixture.receiverID}, &received); err != nil {
		t.Fatal(err)
	}
	err := client.Call(ctx, "goal.handoff.review.request", map[string]any{"handoff_id": id, "goal_id": fixture.claimedGoalID, "requested_by": fixture.receiverID, "review_request_report": report}, &h)
	stored, getErr := fixture.store.GetGoalHandoff(ctx, id)
	if getErr != nil {
		t.Fatal(getErr)
	}
	return err, stored.ReviewRequestReport
}

func markGoalTaskDone(t *testing.T, fixture goalHandoffRPCTestFixture) {
	t.Helper()
	ctx := context.Background()
	tasks, err := fixture.store.CreateTasks(ctx, fixture.claimedGoalID, "commander", "findings-task", []string{"t"}, []string{"d"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.UpdateTask(ctx, tasks[0].ID, domain.TaskDone, fixture.receiverID); err != nil {
		t.Fatal(err)
	}
}

func TestGoalReviewRequestRefusesTrackedChanges(t *testing.T) {
	for _, staged := range []bool{false, true} {
		name := "unstaged"
		if staged {
			name = "staged"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newGoalHandoffRPCTestFixture(t)
			root := projectRootOf(t, fixture.store, fixture.claimedGoalID)
			newGoalBranchRepo(t, root, fixture.claimedGoalID, "README2.md")
			wt := addGoalWorktree(t, root, fixture.claimedGoalID)
			writeFile(t, filepath.Join(wt, "README.md"), "changed")
			if staged {
				gitIn(t, wt, "add", "README.md")
			}
			err, _ := goalReviewRequest(t, fixture, "ready")
			if err == nil || !strings.Contains(err.Error(), "README.md") || !strings.Contains(err.Error(), "commit") {
				t.Fatalf("error = %v, want refusal naming README.md and commit", err)
			}
		})
	}
}

func TestGoalReviewRequestRecordsUntrackedFiles(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	root := projectRootOf(t, fixture.store, fixture.claimedGoalID)
	newGoalBranchRepo(t, root, fixture.claimedGoalID, "README2.md")
	wt := addGoalWorktree(t, root, fixture.claimedGoalID)
	writeFile(t, filepath.Join(wt, "scratch.txt"), "x")
	err, report := goalReviewRequest(t, fixture, "ready")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(report, "ready") || !strings.Contains(report, findingsHeading) || !strings.Contains(report, "scratch.txt") {
		t.Fatalf("report = %q, want heading and untracked path appended", report)
	}
}

func TestGoalReviewRequestRecordsBranchNotAhead(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	root := projectRootOf(t, fixture.store, fixture.claimedGoalID)
	newGoalBranchRepo(t, root, 999)
	gitIn(t, root, "branch", goalBranchName(fixture.claimedGoalID), "main")
	addGoalWorktree(t, root, fixture.claimedGoalID)
	markGoalTaskDone(t, fixture)
	err, report := goalReviewRequest(t, fixture, "ready")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, findingsHeading) || !strings.Contains(report, "branch に main より新しい commit が無い") {
		t.Fatalf("report = %q, want branch-not-ahead finding", report)
	}
}

func TestGoalReviewRequestUnchangedWhenBranchAhead(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	root := projectRootOf(t, fixture.store, fixture.claimedGoalID)
	newGoalBranchRepo(t, root, fixture.claimedGoalID, "README2.md")
	addGoalWorktree(t, root, fixture.claimedGoalID)
	markGoalTaskDone(t, fixture)
	err, report := goalReviewRequest(t, fixture, "ready")
	if err != nil {
		t.Fatal(err)
	}
	if report != "ready" {
		t.Fatalf("report = %q, want unchanged", report)
	}
}

func TestGoalReviewRequestUnchangedWithoutWorktree(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	root := projectRootOf(t, fixture.store, fixture.claimedGoalID)
	newGoalBranchRepo(t, root, fixture.claimedGoalID, "README2.md")
	err, report := goalReviewRequest(t, fixture, "ready")
	if err != nil {
		t.Fatal(err)
	}
	if report != "ready" {
		t.Fatalf("report = %q, want unchanged", report)
	}
}

func TestTaskReviewRequestRecordsDirtyWorktreeWithoutRefusing(t *testing.T) {
	fixture := newTaskHandoffRPCTestFixture(t)
	ctx := context.Background()
	goalID, err := fixture.store.GetTaskGoalID(ctx, fixture.claimedTaskID)
	if err != nil {
		t.Fatal(err)
	}
	root := projectRootOf(t, fixture.store, goalID)
	newGoalBranchRepo(t, root, goalID, "README2.md")
	wt := addGoalWorktree(t, root, goalID)
	writeFile(t, filepath.Join(wt, "README.md"), "changed")
	writeFile(t, filepath.Join(wt, "scratch.txt"), "x")

	client := mcpshim.NewClient(fixture.socketPath)
	var h store.TaskHandoff
	if err := client.Call(ctx, "task.handoff.request", map[string]any{"handoff_id": "tf", "task_id": fixture.claimedTaskID, "requested_by": fixture.requesterID}, &h); err != nil {
		t.Fatal(err)
	}
	var received handoffReceiveResponse
	if err := client.Call(ctx, "task.handoff.receive", map[string]any{"handoff_id": "tf", "task_id": fixture.claimedTaskID, "received_by": fixture.receiverID}, &received); err != nil {
		t.Fatal(err)
	}
	if err := client.Call(ctx, "task.handoff.review.request", map[string]any{"handoff_id": "tf", "task_id": fixture.claimedTaskID, "requested_by": fixture.receiverID, "review_request_report": "done"}, &h); err != nil {
		t.Fatalf("task review request refused: %v", err)
	}
	stored, err := fixture.store.GetTaskHandoff(ctx, "tf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored.ReviewRequestReport, "done") || !strings.Contains(stored.ReviewRequestReport, findingsHeading) || !strings.Contains(stored.ReviewRequestReport, "2 件") {
		t.Fatalf("report = %q, want heading and 2 paths appended", stored.ReviewRequestReport)
	}
}
