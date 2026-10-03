package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/mcpshim"
	"github.com/michiomochi/atct/internal/store"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newGoalBranchRepo makes a repo on main with one commit and a wt/goal-<id>
// branch that adds the given files.
func newGoalBranchRepo(t *testing.T, root string, goalID int64, added ...string) {
	newGoalBranchRepoOn(t, root, "main", goalID, added...)
}

func newGoalBranchRepoOn(t *testing.T, root, base string, goalID int64, added ...string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "init", "-q", "-b", base)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-q", "-m", "base")
	gitIn(t, root, "checkout", "-q", "-b", goalBranchName(goalID))
	for _, f := range added {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("y"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-q", "--allow-empty", "-m", "goal work")
	gitIn(t, root, "checkout", "-q", base)
}

func TestGoalBranchAddedSpecFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	newGoalBranchRepo(t, root, 7, "doc/specs/a.md", "doc/plans/b.md", "docs/superpowers/specs/c.md", "internal/x/doc/specs/d.md", "doc/specsx/e.md", "README2.md")
	got, err := goalBranchAddedSpecFiles(context.Background(), root, 7)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"doc/plans/b.md", "doc/specs/a.md", "docs/superpowers/specs/c.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestGoalBranchAddedSpecFilesIgnoresModifiedAndMissing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	newGoalBranchRepo(t, root, 8, "README2.md")
	if got, err := goalBranchAddedSpecFiles(context.Background(), root, 8); err != nil || len(got) != 0 {
		t.Fatalf("clean branch: got %v, %v", got, err)
	}
	if got, err := goalBranchAddedSpecFiles(context.Background(), root, 99); err != nil || len(got) != 0 {
		t.Fatalf("missing branch: got %v, %v", got, err)
	}
	if got, err := goalBranchAddedSpecFiles(context.Background(), filepath.Join(t.TempDir(), "nope"), 8); err != nil || len(got) != 0 {
		t.Fatalf("missing repo: got %v, %v", got, err)
	}
}

func TestGoalHandoffReviewRequestRefusesAddedSpecFiles(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	ctx := context.Background()
	client := mcpshim.NewClient(fixture.socketPath)
	goal, err := fixture.store.GetGoal(ctx, fixture.claimedGoalID)
	if err != nil {
		t.Fatal(err)
	}
	projects, err := fixture.store.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range projects {
		if p.ID == goal.ProjectID {
			newGoalBranchRepo(t, p.RootPath, goal.ID, "doc/specs/leak.md")
		}
	}
	var h store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.request", map[string]any{"handoff_id": "spec-files", "goal_id": goal.ID, "requested_by": fixture.requesterID}, &h); err != nil {
		t.Fatal(err)
	}
	var received handoffReceiveResponse
	if err := client.Call(ctx, "goal.handoff.receive", map[string]any{"handoff_id": "spec-files", "goal_id": goal.ID, "received_by": fixture.receiverID}, &received); err != nil {
		t.Fatal(err)
	}
	err = client.Call(ctx, "goal.handoff.review.request", map[string]any{"handoff_id": "spec-files", "goal_id": goal.ID, "requested_by": fixture.receiverID, "review_request_report": "ready"}, &h)
	if err == nil || !strings.Contains(err.Error(), "doc/specs/leak.md") {
		t.Fatalf("review request error = %v, want refusal naming doc/specs/leak.md", err)
	}
	if !strings.Contains(err.Error(), "atct_goal_update_request_report") || !strings.Contains(err.Error(), "git rm") {
		t.Fatalf("refusal %q lacks remediation steps", err)
	}
}

