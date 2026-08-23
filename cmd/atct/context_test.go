package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

func TestParseArgsAcceptsContext(t *testing.T) {
	cfg, err := parseArgs([]string{"context"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.subcommand != "context" {
		t.Fatalf("subcommand = %q, want context", cfg.subcommand)
	}
}

func TestRenderContextOmitsInactiveGoals(t *testing.T) {
	got := renderContext([]contextGoal{
		{Goal: domain.Goal{ID: "done-goal", Content: "Finished", Status: domain.GoalDone}},
	}, nil)
	if got != "" {
		t.Fatalf("renderContext = %q, want empty output", got)
	}
}

func TestRenderContextIncludesGoalDetails(t *testing.T) {
	got := renderContext([]contextGoal{{
		Goal: domain.Goal{
			ID:      "goal-123",
			Content: "Ship context\n\nExpose the current ATCT state to the next session.",
			Status:  domain.GoalActive,
		},
	}}, nil)
	for _, want := range []string{
		"Goal: Ship context",
		"Description: Expose the current ATCT state to the next session.",
		"goal_id: goal-123",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("context does not contain %q: %q", want, got)
		}
	}
}

func TestRenderContextIncludesActionableTasksAndIDs(t *testing.T) {
	got := renderContext([]contextGoal{{
		Goal: domain.Goal{ID: "goal-123", Content: "Ship context", Status: domain.GoalActive},
		Tasks: []domain.Task{
			{ID: "task-todo", Title: "Declare tests", Status: domain.TaskTodo},
			{ID: "task-doing", Title: "Implement command", Status: domain.TaskDoing},
			{ID: "task-done", Title: "Review design", Status: domain.TaskDone},
		},
	}}, nil)
	for _, want := range []string{
		"[todo] Declare tests (task_id: task-todo)",
		"[doing] Implement command (task_id: task-doing)",
		"atct_task_claim",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("context does not contain %q: %q", want, got)
		}
	}
	if strings.Contains(got, "task-done") || strings.Contains(got, "Review design") {
		t.Fatalf("done task should not be listed: %q", got)
	}
	manyTasks := contextGoal{Goal: domain.Goal{ID: "goal-many", Content: "Many tasks", Status: domain.GoalActive}}
	for i := 0; i < 6; i++ {
		manyTasks.Tasks = append(manyTasks.Tasks, domain.Task{
			ID: "task-many-" + string(rune('a'+i)), Title: "Task", Status: domain.TaskTodo,
		})
	}
	manyOutput := renderContext([]contextGoal{manyTasks}, nil)
	if lines := len(strings.Split(strings.TrimSuffix(manyOutput, "\n"), "\n")); lines > 30 {
		t.Fatalf("context has %d lines, want at most 30: %q", lines, manyOutput)
	}
}

func TestRenderContextIncludesUnappliedDecisionsAndPollTool(t *testing.T) {
	got := renderContext([]contextGoal{{
		Goal: domain.Goal{ID: "goal-123", Content: "Ship context", Status: domain.GoalActive},
	}}, []domain.Decision{
		{
			ID: "decision-456", GoalID: "goal-123", Question: "Which output format should be used?",
			AnswerLabel: "compact", AnswerText: "Use compact lines.", Status: domain.DecisionAnswered,
			AnsweredAt: ptrTime(time.Unix(1, 0)),
		},
		{ID: "decision-applied", GoalID: "goal-123", Question: "Already handled", Status: domain.DecisionApplied},
	})
	for _, want := range []string{
		"decision_id: decision-456",
		"Which output format should be used?",
		"compact",
		"atct_decision_poll",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("context does not contain %q: %q", want, got)
		}
	}
	if strings.Contains(got, "decision-applied") || strings.Contains(got, "Already handled") {
		t.Fatalf("applied decision should not be listed: %q", got)
	}

	noTasks := renderContext([]contextGoal{{
		Goal: domain.Goal{ID: "goal-empty", Content: "No tasks", Status: domain.GoalActive},
	}}, nil)
	if !strings.Contains(noTasks, "atct_task_declare") || strings.Contains(noTasks, "atct_task_claim") {
		t.Fatalf("no-task state has wrong next tool: %q", noTasks)
	}
	withTodo := renderContext([]contextGoal{{
		Goal:  domain.Goal{ID: "goal-todo", Content: "Todo", Status: domain.GoalActive},
		Tasks: []domain.Task{{ID: "task-todo", Title: "A task", Status: domain.TaskTodo}},
	}}, nil)
	if !strings.Contains(withTodo, "atct_task_claim") || strings.Contains(withTodo, "atct_task_declare") {
		t.Fatalf("todo state has wrong next tool: %q", withTodo)
	}
}

