package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
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

// humanEvidence is one human message in the session's transcript that holds the
// quote as a whole line. ID is the message's own ID in the transcript.
type humanEvidence struct {
	ID   string
	Text string
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
	found, err := d.verifyHumanQuote(ctx, p.AgentSessionID, decision, p.HumanQuote)
	if err != nil {
		return domain.Decision{}, err
	}

	// A human message answers at most one decision. Each candidate is tried newest
	// first, and a message already used by another decision is skipped.
	quote := strings.TrimSpace(p.HumanQuote)
	for _, human := range found {
		src := store.AnswerSource{Via: "chat", Quote: quote, Evidence: truncateRunes(human.Text, humanEvidenceMaxRunes), PromptID: human.ID}
		err := d.writeChatAnswer(ctx, decision, p, src)
		if errors.Is(err, store.ErrAnswerPromptReused) {
			continue
		}
		if err != nil {
			return domain.Decision{}, err
		}
		return d.store.GetDecision(ctx, decision.ID)
	}
	return domain.Decision{}, fmt.Errorf("%w: this human message already answered another decision; ask the human to answer on the dashboard", ErrHumanQuoteUnverified)
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

// verifyHumanQuote finds the session's own transcript and returns every human
// message in it that holds the quote as one whole line and was written after the
// decision was asked. The newest comes first. The daemon finds the transcript
// itself, so nothing the agent supplies is trusted. Any miss
// refuses the answer.
func (d *Daemon) verifyHumanQuote(ctx context.Context, agentSessionID int64, decision domain.Decision, quote string) ([]humanEvidence, error) {
	needle := normalizeQuoteText(quote)
	if !hasLetterOrDigit(needle) {
		return nil, fmt.Errorf("%w: human_quote must contain a letter or a digit", ErrHumanQuoteUnverified)
	}
	sessionKey, err := d.store.AgentSessionKey(ctx, agentSessionID)
	if err != nil {
		return nil, fmt.Errorf("resolve session key: %w", err)
	}
	if sessionKey == "" {
		return nil, fmt.Errorf("%w: this session has no session_key", ErrHumanQuoteUnverified)
	}
	// The store keeps created_at in whole seconds, so a message in the second the
	// decision was asked could predate it. A real answer never comes that fast.
	since := decision.CreatedAt.Add(time.Second)
	candidates, listed := transcriptCandidates(sessionKey)
	if !listed {
		return nil, fmt.Errorf("%w: the transcript folders could not be listed", ErrHumanQuoteUnverified)
	}
	path, codex, err := harnessTranscript(sessionKey, candidates)
	if err != nil {
		return nil, err
	}
	match := func(line []byte) (humanEvidence, bool) {
		return transcriptLineHumanQuote(line, needle, since)
	}
	if codex {
		match = func(line []byte) (humanEvidence, bool) {
			return codexLineHumanQuote(line, needle, since)
		}
	}
	found, err := scanTranscript(path, match)
	if err != nil {
		return nil, fmt.Errorf("%w: read transcript: %v", ErrHumanQuoteUnverified, err)
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("%w: no prompt this session sent after decision %d was asked has the quote as a whole line; ask the human to answer on the dashboard", ErrHumanQuoteUnverified, decision.ID)
	}
	slices.Reverse(found)
	return found, nil
}

// harnessTranscript returns the one file among candidates that is this session's
// transcript, and whether it is a Codex rollout. It refuses when no candidate
// exists, when more than one does (a symlink counts as one), or when the file is
// not at a path the harness writes.
func harnessTranscript(sessionKey string, candidates []string) (string, bool, error) {
	var found []string
	for _, candidate := range candidates {
		_, err := os.Lstat(candidate)
		switch {
		case err == nil:
			found = append(found, candidate)
		case !isMissingPath(err):
			return "", false, fmt.Errorf("%w: a transcript candidate could not be checked", ErrHumanQuoteUnverified)
		}
	}
	switch len(found) {
	case 0:
		return "", false, fmt.Errorf("%w: no transcript exists for this session", ErrHumanQuoteUnverified)
	case 1:
	default:
		return "", false, fmt.Errorf("%w: more than one transcript exists for this session", ErrHumanQuoteUnverified)
	}
	path := found[0]
	if isCodexRolloutPath(path, sessionKey) {
		if !insideCodexSessions(path, sessionKey) {
			return "", false, fmt.Errorf("%w: transcript path is outside the Codex sessions directory", ErrHumanQuoteUnverified)
		}
		return path, true, nil
	}
	if !isHarnessTranscriptPath(path, sessionKey) {
		return "", false, fmt.Errorf("%w: transcript path is not this session's harness transcript", ErrHumanQuoteUnverified)
	}
	if !insideClaudeProjects(path, sessionKey) {
		return "", false, fmt.Errorf("%w: transcript path is outside the Claude Code projects directory", ErrHumanQuoteUnverified)
	}
	return path, false, nil
}

// isHarnessTranscriptPath accepts only the path Claude Code writes a session's
// transcript to: <config>/projects/<project>/<session_id>.jsonl. A file named for
// the session anywhere else could be one the agent wrote itself.
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
	for _, realRoot := range claudeProjectsRoots() {
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

// claudeProjectsRoots lists the resolved projects folders of CLAUDE_CONFIG_DIR and
// of the default ~/.claude, each once.
func claudeProjectsRoots() []string {
	var roots []string
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		roots = append(roots, filepath.Join(dir, "projects"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, ".claude", "projects"))
	}
	return resolvedRoots(roots)
}

// codexSessionsRoots lists the resolved sessions folders of CODEX_HOME and of the
// default ~/.codex, each once.
func codexSessionsRoots() []string {
	var roots []string
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		roots = append(roots, filepath.Join(dir, "sessions"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, ".codex", "sessions"))
	}
	return resolvedRoots(roots)
}

