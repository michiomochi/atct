package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michiomochi/atct/internal/domain"
	"github.com/michiomochi/atct/internal/store"
)

// chatAnswerFixture identifies the subcommander and commander sessions with
// session keys, the way SessionStart does, and gives each session a transcript
// at the path Claude Code uses: <config>/projects/<project>/<session_key>.jsonl.
type chatAnswerFixture struct {
	unappliedDecisionScopeRPCTestFixture
	subKey      string
	cmdKey      string
	projectsDir string
}

func newChatAnswerFixture(t *testing.T) chatAnswerFixture {
	t.Helper()
	f := newUnappliedDecisionScopeRPCTestFixture(t)
	ctx := t.Context()
	subKey, cmdKey := "chat-answer-sub", "chat-answer-cmd"
	if _, _, err := f.store.IdentifyAgentSession(ctx, f.subcommanderSessionID, subKey); err != nil {
		t.Fatalf("IdentifyAgentSession subcommander: %v", err)
	}
	if _, _, err := f.store.IdentifyAgentSession(ctx, f.commanderSessionID, cmdKey); err != nil {
		t.Fatalf("IdentifyAgentSession commander: %v", err)
	}
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	projectsDir := filepath.Join(configDir, "projects", "atct-test")
	if err := os.MkdirAll(projectsDir, 0o700); err != nil {
		t.Fatalf("MkdirAll projects: %v", err)
	}
	return chatAnswerFixture{unappliedDecisionScopeRPCTestFixture: f, subKey: subKey, cmdKey: cmdKey, projectsDir: projectsDir}
}

func (f chatAnswerFixture) transcriptPath(sessionKey string) string {
	return filepath.Join(f.projectsDir, sessionKey+".jsonl")
}

// askChatDecision asks a decision in goal A from the subcommander with two options.
func (f chatAnswerFixture) askChatDecision(t *testing.T) domain.Decision {
	t.Helper()
	d, err := f.store.AskDecision(t.Context(), store.AskInput{
		GoalID: f.goalAID, Kind: domain.KindDecision, Question: "which option?",
		Options:        []domain.Option{{Label: "A", Description: "a"}, {Label: "B", Description: "b"}},
		AgentSessionID: f.subcommanderSessionID,
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	return d
}

// recordPrompt records a human prompt and appends the matching human message to
// the session's transcript.
func (f chatAnswerFixture) recordPrompt(t *testing.T, sessionKey, promptID, prompt, transcriptText string, recordedAt time.Time) {
	t.Helper()
	f.recordPromptLine(t, sessionKey, promptID, prompt, transcriptText, true, recordedAt)
}

// recordPromptLine records a prompt and appends one transcript line for it. The
// line is a human message only when human is true.
func (f chatAnswerFixture) recordPromptLine(t *testing.T, sessionKey, promptID, prompt, transcriptText string, human bool, recordedAt time.Time) {
	t.Helper()
	path := f.transcriptPath(sessionKey)
	appendTranscriptLine(t, path, promptID, transcriptText, human)
	if err := f.store.RecordHumanPrompt(t.Context(), store.HumanPromptInput{
		SessionKey: sessionKey, PromptID: promptID, TranscriptPath: path, Prompt: prompt, RecordedAt: recordedAt,
	}); err != nil {
		t.Fatalf("RecordHumanPrompt: %v", err)
	}
}

func (f chatAnswerFixture) answerRPC(t *testing.T, agentSessionID int64, decisionID int64, answer, answerText, quote string) (json.RawMessage, json.RawMessage) {
	t.Helper()
	return chatAnswerRPC(t, f.socketPath, "decision.answer", map[string]any{
		"decision_id": decisionID, "answer": answer, "answer_text": answerText, "human_quote": quote,
		"agent_session_id": agentSessionID, "include_unapplied_answers": true,
	})
}

func chatAnswerRPC(t *testing.T, socketPath, method string, params map[string]any) (json.RawMessage, json.RawMessage) {
	t.Helper()
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial daemon: %v", err)
	}
	defer conn.Close()
	request := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		t.Fatalf("encode %s: %v", method, err)
	}
	var response struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		t.Fatalf("decode %s: %v", method, err)
	}
	return response.Result, response.Error
}

func refused(rpcError json.RawMessage) bool {
	return len(rpcError) > 0 && string(rpcError) != "null"
}

