package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

// humanQuoteMinRunes is the shortest quote that counts as evidence. A one- or
// two-character quote such as "はい" occurs in almost any prompt, so it proves
// nothing about what the human said.
const humanQuoteMinRunes = 2

// ErrHumanQuoteUnverified refuses a chat answer whose quote cannot be tied to a
// message the human actually typed. The decision stays open.
var ErrHumanQuoteUnverified = errors.New("human_quote could not be verified against the human's own prompt")

type decisionAnswerParams struct {
	DecisionID              int64  `json:"decision_id"`
	Answer                  string `json:"answer"`
	AnswerText              string `json:"answer_text"`
	HumanQuote              string `json:"human_quote"`
	AgentSessionID          int64  `json:"agent_session_id"`
	IncludeUnappliedAnswers bool   `json:"include_unapplied_answers"`
}

// answerDecisionFromChat records an answer the human gave in the chat. Every
// check runs before the write, and a failed check records nothing.
func (d *Daemon) answerDecisionFromChat(ctx context.Context, p decisionAnswerParams) (domain.Decision, error) {
	if p.AgentSessionID == 0 {
		return domain.Decision{}, errors.New("agent_session_id is required to answer a decision from the chat")
	}
	decision, err := d.store.GetDecision(ctx, p.DecisionID)
	if err != nil {
		return domain.Decision{}, err
	}
	if decision.Status != domain.DecisionOpen {
		return domain.Decision{}, fmt.Errorf("%w: %d", store.ErrDecisionNotOpen, decision.ID)
	}
	if err := d.ensureCanAnswerFromChat(ctx, p.AgentSessionID, decision); err != nil {
		return domain.Decision{}, err
	}
	if err := validateChatAnswer(decision, p); err != nil {
		return domain.Decision{}, err
	}
	evidence, err := d.verifyHumanQuote(ctx, p.AgentSessionID, decision, p.HumanQuote)
	if err != nil {
		return domain.Decision{}, err
	}

	src := store.AnswerSource{Via: "chat", Quote: strings.TrimSpace(p.HumanQuote), Evidence: evidence}
	switch {
	case decision.Kind == domain.KindGoalReview && p.Answer == "approve":
		if _, err := d.store.ApproveGoalReviewWith(ctx, decision.ID, src); err != nil {
			return domain.Decision{}, err
		}
	case decision.Kind == domain.KindGoalReview:
		if err := d.store.RejectGoalReviewWith(ctx, decision.ID, p.AnswerText, src); err != nil {
			return domain.Decision{}, err
		}
	default:
		if _, err := d.store.AnswerDecisionWith(ctx, store.AnswerInput{
			DecisionID: decision.ID, AnswerLabel: p.Answer, AnswerText: p.AnswerText,
		}, src); err != nil {
			return domain.Decision{}, err
		}
	}
	return d.store.GetDecision(ctx, decision.ID)
}

// ensureCanAnswerFromChat admits the session that asked the decision, and a
// commander the decision is routed to within the same project. Anyone else is
// refused the same way decision.poll refuses a foreign decision.
func (d *Daemon) ensureCanAnswerFromChat(ctx context.Context, agentSessionID int64, decision domain.Decision) error {
	if decision.AgentSessionID == agentSessionID {
		return nil
	}
	role, err := d.deriveSessionRole(ctx, agentSessionID)
	if err != nil {
		return err
	}
	if role.Role == "commander" && store.DecisionRoutesToCommander(decision) {
		goal, err := d.store.GetGoal(ctx, decision.GoalID)
		if err != nil {
			return err
		}
		if goal.ProjectID == role.ProjectID {
			return nil
		}
	}
	return fmt.Errorf("decision %d is owned by another agent session", decision.ID)
}

func validateChatAnswer(decision domain.Decision, p decisionAnswerParams) error {
	switch {
	case decision.Kind == domain.DecisionKind("completion"):
		return errors.New("use approve or reject for this decision")
	case decision.Kind == domain.KindGoalReview:
		if p.Answer != "approve" && p.Answer != "reject" {
			return fmt.Errorf("goal_review accepts answer approve or reject, not %q", p.Answer)
		}
	case p.Answer != "":
		for _, option := range decision.Options {
			if option.Label == p.Answer {
				return nil
			}
		}
		return fmt.Errorf("answer %q is not one of the option labels of decision %d", p.Answer, decision.ID)
	case strings.TrimSpace(p.AnswerText) == "":
		return errors.New("an answer label or answer_text is required")
	}
	return nil
}

