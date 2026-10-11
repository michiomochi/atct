package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

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

func TestRecordHumanPromptKeepsNewestFiftyPerSession(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	base := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	for i := range 52 {
		if err := s.RecordHumanPrompt(ctx, HumanPromptInput{
			SessionKey: "chat-session-a", PromptID: fmt.Sprintf("prompt-%02d", i),
			TranscriptPath: "/tmp/t.jsonl", Prompt: "prompt", RecordedAt: base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("RecordHumanPrompt %d: %v", i, err)
		}
	}
	if err := s.RecordHumanPrompt(ctx, HumanPromptInput{
		SessionKey: "chat-session-b", PromptID: "other", TranscriptPath: "/tmp/b.jsonl", Prompt: "other", RecordedAt: base,
	}); err != nil {
		t.Fatalf("RecordHumanPrompt other session: %v", err)
	}

	got, err := s.HumanPromptsSince(ctx, "chat-session-a", time.Time{})
	if err != nil {
		t.Fatalf("HumanPromptsSince: %v", err)
	}
	if len(got) != 50 {
		t.Fatalf("kept %d prompts, want 50", len(got))
	}
	if !got[0].RecordedAt.Equal(base.Add(51*time.Minute)) || !got[49].RecordedAt.Equal(base.Add(2*time.Minute)) {
		t.Fatalf("kept range = %v..%v, want the newest 50 newest first", got[0].RecordedAt, got[49].RecordedAt)
	}
	other, err := s.HumanPromptsSince(ctx, "chat-session-b", time.Time{})
	if err != nil {
		t.Fatalf("HumanPromptsSince other: %v", err)
	}
	if len(other) != 1 {
		t.Fatalf("other session kept %d prompts, want 1", len(other))
	}
}

func TestHumanPromptsSinceFiltersByRecordedAt(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	base := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	for i := range 3 {
		if err := s.RecordHumanPrompt(ctx, HumanPromptInput{
			SessionKey: "chat-since", PromptID: "p", TranscriptPath: "/tmp/s.jsonl", Prompt: "p", RecordedAt: base.Add(time.Duration(i) * time.Hour),
		}); err != nil {
			t.Fatalf("RecordHumanPrompt: %v", err)
		}
	}
	got, err := s.HumanPromptsSince(ctx, "chat-since", base.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("HumanPromptsSince: %v", err)
	}
	if len(got) != 2 || !got[0].RecordedAt.Equal(base.Add(2*time.Hour)) || !got[1].RecordedAt.Equal(base.Add(time.Hour)) {
		t.Fatalf("since 09:30 returned %d prompts, want the 10:00 and 11:00 ones newest first", len(got))
	}
}

func TestRecordHumanPromptPinsFirstNamedTranscript(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	base := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	const key = "chat-session-pin"
	pinned := func() string {
		t.Helper()
		got, err := s.PinnedTranscript(ctx, key)
		if err != nil {
			t.Fatalf("PinnedTranscript: %v", err)
		}
		return got
	}
	record := func(promptID, path string, at time.Time) {
		t.Helper()
		if err := s.RecordHumanPrompt(ctx, HumanPromptInput{
			SessionKey: key, PromptID: promptID, TranscriptPath: path, Prompt: "prompt", RecordedAt: at,
		}); err != nil {
			t.Fatalf("RecordHumanPrompt %s: %v", promptID, err)
		}
	}
	if got := pinned(); got != "" {
		t.Fatalf("pinned before any prompt = %q, want empty", got)
	}
	record("p-empty", "", base)
	if got := pinned(); got != "" {
		t.Fatalf("a prompt without a transcript pinned %q", got)
	}
	record("p-first", "/tmp/first.jsonl", base.Add(time.Minute))
	record("p-second", "/tmp/second.jsonl", base.Add(2*time.Minute))
	if got := pinned(); got != "/tmp/first.jsonl" {
		t.Fatalf("pinned = %q, want the first named path", got)
	}
}

func TestPinnedTranscriptSurvivesPruning(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	base := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	const key = "chat-session-pruned"
	if err := s.RecordHumanPrompt(ctx, HumanPromptInput{
		SessionKey: key, PromptID: "p-first", TranscriptPath: "/tmp/first.jsonl", Prompt: "prompt", RecordedAt: base,
	}); err != nil {
		t.Fatalf("RecordHumanPrompt first: %v", err)
	}
	for i := range 52 {
		if err := s.RecordHumanPrompt(ctx, HumanPromptInput{
			SessionKey: key, PromptID: fmt.Sprintf("p-%02d", i), TranscriptPath: "/tmp/later.jsonl", Prompt: "prompt",
			RecordedAt: base.Add(time.Duration(i+1) * time.Minute),
		}); err != nil {
			t.Fatalf("RecordHumanPrompt %d: %v", i, err)
		}
	}
	prompts, err := s.HumanPromptsSince(ctx, key, time.Time{})
	if err != nil {
		t.Fatalf("HumanPromptsSince: %v", err)
	}
	for _, p := range prompts {
		if p.PromptID == "p-first" {
			t.Fatalf("the first prompt was still kept; the test needs it pruned")
		}
	}
	got, err := s.PinnedTranscript(ctx, key)
	if err != nil || got != "/tmp/first.jsonl" {
		t.Fatalf("pinned after pruning = %q, %v; want /tmp/first.jsonl", got, err)
	}
}

func TestRecordHumanPromptTruncatesToRunes(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	long := strings.Repeat("あ", humanPromptMaxRunes+1)
	if err := s.RecordHumanPrompt(ctx, HumanPromptInput{
		SessionKey: "chat-long", PromptID: "p", TranscriptPath: "/tmp/l.jsonl", Prompt: long,
	}); err != nil {
		t.Fatalf("RecordHumanPrompt: %v", err)
	}
	got, err := s.HumanPromptsSince(ctx, "chat-long", time.Time{})
	if err != nil {
		t.Fatalf("HumanPromptsSince: %v", err)
	}
	if len(got) != 1 || len([]rune(got[0].Prompt)) != humanPromptMaxRunes {
		t.Fatalf("stored prompt runes = %d, want %d", len([]rune(got[0].Prompt)), humanPromptMaxRunes)
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