// resolvedRoots resolves each root that exists, dropping duplicates so one folder
// reached through two settings is counted once.
func resolvedRoots(roots []string) []string {
	seen := map[string]bool{}
	var resolved []string
	for _, root := range roots {
		realRoot, err := filepath.EvalSymlinks(root)
		if err != nil || seen[realRoot] {
			continue
		}
		seen[realRoot] = true
		resolved = append(resolved, realRoot)
	}
	return resolved
}

// transcriptCandidates lists every file under the Claude projects folders and the
// Codex sessions folders that could be this session's transcript: a Claude
// <session_key>.jsonl, or a Codex rollout named for the session. Both harnesses
// count together, so a copy in the other harness's folder is a second transcript.
// ok is false when a folder exists but cannot be listed.
func transcriptCandidates(sessionKey string) (candidates []string, ok bool) {
	codexName := codexRolloutName(sessionKey)
	roots := append(claudeProjectsRoots(), codexSessionsRoots()...)
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && (d.Name() == sessionKey+".jsonl" || codexName.MatchString(d.Name())) {
				candidates = append(candidates, path)
			}
			return nil
		})
		if err != nil && !isMissingPath(err) {
			return nil, false
		}
	}
	return candidates, true
}

// isMissingPath reports whether err only says the path is not there: it does not
// exist, or a parent on the way is not a folder. Any other error, such as a folder
// the daemon may not read, must refuse the answer rather than hide a candidate.
func isMissingPath(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// codexRolloutName matches a Codex rollout file for one session:
// rollout-<YYYY>-<MM>-<DD>T<HH>-<MM>-<SS>-<session_key>.jsonl.
func codexRolloutName(sessionKey string) *regexp.Regexp {
	return regexp.MustCompile(`^rollout-\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-` + regexp.QuoteMeta(sessionKey) + `\.jsonl$`)
}

var (
	codexYearDir  = regexp.MustCompile(`^\d{4}$`)
	codexMonthDir = regexp.MustCompile(`^\d{2}$`)
)

// isCodexRolloutPath is a lexical check on the candidate path: absolute, clean,
// and named like this session's rollout. The folder layout is checked by
// insideCodexSessions.
func isCodexRolloutPath(path, sessionKey string) bool {
	if sessionKey == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	return codexRolloutName(sessionKey).MatchString(filepath.Base(path))
}

// insideCodexSessions reports whether path is exactly <sessions>/YYYY/MM/DD/
// rollout-<time>-<session_key>.jsonl under the sessions folder of CODEX_HOME or of
// the default ~/.codex. Like the Claude check, the file must not be a symlink.
func insideCodexSessions(path, sessionKey string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	for _, realRoot := range codexSessionsRoots() {
		rel, err := filepath.Rel(realRoot, resolved)
		if err != nil {
			continue
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) == 4 && codexYearDir.MatchString(parts[0]) && codexMonthDir.MatchString(parts[1]) &&
			codexMonthDir.MatchString(parts[2]) && codexRolloutName(sessionKey).MatchString(parts[3]) {
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
	UUID      string `json:"uuid"`
	Timestamp string `json:"timestamp"`
	Origin    struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// scanTranscript runs match over every line of the transcript, with no line-length
// limit, and returns each human message match accepts, in file order.
func scanTranscript(path string, match func(line []byte) (humanEvidence, bool)) ([]humanEvidence, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var found []humanEvidence
	r := bufio.NewReader(f)
	for {
		line, readErr := r.ReadBytes('\n')
		if human, ok := match(line); ok {
			found = append(found, human)
		}
		if errors.Is(readErr, io.EOF) {
			return found, nil
		}
		if readErr != nil {
			return nil, readErr
		}
	}
}

// transcriptLineHumanQuote accepts a Claude transcript line that is a human message
// written at or after since and holding the needle as a whole line.
func transcriptLineHumanQuote(line []byte, needle string, since time.Time) (humanEvidence, bool) {
	var entry transcriptEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return humanEvidence{}, false
	}
	if entry.Type != "user" || entry.UUID == "" || entry.Origin.Kind != "human" {
		return humanEvidence{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
	if err != nil || at.Before(since) {
		return humanEvidence{}, false
	}
	text := messageText(entry.Message.Content)
	if !quoteLineMatches(text, needle) {
		return humanEvidence{}, false
	}
	return humanEvidence{ID: entry.UUID, Text: text}, true
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

// atctClientMessagePrefix marks a Codex turn input that ATCT sent itself. The
// cmd/atct side writes it as clientUserMessageId; a human's typed input has none.
const atctClientMessagePrefix = "atct:"

// codexRolloutEntry is the part of one Codex rollout line that the human-quote
// check reads.
type codexRolloutEntry struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type string `json:"type"`
		Item struct {
			ID       string          `json:"id"`
			Type     string          `json:"type"`
			ClientID string          `json:"client_id"`
			Content  json.RawMessage `json:"content"`
		} `json:"item"`
	} `json:"payload"`
}

// codexLineHumanQuote accepts only a user message the human typed: a UserMessage
// item with a client_id that ATCT did not set. Codex's own inputs carry no
// client_id, so they are refused as well. The item's ID is the message's own ID.
func codexLineHumanQuote(line []byte, needle string, since time.Time) (humanEvidence, bool) {
	var entry codexRolloutEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return humanEvidence{}, false
	}
	item := entry.Payload.Item
	if entry.Type != "event_msg" || entry.Payload.Type != "item_completed" || item.Type != "UserMessage" {
		return humanEvidence{}, false
	}
	if item.ID == "" || item.ClientID == "" || strings.HasPrefix(item.ClientID, atctClientMessagePrefix) {
		return humanEvidence{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
	if err != nil || at.Before(since) {
		return humanEvidence{}, false
	}
	text := codexMessageText(item.Content)
	if !quoteLineMatches(text, needle) {
		return humanEvidence{}, false
	}
	return humanEvidence{ID: item.ID, Text: text}, true
}

// codexMessageText joins the text parts of a Codex message with newlines, so each
// part is a line and a quote must sit inside one part.
func codexMessageText(raw json.RawMessage) string {
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var texts []string
	for _, part := range parts {
		if part.Type == "text" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}
