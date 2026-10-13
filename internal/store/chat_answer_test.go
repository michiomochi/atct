package store

import (
	"context"
	"errors"
	"testing"

	"github.com/michiomochi/atct/internal/domain"
)

func TestAnswerDecisionWithRecordsSource(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	taskID := newTestDecisionTask(t, s, goalID, "chat-answer")

	chat, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, TaskID: taskID, Kind: domain.KindDecision, Question: "chat?", AgentSessionID: testSessionID("chat-answer-1"),
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if _, err := s.AnswerDecisionWith(ctx, AnswerInput{DecisionID: chat.ID, AnswerText: "yes"}, AnswerSource{
		Via: "chat", Quote: "yes, go ahead", Evidence: "prompt-42",
	}); err != nil {
		t.Fatalf("AnswerDecisionWith: %v", err)
	}
	got, err := s.GetDecision(ctx, chat.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.AnsweredVia != "chat" || got.AnswerQuote != "yes, go ahead" || got.AnswerEvidence != "prompt-42" {
		t.Fatalf("decision source = %q/%q/%q, want chat/quote/evidence", got.AnsweredVia, got.AnswerQuote, got.AnswerEvidence)
	}

	dashboard, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, TaskID: taskID, Kind: domain.KindDecision, Question: "dashboard?", AgentSessionID: testSessionID("chat-answer-2"),
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	if _, err := s.AnswerDecision(ctx, AnswerInput{DecisionID: dashboard.ID, AnswerText: "A"}); err != nil {
		t.Fatalf("AnswerDecision: %v", err)
	}
	got, err = s.GetDecision(ctx, dashboard.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.AnsweredVia != "" || got.AnswerQuote != "" || got.AnswerEvidence != "" {
		t.Fatalf("dashboard answer source = %q/%q/%q, want empty", got.AnsweredVia, got.AnswerQuote, got.AnswerEvidence)
	}
}

func TestGoalReviewApproveAndRejectWithRecordSource(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	commanderID := testSessionID("chat-review-commander")
	receiverID := testSessionID("chat-review-receiver")
	addLiveProjectClaim(t, s, goalID, "chat-review-commander")
	addTestAgentSession(t, s, "chat-review-receiver")
	receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, "chat-review-handoff", goalID, commanderID, receiverID)
	review, err := s.RequestGoalReview(ctx, goalID, commanderID, domain.CompletionReport{
		WorkDone: "work", NowPossible: "result", HowToVerify: "verify", Surprises: "none", NeedsReview: "none",
	})
	if err != nil {
		t.Fatalf("RequestGoalReview: %v", err)
	}

	src := AnswerSource{Via: "chat", Quote: "approve it", Evidence: "prompt-7"}
	if _, err := s.ApproveGoalReviewWith(ctx, review.ID, src); err != nil {
		t.Fatalf("ApproveGoalReviewWith: %v", err)
	}
	got, err := s.GetDecision(ctx, review.ID)
	if err != nil {
		t.Fatalf("GetDecision after approval: %v", err)
	}
	if got.AnsweredVia != "chat" || got.AnswerQuote != "approve it" || got.AnswerEvidence != "prompt-7" {
		t.Fatalf("approved source = %q/%q/%q, want chat/quote/evidence", got.AnsweredVia, got.AnswerQuote, got.AnswerEvidence)
	}
}

func TestGoalReviewRejectWithRecordSource(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	commanderID := testSessionID("chat-reject-commander")
	receiverID := testSessionID("chat-reject-receiver")
	addLiveProjectClaim(t, s, goalID, "chat-reject-commander")
	addTestAgentSession(t, s, "chat-reject-receiver")
	receiveGoalHandoffReviewForGoalReviewTest(t, s, ctx, "chat-reject-handoff", goalID, commanderID, receiverID)
	review, err := s.RequestGoalReview(ctx, goalID, commanderID, domain.CompletionReport{
		WorkDone: "work", NowPossible: "result", HowToVerify: "verify", Surprises: "none", NeedsReview: "none",
	})
	if err != nil {
		t.Fatalf("RequestGoalReview: %v", err)
	}

	if err := s.RejectGoalReviewWith(ctx, review.ID, "needs another pass", AnswerSource{Via: "chat", Quote: "no", Evidence: "prompt-8"}); err != nil {
		t.Fatalf("RejectGoalReviewWith: %v", err)
	}
	got, err := s.GetDecision(ctx, review.ID)
	if err != nil {
		t.Fatalf("GetDecision after rejection: %v", err)
	}
	if got.AnswerText != "needs another pass" || got.AnsweredVia != "chat" || got.AnswerQuote != "no" || got.AnswerEvidence != "prompt-8" {
		t.Fatalf("rejected decision = %+v, want reason and chat source", got)
	}
}

func TestAnswerDecisionRefusesPromptAlreadyUsedForAnotherDecision(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	goalID := newTestGoal(t, s)
	taskID := newTestDecisionTask(t, s, goalID, "chat-prompt-reuse")

	first, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, TaskID: taskID, Kind: domain.KindDecision, Question: "first?", AgentSessionID: testSessionID("chat-prompt-reuse-1"),
	})
	if err != nil {
		t.Fatalf("AskDecision first: %v", err)
	}
	second, err := s.AskDecision(ctx, AskInput{
		GoalID: goalID, TaskID: taskID, Kind: domain.KindDecision, Question: "second?", AgentSessionID: testSessionID("chat-prompt-reuse-2"),
	})
	if err != nil {
		t.Fatalf("AskDecision second: %v", err)
	}
	if _, err := s.AnswerDecisionWith(ctx, AnswerInput{DecisionID: first.ID, AnswerText: "yes"}, AnswerSource{
		Via: "chat", Quote: "yes", Evidence: "yes", PromptID: "pid-once",
	}); err != nil {
		t.Fatalf("first AnswerDecisionWith: %v", err)
	}

	_, err = s.AnswerDecisionWith(ctx, AnswerInput{DecisionID: second.ID, AnswerText: "yes"}, AnswerSource{
		Via: "chat", Quote: "yes", Evidence: "yes", PromptID: "pid-once",
	})
	if !errors.Is(err, ErrAnswerPromptReused) {
		t.Fatalf("second AnswerDecisionWith err = %v, want ErrAnswerPromptReused", err)
	}
	got, err := s.GetDecision(ctx, second.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.Status != domain.DecisionOpen {
		t.Fatalf("second decision status = %s after refused reuse, want open", got.Status)
	}
}
