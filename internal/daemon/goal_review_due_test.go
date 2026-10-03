package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/rpc"
)

type reviewDueItem struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	UpdatedAt time.Time `json:"updated_at"`
}

func goalListReviewDue(t *testing.T, fixture goalListFixture) json.RawMessage {
	t.Helper()
	params, err := json.Marshal(map[string]any{"cwd": fixture.project.RootPath})
	if err != nil {
		t.Fatalf("marshal goal.list params: %v", err)
	}
	result, err := fixture.daemon.dispatch(context.Background(), rpc.Request{Method: "goal.list", Params: params})
	if err != nil {
		t.Fatalf("goal.list: %v", err)
	}
	var response struct {
		ReviewDueGoals json.RawMessage `json:"review_due_goals"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		t.Fatalf("unmarshal goal.list response: %v", err)
	}
	return response.ReviewDueGoals
}

func ageGoalForTest(t *testing.T, fixture goalListFixture, goalID int64, age time.Duration) time.Time {
	t.Helper()
	at := time.Now().UTC().Add(-age).Truncate(time.Second)
	if _, err := fixture.store.DB().ExecContext(context.Background(), `UPDATE goals SET updated_at = ? WHERE id = ?`, at.Format(time.RFC3339), goalID); err != nil {
		t.Fatalf("age goal %d: %v", goalID, err)
	}
	return at
}

func TestGoalListReturnsProposedGoalsIdleForSevenDays(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()
	ctx := context.Background()

	agentDue, humanDue := fixture.proposed[0], fixture.proposed[1]
	if _, err := fixture.store.DB().ExecContext(ctx, `UPDATE goals SET creator = 'human' WHERE id = ?`, humanDue.ID); err != nil {
		t.Fatal(err)
	}
	agentAt := ageGoalForTest(t, fixture, agentDue.ID, 8*24*time.Hour)
	ageGoalForTest(t, fixture, humanDue.ID, 8*24*time.Hour)
	fresh, err := fixture.store.CreateGoal(ctx, fixture.project.ID, "fresh proposal", "agent")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	ageGoalForTest(t, fixture, fresh.ID, 6*24*time.Hour)
	ageGoalForTest(t, fixture, fixture.active[0].ID, 30*24*time.Hour)
	ageGoalForTest(t, fixture, fixture.done[0].ID, 30*24*time.Hour)

	var items []reviewDueItem
	if err := json.Unmarshal(goalListReviewDue(t, fixture), &items); err != nil {
		t.Fatalf("unmarshal review_due_goals: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("review_due_goals = %+v, want the two 8-day-old proposed goals only", items)
	}
	byID := map[int64]reviewDueItem{}
	for _, item := range items {
		byID[item.ID] = item
	}
	for _, goal := range []struct {
		id    int64
		title string
	}{{agentDue.ID, summaryLine(agentDue.Content)}, {humanDue.ID, summaryLine(humanDue.Content)}} {
		item, ok := byID[goal.id]
		if !ok {
			t.Fatalf("goal %d missing from review_due_goals: %+v", goal.id, items)
		}
		if item.Title != goal.title {
			t.Fatalf("goal %d title = %q, want %q", goal.id, item.Title, goal.title)
		}
	}
	if !byID[agentDue.ID].UpdatedAt.Equal(agentAt) {
		t.Fatalf("updated_at = %v, want %v", byID[agentDue.ID].UpdatedAt, agentAt)
	}
}

func TestGoalListReviewDueGoalsIsAnEmptyArrayWhenNothingIsDue(t *testing.T) {
	fixture := newGoalListFixture(t)
	defer fixture.store.Close()

	if got := string(goalListReviewDue(t, fixture)); got != "[]" {
		t.Fatalf("review_due_goals = %s, want []", got)
	}
}
