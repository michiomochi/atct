package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestCreateGoalWithHumanCreatorStartsActiveWithoutApproval(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "human", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	goal, err := s.CreateGoal(ctx, project.ID, "human goal\n\ndescription", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if goal.Status != domain.GoalActive {
		t.Fatalf("status = %q, want %q", goal.Status, domain.GoalActive)
	}
	if goal.Creator != "human" {
		t.Fatalf("creator = %q, want human", goal.Creator)
	}
	decisions, err := s.ListOpenDecisions(ctx, goal.ID)
	if err != nil {
		t.Fatalf("ListOpenDecisions: %v", err)
	}
	if len(decisions) != 0 {
		t.Fatalf("open decisions = %d, want 0", len(decisions))
	}
}

func TestCreateGoalWithAgentCreatorProposesAndAsksWithoutDefault(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "agent", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	goal, err := s.CreateGoal(ctx, project.ID, "agent goal\n\ndescription", "agent")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	if goal.Status != domain.GoalProposed {
		t.Fatalf("status = %q, want %q", goal.Status, domain.GoalProposed)
	}
	if goal.Creator != "agent" {
		t.Fatalf("creator = %q, want agent", goal.Creator)
	}
	decisions, err := s.ListOpenDecisions(ctx, goal.ID)
	if err != nil {
		t.Fatalf("ListOpenDecisions: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("open decisions = %d, want 1", len(decisions))
	}
	decision := decisions[0]
	if decision.Kind != domain.KindGoalApproval {
		t.Fatalf("kind = %q, want %q", decision.Kind, domain.KindGoalApproval)
	}
	if decision.DefaultOption != "" || decision.DefaultAfterMs != nil {
		t.Fatalf("default = %q/%v, want no default", decision.DefaultOption, decision.DefaultAfterMs)
	}
}

func TestCreateGoalTreatsEmptyAndUnknownCreatorsAsAgent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "creator-normalization", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	for _, creator := range []string{"", "automation"} {
		goal, err := s.CreateGoal(ctx, project.ID, "normalized creator\n\ndescription", creator)
		if err != nil {
			t.Fatalf("CreateGoal(%q): %v", creator, err)
		}
		if goal.Creator != "agent" || goal.Status != domain.GoalProposed {
			t.Fatalf("CreateGoal(%q) = creator %q/status %q, want agent/proposed", creator, goal.Creator, goal.Status)
		}
		decisions, err := s.ListOpenDecisions(ctx, goal.ID)
		if err != nil {
			t.Fatalf("ListOpenDecisions(%q): %v", creator, err)
		}
		if len(decisions) != 1 || decisions[0].DefaultOption != "" || decisions[0].DefaultAfterMs != nil {
			t.Fatalf("decisions for %q = %+v, want one decision without a default", creator, decisions)
		}
	}
}

func TestApproveGoalActivatesGoalAndAppliesApproval(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "approve", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "approve goal\n\ndescription", "agent")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	decisions, err := s.ListOpenDecisions(ctx, goal.ID)
	if err != nil {
		t.Fatalf("ListOpenDecisions: %v", err)
	}

	approved, err := s.ApproveGoal(ctx, decisions[0].ID)
	if err != nil {
		t.Fatalf("ApproveGoal: %v", err)
	}
	if approved.Status != domain.GoalActive {
		t.Fatalf("status = %q, want %q", approved.Status, domain.GoalActive)
	}
	decision, err := s.GetDecision(ctx, decisions[0].ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if decision.Status != domain.DecisionApplied || decision.AnswerLabel != "approve" {
		t.Fatalf("decision = %q/%q, want applied/approve", decision.Status, decision.AnswerLabel)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "approval-tasks", []string{"work after approval"}, []string{"Run after approval."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	claimed, err := s.ClaimTask(ctx, tasks[0].ID, "approved-agent-session")
	if err != nil {
		t.Fatalf("ClaimTask after approval: %v", err)
	}
	if claimed.ClaimedBy != "approved-agent-session" {
		t.Fatalf("claimed_by = %q, want approved-agent-session", claimed.ClaimedBy)
	}
}

func TestRejectGoalDropsGoalAndKeepsReason(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "reject", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "reject goal\n\ndescription", "agent")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	decisions, err := s.ListOpenDecisions(ctx, goal.ID)
	if err != nil {
		t.Fatalf("ListOpenDecisions: %v", err)
	}

	const reason = "needs a human-owned scope"
	if err := s.RejectGoal(ctx, decisions[0].ID, reason); err != nil {
		t.Fatalf("RejectGoal: %v", err)
	}
	dropped, err := s.GetGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	if dropped.Status != domain.GoalDropped {
		t.Fatalf("status = %q, want %q", dropped.Status, domain.GoalDropped)
	}
	decision, err := s.GetDecision(ctx, decisions[0].ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if decision.Status != domain.DecisionAnswered || decision.AnswerLabel != "reject" || !strings.Contains(decision.AnswerText, reason) {
		t.Fatalf("decision = %q/%q/%q, want answered/reject/%q", decision.Status, decision.AnswerLabel, decision.AnswerText, reason)
	}
}

func TestClaimTaskRejectsTaskForProposedGoal(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "claim", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "proposed goal\n\ndescription", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	tasks, err := s.DeclareTasks(ctx, goal.ID, "agent", "proposed-task", []string{"work"}, []string{"Work after approval."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE goals SET status = ? WHERE id = ?", string(domain.GoalProposed), goal.ID); err != nil {
		t.Fatalf("set goal proposed: %v", err)
	}

	_, err = s.ClaimTask(ctx, tasks[0].ID, "agent-session")
	if !errors.Is(err, ErrGoalNotActive) {
		t.Fatalf("ClaimTask error = %v, want ErrGoalNotActive", err)
	}
	if !strings.Contains(err.Error(), "承認されていない") || !strings.Contains(err.Error(), "承認") {
		t.Fatalf("ClaimTask error = %q, want actionable approval guidance", err)
	}
}

func TestDeclareTasksRejectsTaskForProposedGoalWithoutCreatingTasks(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "declare-proposed", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "proposed goal\n\ndescription", "agent")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	_, err = s.DeclareTasks(ctx, goal.ID, "agent", "proposed-tasks", []string{"work one", "work two"}, []string{"Work one after approval.", "Work two after approval."})
	if !errors.Is(err, ErrGoalNotActive) {
		t.Fatalf("DeclareTasks error = %v, want ErrGoalNotActive", err)
	}
	if !strings.Contains(err.Error(), "承認されていない") || !strings.Contains(err.Error(), "承認") {
		t.Fatalf("DeclareTasks error = %q, want actionable approval guidance", err)
	}
	tasks, err := s.ListTasks(ctx, goal.ID)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("tasks = %d, want 0", len(tasks))
	}
}

func TestDeclareTasksAllowsActiveGoal(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "declare-active", t.TempDir())
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "active goal\n\ndescription", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	tasks, err := s.DeclareTasks(ctx, goal.ID, "human", "active-tasks", []string{"work one", "work two"}, []string{"Run work one.", "Run work two."})
	if err != nil {
		t.Fatalf("DeclareTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("tasks = %d, want 2", len(tasks))
	}
}
