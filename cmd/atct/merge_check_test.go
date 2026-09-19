package main

import "testing"

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