// appendTranscriptLine appends one transcript line. A human line carries the
// origin the daemon requires; a non-human line does not.
func appendTranscriptLine(t *testing.T, path, promptID, text string, human bool) {
	t.Helper()
	appendTranscriptLineAt(t, path, promptID, text, human, time.Now())
}

func appendTranscriptLineAt(t *testing.T, path, promptID, text string, human bool, at time.Time) {
	t.Helper()
	entry := map[string]any{
		"type": "user", "promptId": promptID, "timestamp": at.UTC().Format(time.RFC3339Nano),
		"message": map[string]any{"role": "user", "content": text},
	}
	if human {
		entry["origin"] = map[string]any{"kind": "human"}
	}
	line, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal transcript: %v", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	defer file.Close()
	if _, err := file.Write(append(line, '\n')); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

func TestChatAnswerRecordsVerifiedHumanQuote(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	prompt := "Aにしてください"
	f.recordPrompt(t, f.subKey, "pid-ok", prompt, prompt, time.Now())

	result, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "Aにしてください")
	if refused(rpcError) {
		t.Fatalf("decision.answer refused: %s", rpcError)
	}
	got, err := f.store.GetDecision(t.Context(), d.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.Status != domain.DecisionAnswered || got.AnswerLabel != "A" || got.AnsweredVia != "chat" ||
		got.AnswerQuote != "Aにしてください" || got.AnswerEvidence != prompt {
		t.Fatalf("decision = %+v, want answered A via chat with quote and evidence; result %s", got, result)
	}
}

func TestChatAnswerFallsBackToOlderHumanPrompt(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	// The older prompt is a real human message. The newer one holds the same
	// quote, but it came from a non-human source such as a task notification.
	human := "Aにしてください"
	f.recordPrompt(t, f.subKey, "pid-old", human, human, time.Now())
	notification := "<task-notification>Aにしてください</task-notification>"
	f.recordPromptLine(t, f.subKey, "pid-new", notification, notification, false, time.Now())

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "Aにしてください"); refused(rpcError) {
		t.Fatalf("answer refused although an older prompt is verified: %s", rpcError)
	}
	got, err := f.store.GetDecision(t.Context(), d.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.AnswerEvidence != human {
		t.Fatalf("answer_evidence = %q, want the older human prompt %q", got.AnswerEvidence, human)
	}
}

func TestChatAnswerRefusesFabricatedQuote(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	f.recordPrompt(t, f.subKey, "pid-fab", "Aにしてください", "Aにしてください", time.Now())

	_, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "B", "", "Bで進めてください")
	if !refused(rpcError) {
		t.Fatalf("fabricated quote was accepted")
	}
	got, _ := f.store.GetDecision(t.Context(), d.ID)
	if got.Status != domain.DecisionOpen {
		t.Fatalf("status = %s after refused answer, want open", got.Status)
	}
}

func TestChatAnswerRefusesQuoteOfPromptRecordedBeforeDecision(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	f.recordPrompt(t, f.subKey, "pid-early", "Aにしてください", "Aにしてください", d.CreatedAt.Add(-time.Hour))

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "Aにしてください"); !refused(rpcError) {
		t.Fatalf("quote from a prompt before the decision was accepted")
	}
}

func TestChatAnswerRefusesPromptFromAnotherSession(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	f.recordPrompt(t, "chat-answer-other", "pid-other", "Aにしてください", "Aにしてください", time.Now())

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "Aにしてください"); !refused(rpcError) {
		t.Fatalf("quote from another session's prompt was accepted")
	}
}

func TestChatAnswerRefusesTranscriptWithoutHumanOrigin(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	f.recordPromptLine(t, f.subKey, "pid-agent", "Aにしてください", "Aにしてください", false, time.Now())

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "Aにしてください"); !refused(rpcError) {
		t.Fatalf("transcript line without human origin was accepted")
	}
}

func TestChatAnswerRefusesMissingTranscript(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	// The path has the harness layout, but no file was ever written there.
	if err := f.store.RecordHumanPrompt(t.Context(), store.HumanPromptInput{
		SessionKey: f.subKey, PromptID: "pid-gone", TranscriptPath: f.transcriptPath(f.subKey),
		Prompt: "Aにしてください", RecordedAt: time.Now(),
	}); err != nil {
		t.Fatalf("RecordHumanPrompt: %v", err)
	}

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "Aにしてください"); !refused(rpcError) {
		t.Fatalf("answer with a missing transcript was accepted")
	}
}