func TestRenderContextMarksDefaultAppliedAnswers(t *testing.T) {
	decisions := []domain.Decision{
		{
			ID: "decision-human", GoalID: "goal-123", Question: "Which human answer?",
			AnswerLabel: "human", AnswerText: "human answer", Status: domain.DecisionAnswered,
			AnsweredAt: ptrTime(time.Unix(1, 0)),
		},
		{
			ID: "decision-default", GoalID: "goal-123", Question: "Which default answer?",
			AnswerLabel: "default", AnswerText: "default answer", Status: domain.DecisionAnswered,
			AnsweredAt: ptrTime(time.Unix(2, 0)), DefaultAppliedAt: ptrTime(time.Unix(3, 0)),
		},
	}
	goals := []contextGoal{{
		Goal: domain.Goal{ID: "goal-123", Content: "Ship context", Status: domain.GoalActive},
	}}
	for name, got := range map[string]string{
		"current": renderContext(goals, decisions),
		"legacy":  renderContextLegacy(goals, decisions),
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(got, "answer: human - human answer") {
				t.Fatalf("context omitted human answer: %q", got)
			}
			if strings.Contains(got, "answer: human - human answer (default applied because no one answered)") {
				t.Fatalf("context marked human answer as default-applied: %q", got)
			}
			if !strings.Contains(got, "answer: default - default answer (default applied because no one answered)") {
				t.Fatalf("context omitted default-applied marker: %q", got)
			}
		})
	}
}

func TestContextIsSilentForUnregisteredProject(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	got, err := contextText(dir, t.TempDir())
	if err != nil {
		t.Fatalf("contextText: %v", err)
	}
	if got != "" {
		t.Fatalf("contextText = %q, want empty output", got)
	}
}

func TestContextBriefIncludesClaimedCommander(t *testing.T) {
	fixture := newContextCheckFixture(t)
	if _, err := fixture.db.ClaimProject(context.Background(), fixture.goal.ProjectID, "atct-commander"); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}

	got, err := contextBriefTextForProject(fixture.dir, fixture.cwd, "", false)
	if err != nil {
		t.Fatalf("contextBriefTextForProject: %v", err)
	}
	if !strings.Contains(got, "commander atct-com…") {
		t.Fatalf("brief omitted visibly truncated commander: %q", got)
	}
}

