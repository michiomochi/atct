package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestListDecisionsForGoalReturnsOnlyThatGoalsDecisions(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	project, err := s.CreateProject(ctx, "atct", "/repos/atct")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	goalA, err := s.CreateGoal(ctx, project.ID, "goal a", "human")
	if err != nil {
		t.Fatalf("CreateGoal A: %v", err)
	}
	goalB, err := s.CreateGoal(ctx, project.ID, "goal b", "human")
	if err != nil {
		t.Fatalf("CreateGoal B: %v", err)
	}
	options := []domain.Option{{Label: "yes", Description: "go ahead", Consequence: "ships"}, {Label: "no", Description: "stop", Consequence: "waits"}}
	decisionA, err := s.AskDecision(ctx, AskInput{
		GoalID: goalA.ID, Kind: domain.KindDecision, Question: "A?", Options: options, AgentSessionID: testSessionID("list-for-goal-a"),
	})
	if err != nil {
		t.Fatalf("AskDecision A: %v", err)
	}
	decisionB, err := s.AskDecision(ctx, AskInput{
		GoalID: goalB.ID, Kind: domain.KindDecision, Question: "B?", Options: options, AgentSessionID: testSessionID("list-for-goal-b"),
	})
	if err != nil {
		t.Fatalf("AskDecision B: %v", err)
	}
	if _, err := s.AnswerDecision(ctx, AnswerInput{DecisionID: decisionA.ID, AnswerLabel: "yes"}); err != nil {
		t.Fatalf("AnswerDecision A: %v", err)
	}

	gotA, err := s.ListDecisionsForGoal(ctx, goalA.ID)
	if err != nil {
		t.Fatalf("ListDecisionsForGoal A: %v", err)
	}
	if len(gotA) != 1 {
		t.Fatalf("goal A decisions: got %d, want 1: %+v", len(gotA), gotA)
	}
	if gotA[0].ID != decisionA.ID || gotA[0].Question != "A?" || !reflect.DeepEqual(gotA[0].Options, options) ||
		gotA[0].Status != domain.DecisionAnswered || gotA[0].AgentSessionID != testSessionID("list-for-goal-a") {
		t.Fatalf("goal A decision: got %+v, want answered decision %d with options %+v", gotA[0], decisionA.ID, options)
	}

	gotB, err := s.ListDecisionsForGoal(ctx, goalB.ID)
	if err != nil {
		t.Fatalf("ListDecisionsForGoal B: %v", err)
	}
	if len(gotB) != 1 || gotB[0].ID != decisionB.ID || gotB[0].Status != domain.DecisionOpen {
		t.Fatalf("goal B decisions: got %+v, want only open decision %d", gotB, decisionB.ID)
	}
}
