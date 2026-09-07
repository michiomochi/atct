package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
)

func TestDecisionLifecycleOwnsCanonicalHumanDecisionBlocker(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "blocker-project", "/repos/blocker-project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goal, err := s.CreateGoal(ctx, project.ID, "blocked goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	commanderID := registerNamedTestAgentSession(t, s, "blocker-commander", 0)
	if _, err := s.ClaimProject(ctx, project.ID, commanderID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}

	decision, err := s.AskDecision(ctx, AskInput{
		GoalID:         goal.ID,
		Kind:           domain.KindDecision,
		Question:       "Which migration order is safe?",
		Options:        []domain.Option{{Label: "ordered"}, {Label: "parallel"}},
		AgentSessionID: commanderID,
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	open, err := s.ListOpenOrchestrationBlockers(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListOpenOrchestrationBlockers after ask: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("open blockers after ask = %d, want 1 (%#v)", len(open), open)
	}
	blocker := open[0]
	if blocker.Kind != OrchestrationBlockerHumanDecision {
		t.Fatalf("blocker kind = %q, want %q", blocker.Kind, OrchestrationBlockerHumanDecision)
	}
	if blocker.SourceID != fmt.Sprint(decision.ID) {
		t.Fatalf("blocker source_id = %q, want %d", blocker.SourceID, decision.ID)
	}
	if blocker.OwnerRole != OrchestrationBlockerOwnerCommander {
		t.Fatalf("blocker owner_role = %q, want commander", blocker.OwnerRole)
	}
	if blocker.Instruction == "" || blocker.Generation == "" || blocker.BlockerID == "" {
		t.Fatalf("blocker identity/instruction incomplete: %#v", blocker)
	}

	reconciliation, err := s.ReconcileWorkflow(ctx, WorkflowEventQuery{ProjectID: project.ID})
	if err != nil {
		t.Fatalf("ReconcileWorkflow: %v", err)
	}
	if len(reconciliation.Blockers) != 1 || reconciliation.Blockers[0].BlockerID != blocker.BlockerID {
		t.Fatalf("reconciliation blockers = %#v, want blocker %q", reconciliation.Blockers, blocker.BlockerID)
	}

	if _, err := s.AnswerDecision(ctx, AnswerInput{DecisionID: decision.ID, AnswerLabel: "ordered"}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
	open, err = s.ListOpenOrchestrationBlockers(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListOpenOrchestrationBlockers after answer: %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("open blockers after answer = %#v, want none", open)
	}
	settled, err := s.GetOrchestrationBlocker(ctx, blocker.BlockerID)
	if err != nil {
		t.Fatalf("GetOrchestrationBlocker after answer: %v", err)
	}
	if settled.ResolvedAt == nil {
		t.Fatalf("human decision blocker resolved_at is nil: %#v", settled)
	}
}

func TestDependencyMergeBlockerReportIsOwnerCheckedAndIdempotent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "merge-blocker-project", "/repos/merge-blocker-project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	commanderID := registerNamedTestAgentSession(t, s, "merge-blocker-commander", 0)
	if _, err := s.ClaimProject(ctx, project.ID, commanderID); err != nil {
		t.Fatalf("ClaimProject: %v", err)
	}
	otherID := registerNamedTestAgentSession(t, s, "merge-blocker-other", 0)
	scopeKey := ProjectOrchestrationScopeKey(project.ID)
	input := OrchestrationBlockerReportInput{
		ProjectID:      project.ID,
		ScopeKey:       scopeKey,
		Kind:           OrchestrationBlockerDependencyMerge,
		SourceID:       "main:abc123:goal:17",
		Generation:     "merge-generation-1",
		OwnerRole:      OrchestrationBlockerOwnerCommander,
		Instruction:    "integrate main commit abc123 before resuming goal 17",
		AgentSessionID: commanderID,
	}

	first, created, err := s.ReportOrchestrationBlocker(ctx, input)
	if err != nil {
		t.Fatalf("ReportOrchestrationBlocker first: %v", err)
	}
	if !created {
		t.Fatal("first blocker report was not marked created")
	}
	secondInput := input
	secondInput.Instruction = "integrate abc123 before resuming goal 17"
	second, created, err := s.ReportOrchestrationBlocker(ctx, secondInput)
	if err != nil {
		t.Fatalf("ReportOrchestrationBlocker retry: %v", err)
	}
	if created {
		t.Fatal("idempotent blocker retry was marked created")
	}
	if second.BlockerID != first.BlockerID {
		t.Fatalf("retry blocker_id = %q, want %q", second.BlockerID, first.BlockerID)
	}
	if second.Instruction != secondInput.Instruction {
		t.Fatalf("retry instruction = %q, want updated instruction %q", second.Instruction, secondInput.Instruction)
	}

	open, err := s.ListOpenOrchestrationBlockers(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListOpenOrchestrationBlockers: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("open merge blockers = %d, want 1 (%#v)", len(open), open)
	}

	next := input
	next.Generation = "merge-generation-2"
	next.SourceID = input.SourceID
	third, created, err := s.ReportOrchestrationBlocker(ctx, next)
	if err != nil {
		t.Fatalf("ReportOrchestrationBlocker new generation: %v", err)
	}
	if !created || third.BlockerID == first.BlockerID {
		t.Fatalf("new generation report = %#v, created=%v; want fresh blocker", third, created)
	}
	open, err = s.ListOpenOrchestrationBlockers(ctx, project.ID)
	if err != nil {
		t.Fatalf("ListOpenOrchestrationBlockers after generation: %v", err)
	}
	if len(open) != 2 {
		t.Fatalf("open merge blockers after new generation = %d, want 2", len(open))
	}

	unauthorized := input
	unauthorized.AgentSessionID = otherID
	if _, _, err := s.ReportOrchestrationBlocker(ctx, unauthorized); !errors.Is(err, ErrOrchestrationBlockerUnauthorized) {
		t.Fatalf("foreign blocker report error = %v, want ErrOrchestrationBlockerUnauthorized", err)
	}
	if _, err := s.ResolveOrchestrationBlocker(ctx, OrchestrationBlockerResolveInput{
		BlockerID:      first.BlockerID,
		AgentSessionID: otherID,
	}); !errors.Is(err, ErrOrchestrationBlockerUnauthorized) {
		t.Fatalf("foreign blocker resolve error = %v, want ErrOrchestrationBlockerUnauthorized", err)
	}

	resolved, err := s.ResolveOrchestrationBlocker(ctx, OrchestrationBlockerResolveInput{
		BlockerID:      first.BlockerID,
		AgentSessionID: commanderID,
	})
	if err != nil {
		t.Fatalf("ResolveOrchestrationBlocker: %v", err)
	}
	if resolved.ResolvedAt == nil {
		t.Fatalf("resolved blocker has nil resolved_at: %#v", resolved)
	}
	repeated, err := s.ResolveOrchestrationBlocker(ctx, OrchestrationBlockerResolveInput{
		BlockerID:      first.BlockerID,
		AgentSessionID: commanderID,
	})
	if err != nil {
		t.Fatalf("repeated ResolveOrchestrationBlocker: %v", err)
	}
	if repeated.BlockerID != first.BlockerID || repeated.ResolvedAt == nil {
		t.Fatalf("repeated resolve = %#v, want same settled blocker", repeated)
	}
}

func TestHumanDecisionBlockerResolvesForWithdrawalAndDefaultSettlement(t *testing.T) {
	tests := []struct {
		name   string
		settle func(context.Context, *Store, domain.Decision) error
	}{
		{
			name: "withdrawal",
			settle: func(ctx context.Context, s *Store, decision domain.Decision) error {
				return s.WithdrawDecision(ctx, decision.ID, "handled by the commander")
			},
		},
		{
			name: "default",
			settle: func(ctx context.Context, s *Store, decision domain.Decision) error {
				_, err := s.ApplyExpiredDefaults(ctx, decision.CreatedAt.Add(time.Second))
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			s := newTestStore(t)
			project, err := s.CreateProject(ctx, "settlement-"+tt.name, "/repos/settlement-"+tt.name)
			if err != nil {
				t.Fatalf("CreateProject: %v", err)
			}
			goal, err := s.CreateGoal(ctx, project.ID, "settlement goal", "human")
			if err != nil {
				t.Fatalf("CreateGoal: %v", err)
			}
			commanderID := registerNamedTestAgentSession(t, s, "settlement-"+tt.name, 0)
			if _, err := s.ClaimProject(ctx, project.ID, commanderID); err != nil {
				t.Fatalf("ClaimProject: %v", err)
			}
			defaultAfter := int64(0)
			decision, err := s.AskDecision(ctx, AskInput{
				GoalID:         goal.ID,
				Kind:           domain.KindDecision,
				Question:       "settle this blocker",
				Options:        []domain.Option{{Label: "safe"}},
				DefaultOption:  "safe",
				DefaultAfterMs: &defaultAfter,
				AgentSessionID: commanderID,
			})
			if err != nil {
				t.Fatalf("AskDecision: %v", err)
			}
			if err := tt.settle(ctx, s, decision); err != nil {
				t.Fatalf("settle decision: %v", err)
			}
			open, err := s.ListOpenOrchestrationBlockers(ctx, project.ID)
			if err != nil {
				t.Fatalf("ListOpenOrchestrationBlockers: %v", err)
			}
			if len(open) != 0 {
				t.Fatalf("open blockers after %s = %#v, want none", tt.name, open)
			}
		})
	}
}
