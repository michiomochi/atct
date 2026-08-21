package store

import (
	"context"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
)

func TestWaitForAnswerReturnsWhenAnswered(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	taskID := newTestDecisionTask(t, s, goalID, "notify-answer")

	d, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, TaskID: taskID, Kind: domain.KindDecision, Question: "What should we do?", AgentSessionID: "run-1",
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	answerErr := make(chan error, 1)
	go func() {
		_, err := s.AnswerDecision(ctx, AnswerInput{DecisionID: d.ID, AnswerLabel: "A"})
		answerErr <- err
	}()

	got, ok, err := s.WaitForAnswer(ctx, d.ID, 2*time.Second)
	if err != nil {
		t.Fatalf("WaitForAnswer: %v", err)
	}
	if answerErr := <-answerErr; answerErr != nil {
		t.Fatalf("AnswerDecision: %v", answerErr)
	}
	if !ok {
		t.Fatal("ok = false, want true (answer arrived within timeout)")
	}
	if got.AnswerLabel != "A" {
		t.Fatalf("answer_label = %q, want %q", got.AnswerLabel, "A")
	}
}

func TestWaitForAnswerParksOnTimeout(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	taskID := newTestDecisionTask(t, s, goalID, "notify-timeout")

	d, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, TaskID: taskID, Kind: domain.KindDecision, Question: "What should we do?", AgentSessionID: "run-1",
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	_, ok, err := s.WaitForAnswer(ctx, d.ID, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForAnswer: %v", err)
	}
	if ok {
		t.Fatal("ok = true, want false (should park on timeout)")
	}
}

func TestCreateGoalPublishesGoalCreatedEventForHuman(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	parentID := newTestGoal(t, s)
	parent, err := s.GetGoal(ctx, parentID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}

	events, cancel := s.SubscribeEvents()
	defer cancel()

	created, err := s.CreateGoal(ctx, parent.ProjectID, "human-created", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	event := receiveStoreEvent(t, events)
	if event.Name != "goal.created" {
		t.Fatalf("event name = %q, want %q", event.Name, "goal.created")
	}
	goal, ok := event.Data.(domain.Goal)
	if !ok {
		t.Fatalf("event data type = %T, want domain.Goal", event.Data)
	}
	if goal.ID != created.ID {
		t.Fatalf("event goal id = %q, want %q", goal.ID, created.ID)
	}
	assertNoGoalCreatedEvent(t, events)
}

func TestCreateGoalPublishesGoalCreatedEventForAgent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	parentID := newTestGoal(t, s)
	parent, err := s.GetGoal(ctx, parentID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}

	events, cancel := s.SubscribeEvents()
	defer cancel()

	created, err := s.CreateGoal(ctx, parent.ProjectID, "agent-created", "agent")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	goalEvents := 0
	decisionEvents := 0
	for range 2 {
		event := receiveStoreEvent(t, events)
		switch event.Name {
		case "goal.created":
			goalEvents++
			goal, ok := event.Data.(domain.Goal)
			if !ok {
				t.Fatalf("event data type = %T, want domain.Goal", event.Data)
			}
			if goal.ID != created.ID {
				t.Fatalf("event goal id = %q, want %q", goal.ID, created.ID)
			}
		case "decision.created":
			decisionEvents++
		default:
			t.Fatalf("unexpected event name = %q", event.Name)
		}
	}
	if goalEvents != 1 {
		t.Fatalf("goal.created events = %d, want 1", goalEvents)
	}
	if decisionEvents != 1 {
		t.Fatalf("decision.created events = %d, want 1", decisionEvents)
	}
	assertNoGoalCreatedEvent(t, events)
}

func TestAskDecisionDoesNotPublishGoalCreatedEvent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	taskID := newTestDecisionTask(t, s, goalID, "no-goal-event")

	events, cancel := s.SubscribeEvents()
	defer cancel()

	if _, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, TaskID: taskID, Kind: domain.KindDecision, Question: "What should we do?", AgentSessionID: "run-1",
	}); err != nil {
		t.Fatalf("AskDecision: %v", err)
	}

	assertNoGoalCreatedEvent(t, events)
}

func receiveStoreEvent(t *testing.T, events <-chan DecisionEvent) DecisionEvent {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for store event")
		return DecisionEvent{}
	}
}

func assertNoGoalCreatedEvent(t *testing.T, events <-chan DecisionEvent) {
	t.Helper()
	deadline := time.After(100 * time.Millisecond)
	for {
		select {
		case event := <-events:
			if event.Name == "goal.created" {
				t.Fatalf("unexpected goal.created event: %+v", event)
			}
		case <-deadline:
			return
		}
	}
}