func TestContextBriefIsSilentForUnregisteredProject(t *testing.T) {
	dbDir := t.TempDir()
	db, err := store.Open(filepath.Join(dbDir, "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}
	dir := t.TempDir()

	got, err := contextBriefTextForProject(dir, t.TempDir(), "", false)
	if err != nil {
		t.Fatalf("contextBriefTextForProject: %v", err)
	}
	if got != "" {
		t.Fatalf("contextBriefTextForProject = %q, want empty output", got)
	}
}

func TestContextBriefShowsAbsentCommanderForUnclaimedProject(t *testing.T) {
	fixture := newContextCheckFixture(t)

	got, err := contextBriefTextForProject(fixture.dir, fixture.cwd, "", false)
	if err != nil {
		t.Fatalf("contextBriefTextForProject: %v", err)
	}
	if !strings.Contains(got, "commander absent") {
		t.Fatalf("brief omitted absent commander: %q", got)
	}
}

func TestRenderContextDistinguishesClaimedTasks(t *testing.T) {
	t.Setenv(atctAgentSessionIDEnv, "run-self")

	got := renderContext([]contextGoal{{
		Goal: domain.Goal{ID: "goal-claim", Content: "Claimed goal", Status: domain.GoalActive},
		Tasks: []domain.Task{
			{ID: "task-free", Title: "Unclaimed", Status: domain.TaskTodo},
			{ID: "task-self", Title: "Self claim", Status: domain.TaskDoing, ClaimedBy: "run-self"},
			{ID: "task-other", Title: "Other claim", Status: domain.TaskDoing, ClaimedBy: "run-other"},
		},
	}}, nil)

	if !strings.Contains(got, "- [todo] Unclaimed (task_id: task-free)") {
		t.Fatalf("unclaimed task missing from context:\n%s", got)
	}
	if !strings.Contains(got, "- [claimed by this agent session] Self claim (task_id: task-self)") {
		t.Fatalf("current-run claim marker missing from context:\n%s", got)
	}
	if !strings.Contains(got, "- [claimed] Other claim (task_id: task-other)") {
		t.Fatalf("other-run claim marker missing from context:\n%s", got)
	}
}

func TestRenderContextIncludesAllActiveGoals(t *testing.T) {
	goals := make([]contextGoal, 0, 4)
	for i := 1; i <= 4; i++ {
		goals = append(goals, contextGoal{Goal: domain.Goal{
			ID:      fmt.Sprintf("goal-%d", i),
			Content: fmt.Sprintf("Goal %d\n\nvisible", i),
			Status:  domain.GoalActive,
		}})
	}

	got := renderContext(goals, nil)
	for i := 1; i <= 4; i++ {
		if !strings.Contains(got, fmt.Sprintf("Goal: Goal %d", i)) {
			t.Errorf("goal %d missing from context:\n%s", i, got)
		}
	}
}

func TestRenderContextOmitsGoalOmissionSummary(t *testing.T) {
	goals := make([]contextGoal, 0, 4)
	for i := 1; i <= 4; i++ {
		goals = append(goals, contextGoal{Goal: domain.Goal{
			ID:      fmt.Sprintf("goal-%d", i),
			Content: fmt.Sprintf("Goal %d\n\nvisible", i),
			Status:  domain.GoalActive,
		}})
	}

	got := renderContext(goals, nil)
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "... and ") && strings.HasSuffix(line, " goals") {
			t.Fatalf("goal omission summary should be absent from context:\n%s", got)
		}
	}
}

func TestRenderContextTruncatesLongDescriptionByRune(t *testing.T) {
	longDescription := strings.Repeat("日", 101)
	goals := []contextGoal{
		{Goal: domain.Goal{
			ID:      "goal-long-description",
			Content: "Long description\n\n" + longDescription,
			Status:  domain.GoalActive,
		}},
	}
	for i := 2; i <= 4; i++ {
		goals = append(goals, contextGoal{Goal: domain.Goal{
			ID:      fmt.Sprintf("goal-%d", i),
			Content: fmt.Sprintf("Goal %d", i),
			Status:  domain.GoalActive,
		}})
	}

	got := renderContext(goals, nil)
	want := "Description: " + strings.Repeat("日", 100) + "…"
	if !strings.Contains(got, want+"\n") {
		t.Fatalf("long description should be truncated to 100 runes with an ellipsis:\n%s", got)
	}
	if strings.Contains(got, "\uFFFD") {
		t.Fatalf("long Japanese description should not be corrupted:\n%s", got)
	}
}