func addedSpecFiles(t *testing.T, root string, goalID int64) []string {
	t.Helper()
	got, err := goalBranchAddedSpecFiles(context.Background(), root, goalID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestGoalBranchAddedSpecFilesBaseResolution(t *testing.T) {
	t.Run("master only", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "repo")
		newGoalBranchRepoOn(t, root, "master", 7, "doc/plans/p.md")
		if got := addedSpecFiles(t, root, 7); !reflect.DeepEqual(got, []string{"doc/plans/p.md"}) {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("origin only", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "repo")
		newGoalBranchRepo(t, root, 7, "docs/superpowers/plans/p.md")
		gitIn(t, root, "update-ref", "refs/remotes/origin/main", "main")
		gitIn(t, root, "checkout", "-q", "wt/goal-7")
		gitIn(t, root, "branch", "-D", "main")
		if got := addedSpecFiles(t, root, 7); !reflect.DeepEqual(got, []string{"docs/superpowers/plans/p.md"}) {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("no base", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "repo")
		newGoalBranchRepoOn(t, root, "develop", 7, "doc/specs/a.md")
		if got := addedSpecFiles(t, root, 7); len(got) != 0 {
			t.Fatalf("got %v, want none", got)
		}
	})
}

func TestGoalBranchAddedSpecFilesAfterRemovalAndModification(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	newGoalBranchRepo(t, root, 7, "doc/specs/a.md")
	gitIn(t, root, "checkout", "-q", "wt/goal-7")
	gitIn(t, root, "rm", "-q", "doc/specs/a.md")
	gitIn(t, root, "commit", "-q", "-m", "remove spec")
	if got := addedSpecFiles(t, root, 7); len(got) != 0 {
		t.Fatalf("after git rm: got %v, want none", got)
	}
	root = filepath.Join(t.TempDir(), "repo2")
	newGoalBranchRepo(t, root, 8, "README.md") // modifies, does not add
	if got := addedSpecFiles(t, root, 8); len(got) != 0 {
		t.Fatalf("modified only: got %v, want none", got)
	}
}

func TestGoalBranchAddedSpecFilesBrokenRepoFailsOpen(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := addedSpecFiles(t, root, 7); len(got) != 0 {
		t.Fatalf("got %v, want none", got)
	}
}

// advanceMain commits a new file on main after the goal branch was cut.
func advanceMain(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "main-only.md"), []byte("m"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "add", ".")
	gitIn(t, root, "commit", "-q", "-m", "main moves")
}

func TestGoalBranchMissingBase(t *testing.T) {
	ctx := context.Background()
	t.Run("main advanced", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "repo")
		newGoalBranchRepo(t, root, 7)
		advanceMain(t, root)
		if base, missing := goalBranchMissingBase(ctx, root, 7); !missing || base != "refs/heads/main" {
			t.Fatalf("got %q, %v; want refs/heads/main, true", base, missing)
		}
	})
	t.Run("branch ahead of main", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "repo")
		newGoalBranchRepo(t, root, 7, "README2.md")
		if _, missing := goalBranchMissingBase(ctx, root, 7); missing {
			t.Fatal("branch ahead of main reported missing")
		}
	})
	t.Run("branch equal to main", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "repo")
		newGoalBranchRepo(t, root, 7)
		gitIn(t, root, "branch", "wt/goal-9", "main")
		if _, missing := goalBranchMissingBase(ctx, root, 9); missing {
			t.Fatal("branch equal to main reported missing")
		}
	})
	t.Run("merged after advance", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "repo")
		newGoalBranchRepo(t, root, 7)
		advanceMain(t, root)
		gitIn(t, root, "checkout", "-q", "wt/goal-7")
		gitIn(t, root, "merge", "-q", "main", "--no-edit")
		if _, missing := goalBranchMissingBase(ctx, root, 7); missing {
			t.Fatal("merged branch reported missing")
		}
	})
	t.Run("no branch", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "repo")
		newGoalBranchRepo(t, root, 7)
		advanceMain(t, root)
		if _, missing := goalBranchMissingBase(ctx, root, 99); missing {
			t.Fatal("missing branch reported missing base")
		}
	})
	t.Run("no repo", func(t *testing.T) {
		if _, missing := goalBranchMissingBase(ctx, filepath.Join(t.TempDir(), "nope"), 7); missing {
			t.Fatal("missing repo reported missing base")
		}
	})
	t.Run("no main or master", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "repo")
		newGoalBranchRepoOn(t, root, "develop", 7)
		if _, missing := goalBranchMissingBase(ctx, root, 7); missing {
			t.Fatal("no base reported missing")
		}
	})
}

func TestGoalHandoffReviewRequestRefusesStaleBranch(t *testing.T) {
	fixture := newGoalHandoffRPCTestFixture(t)
	ctx := context.Background()
	client := mcpshim.NewClient(fixture.socketPath)
	goal, err := fixture.store.GetGoal(ctx, fixture.claimedGoalID)
	if err != nil {
		t.Fatal(err)
	}
	projects, err := fixture.store.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var root string
	for _, p := range projects {
		if p.ID == goal.ProjectID {
			root = p.RootPath
		}
	}
	newGoalBranchRepo(t, root, goal.ID, "README2.md")
	advanceMain(t, root)
	var h store.GoalHandoff
	if err := client.Call(ctx, "goal.handoff.request", map[string]any{"handoff_id": "stale", "goal_id": goal.ID, "requested_by": fixture.requesterID}, &h); err != nil {
		t.Fatal(err)
	}
	var received handoffReceiveResponse
	if err := client.Call(ctx, "goal.handoff.receive", map[string]any{"handoff_id": "stale", "goal_id": goal.ID, "received_by": fixture.receiverID}, &received); err != nil {
		t.Fatal(err)
	}
	review := map[string]any{"handoff_id": "stale", "goal_id": goal.ID, "requested_by": fixture.receiverID, "review_request_report": "ready"}
	err = client.Call(ctx, "goal.handoff.review.request", review, &h)
	if err == nil || !strings.Contains(err.Error(), "git merge main --no-edit") {
		t.Fatalf("review request error = %v, want refusal with merge instruction", err)
	}
	gitIn(t, root, "checkout", "-q", goalBranchName(goal.ID))
	gitIn(t, root, "merge", "-q", "main", "--no-edit")
	if err := client.Call(ctx, "goal.handoff.review.request", review, &h); err != nil {
		t.Fatalf("review request after merging main: %v", err)
	}
}