// verifyHumanQuote returns the human prompt that contains the quote. The quote
// must be found in a prompt the hook recorded for this session after the
// decision was asked, and that prompt's transcript must hold the same text as a
// human message. Any miss refuses the answer.
func (d *Daemon) verifyHumanQuote(ctx context.Context, agentSessionID int64, decision domain.Decision, quote string) (string, error) {
	needle := normalizeQuoteText(quote)
	if utf8.RuneCountInString(needle) < humanQuoteMinRunes {
		return "", fmt.Errorf("%w: human_quote must be at least %d characters", ErrHumanQuoteUnverified, humanQuoteMinRunes)
	}
	sessionKey, err := d.store.AgentSessionKey(ctx, agentSessionID)
	if err != nil {
		return "", fmt.Errorf("resolve session key: %w", err)
	}
	if sessionKey == "" {
		return "", fmt.Errorf("%w: this session has no session_key", ErrHumanQuoteUnverified)
	}
	prompts, err := d.store.HumanPromptsSince(ctx, sessionKey, decision.CreatedAt)
	if err != nil {
		return "", err
	}
	// Every prompt that contains the quote is a candidate, newest first. The
	// first one whose transcript holds the same text as a human message is the
	// evidence. A hook can also record non-human input, so a newer match is not
	// proof on its own, and the older candidates still get checked.
	reason := fmt.Sprintf("no prompt this session sent after decision %d was asked contains it", decision.ID)
	for _, prompt := range prompts {
		if !strings.Contains(normalizeQuoteText(prompt.Prompt), needle) {
			continue
		}
		if !isHarnessTranscriptPath(prompt.TranscriptPath, sessionKey) {
			reason = "transcript path is not this session's harness transcript"
			continue
		}
		ok, err := transcriptHasHumanQuote(prompt.TranscriptPath, prompt.PromptID, needle)
		if err != nil {
			reason = fmt.Sprintf("read transcript: %v", err)
			continue
		}
		if ok {
			return prompt.Prompt, nil
		}
		reason = "the transcript has no human message with that prompt_id containing it"
	}
	return "", fmt.Errorf("%w: %s; ask the human to answer on the dashboard", ErrHumanQuoteUnverified, reason)
}

// isHarnessTranscriptPath accepts only the path Claude Code writes a session's
// transcript to: <config>/projects/<project>/<session_id>.jsonl. The agent
// supplies transcript_path, so anything else could be a file it wrote itself.
func isHarnessTranscriptPath(path, sessionKey string) bool {
	if sessionKey == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	if filepath.Base(path) != sessionKey+".jsonl" {
		return false
	}
	return filepath.Base(filepath.Dir(filepath.Dir(path))) == "projects"
}

// normalizeQuoteText trims the text and collapses every run of whitespace,
// newlines included, into one space.
func normalizeQuoteText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

type transcriptEntry struct {
	Type     string `json:"type"`
	PromptID string `json:"promptId"`
	Origin   struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// transcriptHasHumanQuote scans the transcript line by line with no line-length
// limit. Only lines that mention promptID are parsed.
func transcriptHasHumanQuote(path, promptID, needle string) (bool, error) {
	if promptID == "" {
		return false, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		line, readErr := r.ReadBytes('\n')
		if bytes.Contains(line, []byte(promptID)) && transcriptLineHasHumanText(line, promptID, needle) {
			return true, nil
		}
		if errors.Is(readErr, io.EOF) {
			return false, nil
		}
		if readErr != nil {
			return false, readErr
		}
	}
}

func transcriptLineHasHumanText(line []byte, promptID, needle string) bool {
	var entry transcriptEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return false
	}
	if entry.Type != "user" || entry.PromptID != promptID || entry.Origin.Kind != "human" {
		return false
	}
	return strings.Contains(normalizeQuoteText(messageText(entry.Message.Content)), needle)
}

// messageText reads a message's content, either a plain string or an array of
// text parts. The parts are joined without a separator, so a quote must sit
// inside one contiguous run of text.
func messageText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, part := range parts {
		if part.Type == "text" {
			b.WriteString(part.Text)
		}
	}
	return b.String()
}
