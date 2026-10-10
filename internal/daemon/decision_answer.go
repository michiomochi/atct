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
	"time"
	"unicode"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

// humanEvidenceMaxRunes caps the human message kept as answer_evidence.
const humanEvidenceMaxRunes = 8000

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
	promptID, evidence, err := d.verifyHumanQuote(ctx, p.AgentSessionID, decision, p.HumanQuote)
	if err != nil {
		return domain.Decision{}, err
	}

	src := store.AnswerSource{Via: "chat", Quote: strings.TrimSpace(p.HumanQuote), Evidence: evidence, PromptID: promptID}
	if err := d.writeChatAnswer(ctx, decision, p, src); err != nil {
		if errors.Is(err, store.ErrAnswerPromptReused) {
			return domain.Decision{}, fmt.Errorf("%w: this human message already answered another decision; ask the human to answer on the dashboard", ErrHumanQuoteUnverified)
		}
		return domain.Decision{}, err
	}
	return d.store.GetDecision(ctx, decision.ID)
}

func (d *Daemon) writeChatAnswer(ctx context.Context, decision domain.Decision, p decisionAnswerParams, src store.AnswerSource) error {
	switch {
	case decision.Kind == domain.KindGoalReview && p.Answer == "approve":
		_, err := d.store.ApproveGoalReviewWith(ctx, decision.ID, src)
		return err
	case decision.Kind == domain.KindGoalReview:
		return d.store.RejectGoalReviewWith(ctx, decision.ID, p.AnswerText, src)
	default:
		_, err := d.store.AnswerDecisionWith(ctx, store.AnswerInput{
			DecisionID: decision.ID, AnswerLabel: p.Answer, AnswerText: p.AnswerText,
		}, src)
		return err
	}
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

// verifyHumanQuote returns the prompt ID and the human message that hold the
// quote as one whole line. The quote must be found in a prompt the hook recorded
// for this session after the decision was asked, and that prompt's transcript
// must hold the same line as a human message written after the decision was
// asked. Any miss refuses the answer.
func (d *Daemon) verifyHumanQuote(ctx context.Context, agentSessionID int64, decision domain.Decision, quote string) (string, string, error) {
	needle := normalizeQuoteText(quote)
	if !hasLetterOrDigit(needle) {
		return "", "", fmt.Errorf("%w: human_quote must contain a letter or a digit", ErrHumanQuoteUnverified)
	}
	sessionKey, err := d.store.AgentSessionKey(ctx, agentSessionID)
	if err != nil {
		return "", "", fmt.Errorf("resolve session key: %w", err)
	}
	if sessionKey == "" {
		return "", "", fmt.Errorf("%w: this session has no session_key", ErrHumanQuoteUnverified)
	}
	// The store keeps created_at in whole seconds, so a message in the second the
	// decision was asked could predate it. A real answer never comes that fast.
	since := decision.CreatedAt.Add(time.Second)
	prompts, err := d.store.HumanPromptsSince(ctx, sessionKey, since)
	if err != nil {
		return "", "", err
	}
	// The hook's copy only finds candidates. The transcript decides, and a
	// message written before the decision was asked never counts. Newest first.
	reason := fmt.Sprintf("no prompt this session sent after decision %d was asked has the quote as a whole line", decision.ID)
	for _, prompt := range prompts {
		if !quoteLineMatches(prompt.Prompt, needle) {
			continue
		}
		if !isHarnessTranscriptPath(prompt.TranscriptPath, sessionKey) {
			reason = "transcript path is not this session's harness transcript"
			continue
		}
		if !insideClaudeProjects(prompt.TranscriptPath, sessionKey) {
			reason = "transcript path is outside the Claude Code projects directory"
			continue
		}
		text, ok, err := transcriptHumanQuote(prompt.TranscriptPath, prompt.PromptID, needle, since)
		if err != nil {
			reason = fmt.Sprintf("read transcript: %v", err)
			continue
		}
		if ok {
			return prompt.PromptID, truncateRunes(text, humanEvidenceMaxRunes), nil
		}
		reason = "the transcript has no human message with that prompt_id, written after the decision was asked, that has the quote as a whole line"
	}
	return "", "", fmt.Errorf("%w: %s; ask the human to answer on the dashboard", ErrHumanQuoteUnverified, reason)
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

// insideClaudeProjects reports whether path is exactly <projects>/<project>/
// <session_key>.jsonl under the projects folder of CLAUDE_CONFIG_DIR or of the
// default ~/.claude. The agent can write files under those folders too (an
// auto-memory folder, for one), so the depth and name are checked, and the
// file itself must not be a symlink: a symlink can point at any file.
func insideClaudeProjects(path, sessionKey string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	var roots []string
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		roots = append(roots, filepath.Join(dir, "projects"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, ".claude", "projects"))
	}
	for _, root := range roots {
		realRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(realRoot, resolved)
		if err != nil {
			continue
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) == 2 && parts[0] != ".." && parts[1] == sessionKey+".jsonl" {
			return true
		}
	}
	return false
}

// normalizeQuoteText trims the text and collapses every run of whitespace,
// newlines included, into one space.
func normalizeQuoteText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// quoteLineMatches reports whether the normalized needle equals one whole line
// of text. A quote inside a longer line does not count.
func quoteLineMatches(text, needle string) bool {
	for _, line := range strings.Split(text, "\n") {
		if normalizeQuoteText(line) == needle {
			return true
		}
	}
	return false
}

// hasLetterOrDigit keeps a quote made only of punctuation or spaces from
// matching a line that is only punctuation.
func hasLetterOrDigit(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsDigit(r)
	})
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) > max {
		return string(runes[:max])
	}
	return s
}

type transcriptEntry struct {
	Type      string `json:"type"`
	PromptID  string `json:"promptId"`
	Timestamp string `json:"timestamp"`
	Origin    struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// transcriptHumanQuote scans the transcript line by line with no line-length
// limit. It returns the text of the human message with promptID that holds the
// needle as a whole line and was written at or after since. Only lines that
// mention promptID are parsed.
func transcriptHumanQuote(path, promptID, needle string, since time.Time) (string, bool, error) {
	if promptID == "" {
		return "", false, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		line, readErr := r.ReadBytes('\n')
		if bytes.Contains(line, []byte(promptID)) {
			if text, ok := transcriptLineHumanQuote(line, promptID, needle, since); ok {
				return text, true, nil
			}
		}
		if errors.Is(readErr, io.EOF) {
			return "", false, nil
		}
		if readErr != nil {
			return "", false, readErr
		}
	}
}

func transcriptLineHumanQuote(line []byte, promptID, needle string, since time.Time) (string, bool) {
	var entry transcriptEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return "", false
	}
	if entry.Type != "user" || entry.PromptID != promptID || entry.Origin.Kind != "human" {
		return "", false
	}
	at, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
	if err != nil || at.Before(since) {
		return "", false
	}
	text := messageText(entry.Message.Content)
	return text, quoteLineMatches(text, needle)
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
