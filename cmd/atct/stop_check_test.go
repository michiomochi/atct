package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

func TestStopCheckTextBlocksOnlyScopedRoleWork(t *testing.T) {
	dir, projectRoot := newPendingFixture(t)
	s := openPendingStore(t, dir)
	ctx := context.Background()
	project, err := s.ResolveProject(ctx, projectRoot)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}

	executorGoal := stopCheckGoal(t, s, ctx, project.ID, "executor goal")
	executorTask := stopCheckTask(t, s, ctx, executorGoal.ID, "executor task")
	executorSession := stopCheckSession(t, s, ctx, project.ID)
	if _, err := s.ClaimTask(ctx, executorTask.ID, executorSession); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	subcommanderGoal := stopCheckGoal(t, s, ctx, project.ID, "subcommander goal")
	subcommanderSession := stopCheckSession(t, s, ctx, project.ID)
	if _, err := s.ClaimGoal(ctx, subcommanderGoal.ID, subcommanderSession); err != nil {
		t.Fatalf("ClaimGoal: %v", err)
	}

	idleGoal := stopCheckGoal(t, s, ctx, project.ID, "idle goal")
	idleTask := stopCheckTask(t, s, ctx, idleGoal.ID, "idle task")

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	tests := []struct {
		name      string
		scope     stopCheckScope
		wantBlock bool
	}{
		{
			name:      "executor blocks its received task",
			scope:     stopCheckScope{Role: "executor", ProjectID: project.ID, GoalID: executorGoal.ID, TaskID: executorTask.ID},
			wantBlock: true,
		},
		{
			name:      "subcommander blocks its received goal",
			scope:     stopCheckScope{Role: "subcommander", ProjectID: project.ID, GoalID: subcommanderGoal.ID},
			wantBlock: true,
		},
		{
			name:      "commander blocks active project goal",
			scope:     stopCheckScope{Role: "commander", ProjectID: project.ID},
			wantBlock: true,
		},
		{
			name:      "executor ignores another task without a received handoff",
			scope:     stopCheckScope{Role: "executor", ProjectID: project.ID, GoalID: idleGoal.ID, TaskID: idleTask.ID},
			wantBlock: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := stopCheckText(dir, tt.scope)
			if err != nil {
				t.Fatalf("stopCheckText: %v", err)
			}
			if got := output != ""; got != tt.wantBlock {
				t.Fatalf("blocked = %v, want %v; output = %q", got, tt.wantBlock, output)
			}
			if tt.wantBlock && !strings.Contains(output, `"decision":"block"`) {
				t.Fatalf("output = %q, want Codex block JSON", output)
			}
		})
	}
}

func TestStopCheckResultBlocksWhenStoreCannotBeRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	output := stopCheckResult(path, stopCheckScope{Role: "commander", ProjectID: 1})
	var response stopCheckResponse
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		t.Fatalf("decode response: %v; output = %q", err, output)
	}
	if response.Decision != "block" || !strings.Contains(response.Reason, "ATCT stop-check failed") {
		t.Fatalf("response = %#v", response)
	}
}

func TestParseStopCheck(t *testing.T) {
	cfg, err := parseArgs([]string{"stop-check", "--role", "executor", "--project", "7", "--goal", "16", "--task", "46"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if got := stopCheckScopeFromConfig(cfg); got != (stopCheckScope{Role: "executor", ProjectID: 7, GoalID: 16, TaskID: 46}) {
		t.Fatalf("scope = %#v", got)
	}
}

func stopCheckGoal(t *testing.T, s *store.Store, ctx context.Context, projectID int64, title string) domain.Goal {
	t.Helper()
	goal, err := s.CreateGoal(ctx, projectID, title, "human")
	if err != nil {
		t.Fatalf("CreateGoal %q: %v", title, err)
	}
	return goal
}

func stopCheckTask(t *testing.T, s *store.Store, ctx context.Context, goalID int64, title string) domain.Task {
	t.Helper()
	tasks, err := s.CreateTasks(ctx, goalID, "agent", title, []string{title}, []string{"finish " + title})
	if err != nil {
		t.Fatalf("CreateTasks %q: %v", title, err)
	}
	return tasks[0]
}

func stopCheckSession(t *testing.T, s *store.Store, ctx context.Context, projectID int64) int64 {
	t.Helper()
	sessionID, err := s.RegisterAgentSessionInProject(ctx, 0, projectID)
	if err != nil {
		t.Fatalf("RegisterAgentSessionInProject: %v", err)
	}
	return sessionID
}