func TestChatAnswerRefusesTranscriptOutsideHarnessLayout(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	// A perfectly valid human transcript, but written where the agent chose.
	fake := filepath.Join(t.TempDir(), "fake.jsonl")
	appendTranscriptLine(t, fake, "pid-fake", "Aにしてください", true)
	if err := f.store.RecordHumanPrompt(t.Context(), store.HumanPromptInput{
		SessionKey: f.subKey, PromptID: "pid-fake", TranscriptPath: fake, Prompt: "Aにしてください", RecordedAt: time.Now(),
	}); err != nil {
		t.Fatalf("RecordHumanPrompt: %v", err)
	}

	_, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "Aにしてください")
	if !refused(rpcError) {
		t.Fatalf("transcript outside the harness layout was accepted")
	}
	if !strings.Contains(string(rpcError), "transcript path is not this session's harness transcript") {
		t.Fatalf("refusal = %s, want the harness-transcript reason", rpcError)
	}
}

func TestChatAnswerRefusesTranscriptNamedForAnotherSession(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	other := filepath.Join(f.projectsDir, "chat-answer-other.jsonl")
	appendTranscriptLine(t, other, "pid-named", "Aにしてください", true)
	if err := f.store.RecordHumanPrompt(t.Context(), store.HumanPromptInput{
		SessionKey: f.subKey, PromptID: "pid-named", TranscriptPath: other, Prompt: "Aにしてください", RecordedAt: time.Now(),
	}); err != nil {
		t.Fatalf("RecordHumanPrompt: %v", err)
	}

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "Aにしてください"); !refused(rpcError) {
		t.Fatalf("transcript named for another session was accepted")
	}
}

func TestIsHarnessTranscriptPath(t *testing.T) {
	const key = "d44c0d31-1bda-44af-bb03-02149a03186c"
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"harness layout", "/home/u/.claude/projects/-repo/" + key + ".jsonl", true},
		{"config dir not named .claude", "/x/cfg/projects/p/" + key + ".jsonl", true},
		{"directly under /tmp", "/tmp/" + key + ".jsonl", false},
		{"not under projects", "/home/u/.claude/transcripts/p/" + key + ".jsonl", false},
		{"wrong file name", "/home/u/.claude/projects/p/other.jsonl", false},
		{"relative path", "projects/p/" + key + ".jsonl", false},
		{"unclean path", "/home/u/.claude/projects/p/../p/" + key + ".jsonl", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isHarnessTranscriptPath(tc.path, key); got != tc.want {
				t.Fatalf("isHarnessTranscriptPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
	if isHarnessTranscriptPath("/home/u/.claude/projects/p/"+key+".jsonl", "") {
		t.Fatalf("empty session key was accepted")
	}
}

func TestChatAnswerAcceptsOneCharacterWholeLine(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	f.recordPrompt(t, f.subKey, "pid-short", "A", "A", time.Now())

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "A"); refused(rpcError) {
		t.Fatalf("one-character whole-line quote refused: %s", rpcError)
	}
}

func TestChatAnswerRefusesQuoteWithoutLetterOrDigit(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	f.recordPrompt(t, f.subKey, "pid-punct", "。。", "。。", time.Now())

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "。。"); !refused(rpcError) {
		t.Fatalf("quote without a letter or digit was accepted")
	}
}

func TestChatAnswerRefusesQuoteInsideLongerLine(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	f.recordPrompt(t, f.subKey, "pid-part", "はい、Aにしてください", "はい、Aにしてください", time.Now())

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "Aにしてください"); !refused(rpcError) {
		t.Fatalf("quote that is only part of the human's line was accepted")
	}
}

