package store

import (
	"context"
	"testing"
)

func TestRequestReportOverwritesGoalSpecAndPlan(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)

	got, err := s.UpdateGoalRequestReport(ctx, goalID, "first spec", "first plan")
	if err != nil {
		t.Fatal(err)
	}
	if got.Spec != "first spec" || got.Plan != "first plan" {
		t.Fatalf("first report = %+v", got)
	}
	got, err = s.UpdateGoalRequestReport(ctx, goalID, "replacement spec", "replacement plan")
	if err != nil {
		t.Fatal(err)
	}
	if got.Spec != "replacement spec" || got.Plan != "replacement plan" {
		t.Fatalf("replacement report = %+v", got)
	}
}
