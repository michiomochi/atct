package store

import (
	"context"
	"testing"
)

func TestGoalHandoffEverReceivedBy(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	goalID := newTestGoal(t, s)
	addLiveProjectClaim(t, s, goalID, "ever-requester")
	addTestAgentSession(t, s, "ever-receiver")
	addTestAgentSession(t, s, "ever-bystander")
	goal, err := s.GetGoal(ctx, goalID)
	if err != nil {
		t.Fatalf("GetGoal: %v", err)
	}
	otherGoal, err := s.CreateGoal(ctx, goal.ProjectID, "other goal", "human")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	handoff, err := s.RequestGoalHandoff(ctx, "ever-handoff", goalID, testSessionID("ever-requester"), "delegate")
	if err != nil {
		t.Fatalf("RequestGoalHandoff: %v", err)
	}
	receiver := testSessionID("ever-receiver")
	if got, err := s.GoalHandoffEverReceivedBy(ctx, goalID, receiver); err != nil || got {
		t.Fatalf("before receive: got %v, err %v, want false", got, err)
	}
	if _, err := s.ReceiveGoalHandoff(ctx, handoff.ID, goalID, receiver); err != nil {
		t.Fatalf("ReceiveGoalHandoff: %v", err)
	}

	for _, tc := range []struct {
		name    string
		goalID  int64
		session int64
		want    bool
	}{
		{"receiver of the goal's handoff", goalID, receiver, true},
		{"session that never received", goalID, testSessionID("ever-bystander"), false},
		{"receiver asked about another goal", otherGoal.ID, receiver, false},
		{"no session", goalID, 0, false},
	} {
		got, err := s.GoalHandoffEverReceivedBy(ctx, tc.goalID, tc.session)
		if err != nil || got != tc.want {
			t.Errorf("%s: got %v, err %v, want %v", tc.name, got, err, tc.want)
		}
	}
}