func TestChatGoalReviewRefusesApproveTakenFromInsideALine(t *testing.T) {
	f := newChatAnswerFixture(t)
	review, err := f.store.AskDecision(t.Context(), store.AskInput{
		GoalID: f.goalAID, Kind: domain.KindGoalReview, Question: "approve goal A?",
	})
	if err != nil {
		t.Fatalf("AskDecision goal review: %v", err)
	}
	line := "まだ approve しないで、テストが落ちている"
	f.recordPrompt(t, f.cmdKey, "pid-not-yet", line, line, time.Now())

	if _, rpcError := f.answerRPC(t, f.commanderSessionID, review.ID, "approve", "", "approve"); !refused(rpcError) {
		t.Fatalf("approve was taken from inside the human's line")
	}
	got, err := f.store.GetDecision(t.Context(), review.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.Status != domain.DecisionOpen {
		t.Fatalf("goal review status = %s after refused approve, want open", got.Status)
	}
}

func TestChatAnswerMatchesWholeLineOfMultiLinePrompt(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	// The hook prompt holds the quote as one line; the transcript holds more.
	f.recordPrompt(t, f.subKey, "pid-multi", "次の件です\nAにしてください", "次の件です\nAにしてください\n詳細は後で", time.Now())

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "Aにしてください"); refused(rpcError) {
		t.Fatalf("whole line of a multi-line prompt refused: %s", rpcError)
	}
	got, err := f.store.GetDecision(t.Context(), d.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	// The evidence is the transcript's human message, not the hook's copy.
	if want := "次の件です\nAにしてください\n詳細は後で"; got.AnswerEvidence != want {
		t.Fatalf("answer_evidence = %q, want the transcript message %q", got.AnswerEvidence, want)
	}
}

func TestChatAnswerRefusesPromptRecordedAgainWithLaterTime(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	// The human's message was written before the decision was asked. The agent
	// re-records it through session.human_prompt.record, so the hook's time is now.
	appendTranscriptLineAt(t, f.transcriptPath(f.subKey), "pid-replayed", "Aにしてください", true, d.CreatedAt.Add(-time.Hour))
	if err := f.store.RecordHumanPrompt(t.Context(), store.HumanPromptInput{
		SessionKey: f.subKey, PromptID: "pid-replayed", TranscriptPath: f.transcriptPath(f.subKey),
		Prompt: "Aにしてください", RecordedAt: time.Now(),
	}); err != nil {
		t.Fatalf("RecordHumanPrompt: %v", err)
	}

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "Aにしてください"); !refused(rpcError) {
		t.Fatalf("message written before the decision was accepted through a later hook time")
	}
}

func TestChatAnswerRefusesPromptAlreadyUsedForAnotherDecision(t *testing.T) {
	f := newChatAnswerFixture(t)
	first := f.askChatDecision(t)
	second := f.askChatDecision(t)
	f.recordPrompt(t, f.subKey, "pid-once", "Aにしてください", "Aにしてください", time.Now())
	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, first.ID, "A", "", "Aにしてください"); refused(rpcError) {
		t.Fatalf("first answer refused: %s", rpcError)
	}

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, second.ID, "A", "", "Aにしてください"); !refused(rpcError) {
		t.Fatalf("one human message answered two decisions")
	}
	got, err := f.store.GetDecision(t.Context(), second.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.Status != domain.DecisionOpen {
		t.Fatalf("second decision status = %s after refused reuse, want open", got.Status)
	}
}

func TestChatAnswerRefusesTranscriptSymlinkedOutsideProjects(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	appendTranscriptLine(t, outside, "pid-link", "Aにしてください", true)
	link := f.transcriptPath(f.subKey)
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if err := f.store.RecordHumanPrompt(t.Context(), store.HumanPromptInput{
		SessionKey: f.subKey, PromptID: "pid-link", TranscriptPath: link, Prompt: "Aにしてください", RecordedAt: time.Now(),
	}); err != nil {
		t.Fatalf("RecordHumanPrompt: %v", err)
	}

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "Aにしてください"); !refused(rpcError) {
		t.Fatalf("symlink from the projects folder to a file outside it was accepted")
	}
}

func TestChatAnswerRefusesTranscriptThatDisagreesWithHookPrompt(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	// The hook recorded the quote, but the transcript's human message says something else.
	f.recordPrompt(t, f.subKey, "pid-mismatch", "Aにしてください", "Bにしてください", time.Now())

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "Aにしてください"); !refused(rpcError) {
		t.Fatalf("quote absent from the transcript's human message was accepted")
	}
}

func TestChatAnswerRefusesSessionThatDidNotAskTheDecision(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	swapKey := "chat-answer-swap"
	if _, _, err := f.store.IdentifyAgentSession(t.Context(), f.swappedSessionID, swapKey); err != nil {
		t.Fatalf("IdentifyAgentSession swapped: %v", err)
	}
	f.recordPrompt(t, swapKey, "pid-swap", "Aにしてください", "Aにしてください", time.Now())

	if _, rpcError := f.answerRPC(t, f.swappedSessionID, d.ID, "A", "", "Aにしてください"); !refused(rpcError) {
		t.Fatalf("session that did not ask the decision was allowed to answer it")
	}
}

