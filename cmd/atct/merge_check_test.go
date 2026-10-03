package main

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGoalBranchMergedIntoCurrentBranch(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    int64
	}{
		{"a plain merge", "git merge wt/goal-283", 283},
		{"no fast forward", "git merge --no-ff wt/goal-283 -m 'Merge goal 283'", 283},
		{"a remote-tracking branch", "git merge origin/wt/goal-1042", 1042},
		{"cherry-picking a goal's commit", "git cherry-pick wt/goal-283~1", 283},
		{"rebasing onto a goal branch", "git rebase wt/goal-283", 283},
		{"pulling a goal branch", "git pull . wt/goal-283", 283},
		{"inside a longer command line", "cd /repo && git merge --no-ff wt/goal-9 && echo done", 9},

		// The other direction is how a worktree keeps up, and it is allowed.
		{"merging main into a worktree", "git merge main", 0},
		// Cleanup names the branch without moving any history onto main.
		{"deleting the branch", "git branch -d wt/goal-283", 0},
		{"removing the worktree", "git worktree remove .worktrees/283", 0},
		{"looking at the branch", "git log wt/goal-283", 0},
		{"diffing against the branch", "git diff main wt/goal-283", 0},
		{"not git at all", "echo git merge wt/goal-283", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := goalBranchMergedIntoCurrentBranch(tt.command); got != tt.want {
				t.Fatalf("goalBranchMergedIntoCurrentBranch(%q) = %d, want %d", tt.command, got, tt.want)
			}
		})
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestMergeCheckDecisionByHead(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "main")
	worktree := filepath.Join(root, "wt")
	trial := filepath.Join(root, "trial")
	runGit(t, root, "init", "-b", "main", main)
	runGit(t, main, "commit", "--allow-empty", "-m", "init")
	runGit(t, main, "worktree", "add", "-b", "wt/goal-309", worktree)
	runGit(t, main, "worktree", "add", "--detach", trial, "main")
	db := t.TempDir() // no approval recorded

	tests := []struct {
		name    string
		cwd     string
		command string
		denied  bool
	}{
		{"main into a goal worktree, branch named in the message", worktree, `git merge main -m "merge main into wt/goal-309"`, false},
		{"goal branch into main", main, "git merge --no-ff wt/goal-309", true},
		{"goal branch into a detached trial worktree", trial, "git merge --no-ff --no-edit wt/goal-309", false},
		{"git -C pointing at main from a worktree", worktree, "git -C " + main + " merge wt/goal-309", true},
		{"cd before the merge", worktree, "cd " + main + " && git merge wt/goal-309", true},
		{"git checkout main before the merge", worktree, "git checkout main && git merge wt/goal-309", true},
		{"git switch main before the merge", worktree, "git switch main && git merge wt/goal-309", true},
		{"git -C and switch before the merge", worktree, "git -C " + worktree + " switch main; git merge wt/goal-309", true},
		{"not a git directory", root, "git merge wt/goal-309", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, denied := mergeCheckDecision(db, tt.cwd, tt.command)
			if denied != tt.denied {
				t.Fatalf("denied = %v (%s), want %v", denied, reason, tt.denied)
			}
		})
	}
}
