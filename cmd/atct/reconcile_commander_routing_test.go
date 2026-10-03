package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/httpapi"
	"github.com/michiomochi/atct/internal/store"
)

// Decisions created without a session (agent_session_id=0) are addressed to the
// commander by kind. These tests carry that from the real store reconcile
// through to what a commander (and only a commander) watch emits. Output is the
// formatted line (watch prints text, not event names): decision.approved is
// "atct decision approved", goal.review.complete is "atct goal review approved",
// goal.review.reject is "atct goal review rejected".

type commanderRoutingWatch struct {
	t         *testing.T
	serverURL string
	client    *httptest.Server
	delivered map[watchDeliveryKey]struct{}
	discrep   map[watchWakeupDiscrepancyDeliveryKey]struct{}
	wakeup    map[watchWakeupDeliveryKey]struct{}
	last      string
}

func newCommanderRoutingWatch(t *testing.T, s *store.Store) *commanderRoutingWatch {
	t.Helper()
	server := httptest.NewServer(httpapi.New(s).Handler())
	t.Cleanup(server.Close)
	return &commanderRoutingWatch{
		t: t, client: server,
		delivered: make(map[watchDeliveryKey]struct{}),
		discrep:   make(map[watchWakeupDiscrepancyDeliveryKey]struct{}),
		wakeup:    make(map[watchWakeupDeliveryKey]struct{}),
	}
}

func (w *commanderRoutingWatch) run(scope watchScope) string {
	w.t.Helper()
	var out bytes.Buffer
	if err := reconcileWatchScope(
		context.Background(), w.client.Client(), w.client.URL, scope, &out,
		w.delivered, &w.last, w.discrep, w.wakeup, newWatchScopeFilter(""), nil,
	); err != nil {
		w.t.Fatalf("reconcileWatchScope: %v", err)
	}
	return out.String()
}

func commanderScope(projectID int64) watchScope {
	return watchScope{Role: "commander", ProjectID: strconv.FormatInt(projectID, 10)}
}

func subcommanderScope(projectID, goalID int64) watchScope {
	return watchScope{Role: "subcommander", ProjectID: strconv.FormatInt(projectID, 10), GoalID: strconv.FormatInt(goalID, 10)}
}

func askSessionlessGoalReview(t *testing.T, s *store.Store, goalID int64) int64 {
	t.Helper()
	d, err := s.AskDecision(context.Background(), store.AskInput{
		GoalID: goalID, Kind: domain.KindGoalReview, Question: "Approve this goal review?",
		Options: []domain.Option{{Label: "approve"}, {Label: "reject"}},
	})
	if err != nil {
		t.Fatalf("AskDecision goal_review: %v", err)
	}
	return d.ID
}

func TestWatchDeliversSessionlessGoalApprovalToCommanderOnce(t *testing.T) {
	ctx := context.Background()
	s, projectID := newReconcileContractStore(t)
	goal, err := s.CreateGoal(ctx, projectID, "proposed by an agent", "agent")
	if err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	decisions, err := s.ListDecisionsForGoal(ctx, goal.ID)
	if err != nil || len(decisions) != 1 || decisions[0].AgentSessionID != 0 || decisions[0].Kind != domain.KindGoalApproval {
		t.Fatalf("goal approval = %#v, err %v; want one session-0 goal_approval", decisions, err)
	}
	if _, err := s.ApproveGoal(ctx, decisions[0].ID); err != nil {
		t.Fatalf("ApproveGoal: %v", err)
	}

	w := newCommanderRoutingWatch(t, s)
	out := w.run(commanderScope(projectID))
	if got := strings.Count(out, "atct decision approved"); got != 1 {
		t.Fatalf("commander output has %d approved lines, want 1:\n%s", got, out)
	}
	if again := w.run(commanderScope(projectID)); strings.TrimSpace(again) != "" {
		t.Fatalf("second reconcile re-delivered:\n%s", again)
	}

	other := newCommanderRoutingWatch(t, s)
	if sub := other.run(subcommanderScope(projectID, goal.ID)); strings.Contains(sub, "atct decision approved") {
		t.Fatalf("subcommander received a commander-addressed event:\n%s", sub)
	}
}

func TestWatchDeliversSessionlessGoalReviewToCommanderOnly(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		resolve    func(t *testing.T, s *store.Store, id int64)
	}{
		{"approve", "atct goal review approved", func(t *testing.T, s *store.Store, id int64) {
			if _, err := s.AnswerDecision(context.Background(), store.AnswerInput{DecisionID: id, AnswerLabel: "approve", AnswerText: "ok"}); err != nil {
				t.Fatalf("AnswerDecision: %v", err)
			}
			if _, err := s.PollDecisions(context.Background(), 0, id); err != nil {
				t.Fatalf("PollDecisions: %v", err)
			}
		}},
		{"reject", "atct goal review rejected", func(t *testing.T, s *store.Store, id int64) {
			if err := s.RejectGoalReview(context.Background(), id, "not yet"); err != nil {
				t.Fatalf("RejectGoalReview: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s, projectID := newReconcileContractStore(t)
			goal, err := s.CreateGoal(ctx, projectID, "active goal", "human")
			if err != nil {
				t.Fatalf("CreateGoal: %v", err)
			}
			tc.resolve(t, s, askSessionlessGoalReview(t, s, goal.ID))

			w := newCommanderRoutingWatch(t, s)
			out := w.run(commanderScope(projectID))
			if got := strings.Count(out, tc.want); got != 1 {
				t.Fatalf("commander output has %d %s, want 1:\n%s", got, tc.want, out)
			}
			if again := w.run(commanderScope(projectID)); strings.TrimSpace(again) != "" {
				t.Fatalf("second reconcile re-delivered:\n%s", again)
			}
			other := newCommanderRoutingWatch(t, s)
			if sub := other.run(subcommanderScope(projectID, goal.ID)); strings.Contains(sub, "atct goal review") {
				t.Fatalf("subcommander received a commander-addressed event:\n%s", sub)
			}
		})
	}
}