func TestChatAnswerRefusesSecondAnswer(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	f.recordPrompt(t, f.subKey, "pid-twice", "Aにしてください", "Aにしてください", time.Now())
	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "Aにしてください"); refused(rpcError) {
		t.Fatalf("first answer refused: %s", rpcError)
	}

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "B", "", "Aにしてください"); !refused(rpcError) {
		t.Fatalf("second answer was accepted")
	}
}

func TestChatAnswerRefusesUnknownOptionLabel(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	f.recordPrompt(t, f.subKey, "pid-label", "Cにしてください", "Cにしてください", time.Now())

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "C", "", "Cにしてください"); !refused(rpcError) {
		t.Fatalf("label that is not an option was accepted")
	}
}

func TestChatAnswerRefusesCompletionDecision(t *testing.T) {
	f := newChatAnswerFixture(t)
	d, err := f.store.AskDecision(t.Context(), store.AskInput{
		GoalID: f.goalAID, Kind: domain.DecisionKind("completion"), Question: "done?",
		AgentSessionID: f.subcommanderSessionID,
	})
	if err != nil {
		t.Fatalf("AskDecision: %v", err)
	}
	f.recordPrompt(t, f.subKey, "pid-completion", "はい、完了です", "はい、完了です", time.Now())

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "", "はい、完了です", "はい、完了です"); !refused(rpcError) {
		t.Fatalf("completion decision was answered from the chat")
	}
}

func TestChatGoalReviewApproveFromCommanderChat(t *testing.T) {
	f := newChatAnswerFixture(t)
	review, err := f.store.AskDecision(t.Context(), store.AskInput{
		GoalID: f.goalAID, Kind: domain.KindGoalReview, Question: "approve goal A?",
	})
	if err != nil {
		t.Fatalf("AskDecision goal review: %v", err)
	}
	f.recordPrompt(t, f.cmdKey, "pid-approve", "承認します", "承認します", time.Now())

	if _, rpcError := f.answerRPC(t, f.commanderSessionID, review.ID, "approve", "", "承認します"); refused(rpcError) {
		t.Fatalf("approve refused: %s", rpcError)
	}
	got, err := f.store.GetDecision(t.Context(), review.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.Status != domain.DecisionApplied || got.AnsweredVia != "chat" || got.AnswerQuote != "承認します" {
		t.Fatalf("goal review = %+v, want applied via chat", got)
	}
}

func TestChatGoalReviewRejectsOptionLabel(t *testing.T) {
	f := newChatAnswerFixture(t)
	review, err := f.store.AskDecision(t.Context(), store.AskInput{
		GoalID: f.goalAID, Kind: domain.KindGoalReview, Question: "approve goal A?",
	})
	if err != nil {
		t.Fatalf("AskDecision goal review: %v", err)
	}
	f.recordPrompt(t, f.cmdKey, "pid-review-label", "承認します", "承認します", time.Now())

	if _, rpcError := f.answerRPC(t, f.commanderSessionID, review.ID, "A", "", "承認します"); !refused(rpcError) {
		t.Fatalf("goal_review accepted an option label")
	}
}

func TestChatAnswerRecordsHumanPromptThroughRPC(t *testing.T) {
	f := newChatAnswerFixture(t)
	_, rpcError := chatAnswerRPC(t, f.socketPath, "session.human_prompt.record", map[string]any{
		"session_key": f.subKey, "prompt_id": "pid-rpc", "transcript_path": "/tmp/none.jsonl", "prompt": "Aにしてください",
	})
	if refused(rpcError) {
		t.Fatalf("session.human_prompt.record refused: %s", rpcError)
	}
	got, err := f.store.HumanPromptsSince(t.Context(), f.subKey, time.Time{})
	if err != nil {
		t.Fatalf("HumanPromptsSince: %v", err)
	}
	if len(got) != 1 || !strings.Contains(got[0].Prompt, "Aにしてください") {
		t.Fatalf("recorded prompts = %+v, want the one prompt", got)
	}

	if _, rpcError := chatAnswerRPC(t, f.socketPath, "session.human_prompt.record", map[string]any{
		"session_key": "", "prompt": "x",
	}); !refused(rpcError) {
		t.Fatalf("record with empty session_key was accepted")
	}
}