func TestRenderContextKeepsShortDescriptionWithoutEllipsis(t *testing.T) {
	shortDescription := strings.Repeat("文", 100)
	goals := []contextGoal{
		{Goal: domain.Goal{
			ID:      "goal-short-description",
			Content: "Short description\n\n" + shortDescription,
			Status:  domain.GoalActive,
		}},
	}
	for i := 2; i <= 4; i++ {
		goals = append(goals, contextGoal{Goal: domain.Goal{
			ID:      fmt.Sprintf("goal-%d", i),
			Content: fmt.Sprintf("Goal %d", i),
			Status:  domain.GoalActive,
		}})
	}

	got := renderContext(goals, nil)
	want := "Description: " + shortDescription
	if !strings.Contains(got, want+"\n") {
		t.Fatalf("description at the 100-rune limit should be unchanged:\n%s", got)
	}
	if strings.Contains(got, want+"…") {
		t.Fatalf("description at the 100-rune limit should not have an ellipsis:\n%s", got)
	}
}

func TestRenderContextLimitsTasksAndReportsOmissions(t *testing.T) {
	tasks := make([]domain.Task, 0, 6)
	for i := 1; i <= 6; i++ {
		tasks = append(tasks, domain.Task{
			ID:     fmt.Sprintf("task-%d", i),
			Title:  fmt.Sprintf("Task %d", i),
			Status: domain.TaskTodo,
		})
	}

	got := renderContext([]contextGoal{{
		Goal:  domain.Goal{ID: "goal-tasks", Content: "Task goal", Status: domain.GoalActive},
		Tasks: tasks,
	}}, nil)
	for i := 1; i <= 5; i++ {
		if !strings.Contains(got, fmt.Sprintf("Task %d", i)) {
			t.Errorf("task %d missing from context:\n%s", i, got)
		}
	}
	if strings.Contains(got, "Task 6") {
		t.Fatalf("sixth task should be omitted from context:\n%s", got)
	}
	if !strings.Contains(got, "... and 1 more tasks") {
		t.Fatalf("task omission count missing from context:\n%s", got)
	}
}

func TestRenderContextOmitsEmptyDescription(t *testing.T) {
	got := renderContext([]contextGoal{{
		Goal: domain.Goal{ID: "goal-empty-description", Content: "No description", Status: domain.GoalActive},
	}}, nil)

	if strings.Contains(got, "Description:") {
		t.Fatalf("empty description should omit its line:\n%s", got)
	}
}

func TestRenderContextKeepsAllDecisionsOutsideCaps(t *testing.T) {
	goals := make([]contextGoal, 0, 4)
	for i := 1; i <= 4; i++ {
		goals = append(goals, contextGoal{Goal: domain.Goal{
			ID:      fmt.Sprintf("goal-%d", i),
			Content: fmt.Sprintf("Goal %d", i),
			Status:  domain.GoalActive,
		}})
	}
	decisions := make([]domain.Decision, 0, 6)
	for i := 1; i <= 6; i++ {
		decisions = append(decisions, domain.Decision{
			ID:          fmt.Sprintf("decision-%d", i),
			GoalID:      "goal-4",
			Question:    fmt.Sprintf("Question %d", i),
			AnswerLabel: "Yes",
			AnswerText:  fmt.Sprintf("Answer %d", i),
			Status:      domain.DecisionAnswered,
		})
	}

	got := renderContext(goals, decisions)
	for i := 1; i <= 6; i++ {
		if !strings.Contains(got, fmt.Sprintf("decision-%d", i)) {
			t.Errorf("decision %d missing from context:\n%s", i, got)
		}
	}
}

type contextCheckFixture struct {
	dir  string
	cwd  string
	db   *store.Store
	goal domain.Goal
}

func newContextCheckFixture(t *testing.T) contextCheckFixture {
	t.Helper()

	dir := t.TempDir()
	cwd := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	project, err := db.CreateProject(context.Background(), "context-check", cwd)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := db.CreateGoal(context.Background(), project.ID, "Wake up\n\ncheck for work", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	return contextCheckFixture{
		dir:  dir,
		cwd:  cwd,
		db:   db,
		goal: goal,
	}
}

func (f contextCheckFixture) addUnappliedAnswer(t *testing.T) {
	t.Helper()

	// An active decision has to name the task it is holding up.
	tasks, err := f.db.DeclareTasks(context.Background(), f.goal.ID, "agent", "blocked-batch", []string{"blocked task"}, []string{"Complete the blocked task before applying its answer."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	decision, err := f.db.AskDecision(context.Background(), store.AskInput{
		GoalID:         f.goal.ID,
		TaskID:         tasks[0].ID,
		Kind:           domain.KindDecision,
		Question:       "Which path should be taken?",
		AgentSessionID: "run-context-check",
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if _, err := f.db.AnswerDecision(context.Background(), store.AnswerInput{
		DecisionID: decision.ID,
		AnswerText: "the path",
	}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
}

func (f contextCheckFixture) addTask(t *testing.T, title string) domain.Task {
	t.Helper()

	tasks, err := f.db.DeclareTasks(context.Background(), f.goal.ID, "agent", "declare-"+title, []string{title}, []string{"Complete the task titled " + title + " and verify its context behavior."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	return tasks[0]
}

func (f contextCheckFixture) runInCWD(t *testing.T, fn func() error) error {
	t.Helper()

	oldCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(f.cwd); err != nil {
		t.Fatalf("Chdir(%q): %v", f.cwd, err)
	}
	defer func() {
		if err := os.Chdir(oldCWD); err != nil {
			t.Errorf("restore cwd %q: %v", oldCWD, err)
		}
	}()
	return fn()
}

func captureContextCheckStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = writer
	defer func() {
		os.Stdout = oldStdout
		_ = reader.Close()
	}()

	callErr := fn()
	if err := writer.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return string(output), callErr
}

func TestContextCheckReturnsZeroForUnappliedAnswer(t *testing.T) {
	fixture := newContextCheckFixture(t)
	fixture.addUnappliedAnswer(t)

	output, exitCode, err := contextCommand(fixture.dir, fixture.cwd)
	if err != nil {
		t.Fatalf("contextCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	if output == "" {
		t.Fatal("contextCommand output is empty for unapplied answer")
	}
}

func TestContextCheckReturnsZeroForUnclaimedTask(t *testing.T) {
	fixture := newContextCheckFixture(t)
	fixture.addTask(t, "unclaimed")

	_, exitCode, err := contextCommand(fixture.dir, fixture.cwd)
	if err != nil {
		t.Fatalf("contextCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
}

func TestContextCheckReturnsZeroForUndeclaredActiveGoal(t *testing.T) {
	fixture := newContextCheckFixture(t)

	_, exitCode, err := contextCommand(fixture.dir, fixture.cwd)
	if err != nil {
		t.Fatalf("contextCommand: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
}

func TestContextCheckIgnoresDoingTasks(t *testing.T) {
	fixture := newContextCheckFixture(t)
	task := fixture.addTask(t, "already doing")
	if _, err := fixture.db.UpdateTask(context.Background(), task.ID, domain.TaskDoing, ""); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	_, exitCode, err := contextCommand(fixture.dir, fixture.cwd)
	if err != nil {
		t.Fatalf("contextCommand: %v", err)
	}
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
	}
}

func TestContextCheckReturnsOneWhenNothingNeedsDoing(t *testing.T) {
	fixture := newContextCheckFixture(t)
	task := fixture.addTask(t, "finished")
	if _, err := fixture.db.UpdateTask(context.Background(), task.ID, domain.TaskDone, ""); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	_, exitCode, err := contextCommand(fixture.dir, fixture.cwd)
	if err != nil {
		t.Fatalf("contextCommand: %v", err)
	}
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
	}
}

func TestContextCheckIsSilent(t *testing.T) {
	cfg, err := parseArgs([]string{"context", "--check"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if !cfg.contextCheck {
		t.Fatal("contextCheck = false, want true")
	}

	fixture := newContextCheckFixture(t)
	fixture.addTask(t, "unclaimed")
	output, err := captureContextCheckStdout(t, func() error {
		return fixture.runInCWD(t, func() error {
			return runContextCheck(fixture.dir)
		})
	})
	if err != nil {
		t.Fatalf("runContextCheck: %v", err)
	}
	if output != "" {
		t.Fatalf("stdout = %q, want empty", output)
	}
}

func TestContextWithoutCheckKeepsOutput(t *testing.T) {
	fixture := newContextCheckFixture(t)
	fixture.addTask(t, "unclaimed")
	output, err := captureContextCheckStdout(t, func() error {
		return fixture.runInCWD(t, func() error {
			return runContext(fixture.dir)
		})
	})
	if err != nil {
		t.Fatalf("runContext: %v", err)
	}
	if !strings.Contains(output, "ATCT context") || !strings.Contains(output, "unclaimed") {
		t.Fatalf("stdout = %q, want legacy context output", output)
	}
}

func TestContextCheckReturnsOneForUnregisteredProject(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	_, exitCode, err := contextCommand(dir, t.TempDir())
	if err != nil {
		t.Fatalf("contextCommand: %v", err)
	}
	if exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", exitCode)
	}
}

type projectSelectionFixture struct {
	dir      string
	cwdA     string
	cwdB     string
	db       *store.Store
	projectA domain.Project
	projectB domain.Project
	goalA    domain.Goal
	goalB    domain.Goal
}

func newProjectSelectionFixture(t *testing.T) projectSelectionFixture {
	t.Helper()

	dir := t.TempDir()
	cwdA := t.TempDir()
	cwdB := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "atct.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	projectA, err := db.CreateProject(context.Background(), "project-a", cwdA)
	if err != nil {
		t.Fatalf("CreateProject(project-a): %v", err)
	}
	projectB, err := db.CreateProject(context.Background(), "project-b", cwdB)
	if err != nil {
		t.Fatalf("CreateProject(project-b): %v", err)
	}
	goalA, err := db.CreateGoal(context.Background(), projectA.ID, "Goal A\n\nproject A goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal(project-a): %v", err)
	}
	goalB, err := db.CreateGoal(context.Background(), projectB.ID, "Goal B\n\nproject B goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal(project-b): %v", err)
	}

	return projectSelectionFixture{
		dir:      dir,
		cwdA:     cwdA,
		cwdB:     cwdB,
		db:       db,
		projectA: projectA,
		projectB: projectB,
		goalA:    goalA,
		goalB:    goalB,
	}
}

func (f projectSelectionFixture) addPendingDecision(t *testing.T, goalID, question, agentSessionID string) {
	t.Helper()

	// An active decision has to name the task it is holding up.
	tasks, err := f.db.DeclareTasks(context.Background(), goalID, "agent", "blocked-"+agentSessionID, []string{"blocked task"}, []string{"Complete the blocked task after the pending decision is handled."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	decision, err := f.db.AskDecision(context.Background(), store.AskInput{
		GoalID:         goalID,
		TaskID:         tasks[0].ID,
		Kind:           domain.KindDecision,
		Question:       question,
		AgentSessionID: agentSessionID,
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if _, err := f.db.AnswerDecision(context.Background(), store.AnswerInput{
		DecisionID: decision.ID,
		AnswerText: "answer",
	}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
}

func TestContextProjectFlagSelectsNamedProject(t *testing.T) {
	fixture := newProjectSelectionFixture(t)

	cfg, err := parseArgs([]string{"context", "--project", "project-b"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.projectName != "project-b" || !cfg.projectSpecified {
		t.Fatalf("project selection = (%q, %t), want (project-b, true)", cfg.projectName, cfg.projectSpecified)
	}

	got, err := contextTextForProject(fixture.dir, fixture.cwdA, cfg.projectName, cfg.projectSpecified)
	if err != nil {
		t.Fatalf("contextTextForProject: %v", err)
	}
	if !strings.Contains(got, "Goal: Goal B") || strings.Contains(got, "Goal: Goal A") {
		t.Fatalf("context output = %q, want only project-b goal", got)
	}
}

func TestContextWithoutProjectUsesCWD(t *testing.T) {
	fixture := newProjectSelectionFixture(t)

	got, err := contextTextForProject(fixture.dir, fixture.cwdA, "", false)
	if err != nil {
		t.Fatalf("contextTextForProject: %v", err)
	}
	if !strings.Contains(got, "Goal: Goal A") || strings.Contains(got, "Goal: Goal B") {
		t.Fatalf("context output = %q, want only cwd project goal", got)
	}
}

func TestContextProjectFlagRejectsUnknownProject(t *testing.T) {
	fixture := newProjectSelectionFixture(t)

	_, err := contextTextForProject(fixture.dir, fixture.cwdA, "missing", true)
	if err == nil {
		t.Fatal("contextTextForProject succeeded for an unknown project")
	}
	if !strings.Contains(err.Error(), `project "missing" not found`) {
		t.Fatalf("error = %v, want unknown project error", err)
	}
}

func TestPendingProjectFlagSelectsNamedProject(t *testing.T) {
	fixture := newProjectSelectionFixture(t)
	fixture.addPendingDecision(t, fixture.goalA.ID, "Decision A", "run-a")
	fixture.addPendingDecision(t, fixture.goalB.ID, "Decision B", "run-b")

	cfg, err := parseArgs([]string{"pending", "--project", "project-b"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if cfg.projectName != "project-b" || !cfg.projectSpecified {
		t.Fatalf("project selection = (%q, %t), want (project-b, true)", cfg.projectName, cfg.projectSpecified)
	}

	got, err := pendingTextForProject(fixture.dir, fixture.cwdA, cfg.projectName, cfg.projectSpecified)
	if err != nil {
		t.Fatalf("pendingTextForProject: %v", err)
	}
	if !strings.Contains(got, "Decision B") || strings.Contains(got, "Decision A") {
		t.Fatalf("pending output = %q, want only project-b decision", got)
	}
}

func TestPendingDefaultAndUnknownProject(t *testing.T) {
	fixture := newProjectSelectionFixture(t)
	fixture.addPendingDecision(t, fixture.goalA.ID, "Decision A", "run-a")
	fixture.addPendingDecision(t, fixture.goalB.ID, "Decision B", "run-b")

	got, err := pendingTextForProject(fixture.dir, fixture.cwdA, "", false)
	if err != nil {
		t.Fatalf("pendingTextForProject: %v", err)
	}
	if !strings.Contains(got, "Decision A") || strings.Contains(got, "Decision B") {
		t.Fatalf("pending output = %q, want only cwd project decision", got)
	}

	if _, err := pendingTextForProject(fixture.dir, fixture.cwdA, "missing", true); err == nil {
		t.Fatal("pendingTextForProject succeeded for an unknown project")
	} else if !strings.Contains(err.Error(), `project "missing" not found`) {
		t.Fatalf("error = %v, want unknown project error", err)
	}
}

func TestContextCheckProjectFlagSelectsOnlyRequestedProject(t *testing.T) {
	fixture := newProjectSelectionFixture(t)

	cfg, err := parseArgs([]string{"context", "--check", "--project", "project-b"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if !cfg.contextCheck || cfg.projectName != "project-b" || !cfg.projectSpecified {
		t.Fatalf("config = %+v, want --check and project-b", cfg)
	}

	got, exitCode, err := contextCommandForProject(fixture.dir, fixture.cwdA, cfg.projectName, cfg.projectSpecified)
	if err != nil {
		t.Fatalf("contextCommandForProject: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0 for an active goal with work", exitCode)
	}
	if !strings.Contains(got, "Goal: Goal B") || strings.Contains(got, "Goal: Goal A") {
		t.Fatalf("context output = %q, want only project-b goal", got)
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}
