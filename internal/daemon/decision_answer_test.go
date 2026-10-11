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

// answerTime is a time after the decision was asked, past the store's whole-second
// created_at, so a human message written then counts.
func answerTime(d domain.Decision) time.Time {
	return d.CreatedAt.Add(2 * time.Second)
}

// recordPromptLine records a prompt and appends one transcript line for it. The
// line is a human message only when human is true.
func (f chatAnswerFixture) recordPromptLine(t *testing.T, sessionKey, promptID, prompt, transcriptText string, human bool, recordedAt time.Time) {
	t.Helper()
	path := f.transcriptPath(sessionKey)
	appendTranscriptLineAt(t, path, promptID, transcriptText, human, recordedAt)
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
	f.recordPrompt(t, f.subKey, "pid-ok", prompt, prompt, answerTime(d))

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
	f.recordPrompt(t, f.subKey, "pid-old", human, human, answerTime(d))
	notification := "<task-notification>Aにしてください</task-notification>"
	f.recordPromptLine(t, f.subKey, "pid-new", notification, notification, false, answerTime(d))

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
	f.recordPrompt(t, f.subKey, "pid-fab", "Aにしてください", "Aにしてください", answerTime(d))

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
	f.recordPrompt(t, "chat-answer-other", "pid-other", "Aにしてください", "Aにしてください", answerTime(d))

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "Aにしてください"); !refused(rpcError) {
		t.Fatalf("quote from another session's prompt was accepted")
	}
}

func TestChatAnswerRefusesTranscriptWithoutHumanOrigin(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	f.recordPromptLine(t, f.subKey, "pid-agent", "Aにしてください", "Aにしてください", false, answerTime(d))

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
	appendTranscriptLineAt(t, fake, "pid-fake", "Aにしてください", true, answerTime(d))
	if err := f.store.RecordHumanPrompt(t.Context(), store.HumanPromptInput{
		SessionKey: f.subKey, PromptID: "pid-fake", TranscriptPath: fake, Prompt: "Aにしてください", RecordedAt: answerTime(d),
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
	appendTranscriptLineAt(t, other, "pid-named", "Aにしてください", true, answerTime(d))
	if err := f.store.RecordHumanPrompt(t.Context(), store.HumanPromptInput{
		SessionKey: f.subKey, PromptID: "pid-named", TranscriptPath: other, Prompt: "Aにしてください", RecordedAt: answerTime(d),
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
	f.recordPrompt(t, f.subKey, "pid-short", "A", "A", answerTime(d))

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "A"); refused(rpcError) {
		t.Fatalf("one-character whole-line quote refused: %s", rpcError)
	}
}

func TestChatAnswerRefusesQuoteWithoutLetterOrDigit(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	f.recordPrompt(t, f.subKey, "pid-punct", "。。", "。。", answerTime(d))

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "。。"); !refused(rpcError) {
		t.Fatalf("quote without a letter or digit was accepted")
	}
}

func TestChatAnswerRefusesQuoteInsideLongerLine(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	f.recordPrompt(t, f.subKey, "pid-part", "はい、Aにしてください", "はい、Aにしてください", answerTime(d))

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
	f.recordPrompt(t, f.cmdKey, "pid-not-yet", line, line, answerTime(review))

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
	f.recordPrompt(t, f.subKey, "pid-multi", "次の件です\nAにしてください", "次の件です\nAにしてください\n詳細は後で", answerTime(d))

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
	f.recordPrompt(t, f.subKey, "pid-once", "Aにしてください", "Aにしてください", answerTime(first))
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
	appendTranscriptLineAt(t, outside, "pid-link", "Aにしてください", true, answerTime(d))
	link := f.transcriptPath(f.subKey)
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if err := f.store.RecordHumanPrompt(t.Context(), store.HumanPromptInput{
		SessionKey: f.subKey, PromptID: "pid-link", TranscriptPath: link, Prompt: "Aにしてください", RecordedAt: answerTime(d),
	}); err != nil {
		t.Fatalf("RecordHumanPrompt: %v", err)
	}

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "Aにしてください"); !refused(rpcError) {
		t.Fatalf("symlink from the projects folder to a file outside it was accepted")
	}
}

func TestChatAnswerRefusesTranscriptSymlinkedWithinProjects(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	// Another session's transcript, in the same projects folder.
	other := filepath.Join(f.projectsDir, "chat-answer-other.jsonl")
	appendTranscriptLineAt(t, other, "pid-within", "Aにしてください", true, answerTime(d))
	link := f.transcriptPath(f.subKey)
	if err := os.Symlink(other, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if err := f.store.RecordHumanPrompt(t.Context(), store.HumanPromptInput{
		SessionKey: f.subKey, PromptID: "pid-within", TranscriptPath: link, Prompt: "Aにしてください", RecordedAt: answerTime(d),
	}); err != nil {
		t.Fatalf("RecordHumanPrompt: %v", err)
	}

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "Aにしてください"); !refused(rpcError) {
		t.Fatalf("symlink to another transcript inside the projects folder was accepted")
	}
}

func TestChatAnswerRefusesTranscriptInAutoMemoryFolder(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	// The agent can write under the projects folder too. This file is not at the
	// harness layout <projects>/<project>/<session_key>.jsonl.
	dir := filepath.Join(f.projectsDir, "memory", "projects", "x")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	fake := filepath.Join(dir, f.subKey+".jsonl")
	appendTranscriptLineAt(t, fake, "pid-memory", "Aにしてください", true, answerTime(d))
	if err := f.store.RecordHumanPrompt(t.Context(), store.HumanPromptInput{
		SessionKey: f.subKey, PromptID: "pid-memory", TranscriptPath: fake, Prompt: "Aにしてください", RecordedAt: answerTime(d),
	}); err != nil {
		t.Fatalf("RecordHumanPrompt: %v", err)
	}

	_, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "Aにしてください")
	if !refused(rpcError) {
		t.Fatalf("transcript in a nested folder under projects was accepted")
	}
	if !strings.Contains(string(rpcError), "transcript path is outside the Claude Code projects directory") {
		t.Fatalf("refusal = %s, want the projects-directory reason", rpcError)
	}
}

func TestChatAnswerRefusesHumanLineInTheSecondTheDecisionWasAsked(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	stored, err := f.store.GetDecision(t.Context(), d.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	// Half a second after created_at, which the store keeps in whole seconds.
	appendTranscriptLineAt(t, f.transcriptPath(f.subKey), "pid-early", "Aにしてください", true, stored.CreatedAt.Add(500*time.Millisecond))
	if err := f.store.RecordHumanPrompt(t.Context(), store.HumanPromptInput{
		SessionKey: f.subKey, PromptID: "pid-early", TranscriptPath: f.transcriptPath(f.subKey), Prompt: "Aにしてください", RecordedAt: answerTime(d),
	}); err != nil {
		t.Fatalf("RecordHumanPrompt: %v", err)
	}
	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "Aにしてください"); !refused(rpcError) {
		t.Fatalf("human line half a second after created_at was accepted")
	}

	// A human line one second after created_at is past the boundary.
	appendTranscriptLineAt(t, f.transcriptPath(f.subKey), "pid-later", "Aにしてください", true, stored.CreatedAt.Add(time.Second))
	if err := f.store.RecordHumanPrompt(t.Context(), store.HumanPromptInput{
		SessionKey: f.subKey, PromptID: "pid-later", TranscriptPath: f.transcriptPath(f.subKey), Prompt: "Aにしてください", RecordedAt: answerTime(d),
	}); err != nil {
		t.Fatalf("RecordHumanPrompt: %v", err)
	}
	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "Aにしてください"); refused(rpcError) {
		t.Fatalf("human line one second after created_at refused: %s", rpcError)
	}
}

func TestChatAnswerRefusesTranscriptThatDisagreesWithHookPrompt(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	// The hook recorded the quote, but the transcript's human message says something else.
	f.recordPrompt(t, f.subKey, "pid-mismatch", "Aにしてください", "Bにしてください", answerTime(d))

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
	f.recordPrompt(t, swapKey, "pid-swap", "Aにしてください", "Aにしてください", answerTime(d))

	if _, rpcError := f.answerRPC(t, f.swappedSessionID, d.ID, "A", "", "Aにしてください"); !refused(rpcError) {
		t.Fatalf("session that did not ask the decision was allowed to answer it")
	}
}

func TestChatAnswerRefusesSecondAnswer(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	f.recordPrompt(t, f.subKey, "pid-twice", "Aにしてください", "Aにしてください", answerTime(d))
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
	f.recordPrompt(t, f.subKey, "pid-label", "Cにしてください", "Cにしてください", answerTime(d))

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
	f.recordPrompt(t, f.subKey, "pid-completion", "はい、完了です", "はい、完了です", answerTime(d))

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
	f.recordPrompt(t, f.cmdKey, "pid-approve", "承認します", "承認します", answerTime(review))

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
	f.recordPrompt(t, f.cmdKey, "pid-review-label", "承認します", "承認します", answerTime(review))

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

// humanTUIClientID is the client_id Codex gives a message the human typed.
const humanTUIClientID = "0b6f5a9e-3c1d-4e8a-9d2b-7a4c1e2f5b60"

// newCodexChatAnswerFixture is the chat fixture with CODEX_HOME pointed at a
// temporary folder. It returns that folder's sessions directory.
func newCodexChatAnswerFixture(t *testing.T) (chatAnswerFixture, string) {
	t.Helper()
	f := newChatAnswerFixture(t)
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	return f, filepath.Join(codexHome, "sessions")
}

// codexRolloutFile is where Codex writes a session's rollout, under a dated folder.
func codexRolloutFile(t *testing.T, sessionsDir, sessionKey string) string {
	t.Helper()
	dir := filepath.Join(sessionsDir, "2026", "10", "11")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll rollout dir: %v", err)
	}
	return filepath.Join(dir, "rollout-2026-10-11T23-15-05-"+sessionKey+".jsonl")
}

// appendCodexUserMessage appends one item_completed line to a rollout. An empty
// clientID leaves client_id out, as Codex does for its own inputs.
func appendCodexUserMessage(t *testing.T, path, turnID, clientID, itemType, text string, at time.Time) {
	t.Helper()
	item := map[string]any{
		"type": itemType, "id": "item-" + turnID,
		"content": []map[string]any{{"type": "text", "text": text, "text_elements": []any{}}},
	}
	if clientID != "" {
		item["client_id"] = clientID
	}
	entry := map[string]any{
		"timestamp": at.UTC().Format(time.RFC3339Nano), "ordinal": 7, "type": "event_msg",
		"payload": map[string]any{"type": "item_completed", "thread_id": "thread", "turn_id": turnID, "item": item},
	}
	line, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal rollout: %v", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open rollout: %v", err)
	}
	defer file.Close()
	if _, err := file.Write(append(line, '\n')); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
}

// recordCodexPrompt writes the human's message into the rollout and records the
// hook's prompt for that turn.
func (f chatAnswerFixture) recordCodexPrompt(t *testing.T, path, sessionKey, turnID, prompt, text, clientID string, at time.Time) {
	t.Helper()
	appendCodexUserMessage(t, path, turnID, clientID, "UserMessage", text, at)
	if err := f.store.RecordHumanPrompt(t.Context(), store.HumanPromptInput{
		SessionKey: sessionKey, PromptID: turnID, TranscriptPath: path, Prompt: prompt, RecordedAt: at,
	}); err != nil {
		t.Fatalf("RecordHumanPrompt: %v", err)
	}
}

func TestChatAnswerRecordsVerifiedCodexQuote(t *testing.T) {
	f, sessions := newCodexChatAnswerFixture(t)
	d := f.askChatDecision(t)
	text := "Aにしてください"
	f.recordCodexPrompt(t, codexRolloutFile(t, sessions, f.subKey), f.subKey, "turn-ok", text, text, humanTUIClientID, answerTime(d))

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", text); refused(rpcError) {
		t.Fatalf("verified Codex quote refused: %s", rpcError)
	}
	got, err := f.store.GetDecision(t.Context(), d.ID)
	if err != nil {
		t.Fatalf("GetDecision: %v", err)
	}
	if got.Status != domain.DecisionAnswered || got.AnswerLabel != "A" || got.AnsweredVia != "chat" || got.AnswerEvidence != text {
		t.Fatalf("decision = %+v, want answered A via chat with the rollout message as evidence", got)
	}
}

// Each case is a rollout line that must not count as the human's typed message.
func TestChatAnswerRefusesCodexLineThatIsNotTheHumansWholeLine(t *testing.T) {
	const quote = "Aにしてください"
	cases := []struct {
		name     string
		lineTurn string
		clientID string
		itemType string
		text     string
		offset   time.Duration // from the decision's created_at
	}{
		{"no client_id", "turn-x", "", "UserMessage", quote, 2 * time.Second},
		{"client_id set by ATCT", "turn-x", "atct:6f1c", "UserMessage", quote, 2 * time.Second},
		{"not a user message", "turn-x", humanTUIClientID, "AgentMessage", quote, 2 * time.Second},
		{"another turn", "turn-other", humanTUIClientID, "UserMessage", quote, 2 * time.Second},
		{"written before the decision was asked", "turn-x", humanTUIClientID, "UserMessage", quote, 500 * time.Millisecond},
		{"quote is part of a longer line", "turn-x", humanTUIClientID, "UserMessage", "はい、" + quote, 2 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, sessions := newCodexChatAnswerFixture(t)
			d := f.askChatDecision(t)
			path := codexRolloutFile(t, sessions, f.subKey)
			// The hook's copy holds the quote; only the rollout line is different. The
			// offset is from the stored created_at, which is in whole seconds.
			stored, err := f.store.GetDecision(t.Context(), d.ID)
			if err != nil {
				t.Fatalf("GetDecision: %v", err)
			}
			appendCodexUserMessage(t, path, tc.lineTurn, tc.clientID, tc.itemType, tc.text, stored.CreatedAt.Add(tc.offset))
			if err := f.store.RecordHumanPrompt(t.Context(), store.HumanPromptInput{
				SessionKey: f.subKey, PromptID: "turn-x", TranscriptPath: path, Prompt: quote, RecordedAt: answerTime(d),
			}); err != nil {
				t.Fatalf("RecordHumanPrompt: %v", err)
			}
			if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", quote); !refused(rpcError) {
				t.Fatalf("rollout line accepted as the human's message")
			}
		})
	}
}

func TestChatAnswerRefusesCodexRolloutOutsideSessionsLayout(t *testing.T) {
	cases := []struct {
		name    string
		symlink bool
		path    func(f chatAnswerFixture, sessions string) string
	}{
		{"outside the sessions folder", false, func(f chatAnswerFixture, _ string) string {
			return filepath.Join(t.TempDir(), "rollout-2026-10-11T23-15-05-"+f.subKey+".jsonl")
		}},
		{"directly under the sessions folder", false, func(f chatAnswerFixture, sessions string) string {
			return filepath.Join(sessions, "rollout-2026-10-11T23-15-05-"+f.subKey+".jsonl")
		}},
		{"a day folder deeper", false, func(f chatAnswerFixture, sessions string) string {
			return filepath.Join(sessions, "2026", "10", "11", "extra", "rollout-2026-10-11T23-15-05-"+f.subKey+".jsonl")
		}},
		{"a folder that is not a date", false, func(f chatAnswerFixture, sessions string) string {
			return filepath.Join(sessions, "tmp", "10", "11", "rollout-2026-10-11T23-15-05-"+f.subKey+".jsonl")
		}},
		{"symlinked to a file outside", true, func(f chatAnswerFixture, sessions string) string {
			return filepath.Join(sessions, "2026", "10", "11", "rollout-2026-10-11T23-15-05-"+f.subKey+".jsonl")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, sessions := newCodexChatAnswerFixture(t)
			d := f.askChatDecision(t)
			path := tc.path(f, sessions)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if tc.symlink {
				target := filepath.Join(t.TempDir(), "outside.jsonl")
				appendCodexUserMessage(t, target, "turn-place", humanTUIClientID, "UserMessage", "Aにしてください", answerTime(d))
				if err := os.Symlink(target, path); err != nil {
					t.Fatalf("Symlink: %v", err)
				}
			} else {
				appendCodexUserMessage(t, path, "turn-place", humanTUIClientID, "UserMessage", "Aにしてください", answerTime(d))
			}
			if err := f.store.RecordHumanPrompt(t.Context(), store.HumanPromptInput{
				SessionKey: f.subKey, PromptID: "turn-place", TranscriptPath: path, Prompt: "Aにしてください", RecordedAt: answerTime(d),
			}); err != nil {
				t.Fatalf("RecordHumanPrompt: %v", err)
			}
			if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", "Aにしてください"); !refused(rpcError) {
				t.Fatalf("rollout at %s was accepted", path)
			}
		})
	}
}

func TestChatAnswerRefusesDuplicatedCodexRollout(t *testing.T) {
	f, sessions := newCodexChatAnswerFixture(t)
	d := f.askChatDecision(t)
	text := "Aにしてください"
	path := codexRolloutFile(t, sessions, f.subKey)
	f.recordCodexPrompt(t, path, f.subKey, "turn-dup", text, text, humanTUIClientID, answerTime(d))
	// A second rollout for the same session on another day, with the same turn.
	otherDay := filepath.Join(sessions, "2026", "10", "12", "rollout-2026-10-12T08-00-00-"+f.subKey+".jsonl")
	if err := os.MkdirAll(filepath.Dir(otherDay), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	appendCodexUserMessage(t, otherDay, "turn-dup", humanTUIClientID, "UserMessage", text, answerTime(d))

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", text); !refused(rpcError) {
		t.Fatalf("answer accepted although the session has two rollouts")
	}
}

func TestChatAnswerRefusesClaudeTranscriptDuplicatedInAnotherProject(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	text := "Aにしてください"
	f.recordPrompt(t, f.subKey, "pid-dup", text, text, answerTime(d))
	otherProject := filepath.Join(filepath.Dir(f.projectsDir), "atct-other")
	if err := os.MkdirAll(otherProject, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	appendTranscriptLineAt(t, filepath.Join(otherProject, f.subKey+".jsonl"), "pid-dup", text, true, answerTime(d))

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", text); !refused(rpcError) {
		t.Fatalf("answer accepted although the session has two transcripts")
	}
}

func TestChatAnswerRefusesCodexTurnAlreadyUsedForAnotherDecision(t *testing.T) {
	f, sessions := newCodexChatAnswerFixture(t)
	first := f.askChatDecision(t)
	second := f.askChatDecision(t)
	text := "Aにしてください"
	f.recordCodexPrompt(t, codexRolloutFile(t, sessions, f.subKey), f.subKey, "turn-once", text, text, humanTUIClientID, answerTime(first))
	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, first.ID, "A", "", text); refused(rpcError) {
		t.Fatalf("first answer refused: %s", rpcError)
	}

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, second.ID, "A", "", text); !refused(rpcError) {
		t.Fatalf("one Codex turn answered two decisions")
	}
}

// The agent can write a Codex rollout beside a real Claude transcript. Both harnesses
// count together, so the real transcript is no longer the only copy and is refused.
func TestChatAnswerRefusesCodexRolloutBesideClaudeTranscript(t *testing.T) {
	f, sessions := newCodexChatAnswerFixture(t)
	d := f.askChatDecision(t)
	text := "Aにしてください"
	f.recordPrompt(t, f.subKey, "pid-real", text, text, answerTime(d))
	fake := codexRolloutFile(t, sessions, f.subKey)
	appendCodexUserMessage(t, fake, "turn-fake", humanTUIClientID, "UserMessage", text, answerTime(d))
	if err := f.store.RecordHumanPrompt(t.Context(), store.HumanPromptInput{
		SessionKey: f.subKey, PromptID: "turn-fake", TranscriptPath: fake, Prompt: text, RecordedAt: answerTime(d),
	}); err != nil {
		t.Fatalf("RecordHumanPrompt: %v", err)
	}

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", text); !refused(rpcError) {
		t.Fatalf("answer accepted with a Codex rollout beside the Claude transcript")
	}
}

func TestChatAnswerRefusesClaudeTranscriptBesideCodexRollout(t *testing.T) {
	f, sessions := newCodexChatAnswerFixture(t)
	d := f.askChatDecision(t)
	text := "Aにしてください"
	f.recordCodexPrompt(t, codexRolloutFile(t, sessions, f.subKey), f.subKey, "turn-real", text, text, humanTUIClientID, answerTime(d))
	// A Claude transcript the agent wrote, in a valid projects folder.
	fake := f.transcriptPath(f.subKey)
	appendTranscriptLineAt(t, fake, "pid-fake", text, true, answerTime(d))
	if err := f.store.RecordHumanPrompt(t.Context(), store.HumanPromptInput{
		SessionKey: f.subKey, PromptID: "pid-fake", TranscriptPath: fake, Prompt: text, RecordedAt: answerTime(d),
	}); err != nil {
		t.Fatalf("RecordHumanPrompt: %v", err)
	}

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", text); !refused(rpcError) {
		t.Fatalf("answer accepted with a Claude transcript beside the Codex rollout")
	}
}

// The first named transcript is pinned. After the real transcript is gone, a copy
// the agent placed in another valid project is the only file left, and a prompt
// naming it is still refused.
func TestChatAnswerRefusesPromptNamingAnotherPathAfterPin(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	text := "Aにしてください"
	f.recordPrompt(t, f.subKey, "pid-pinned", text, text, answerTime(d))
	if err := os.Remove(f.transcriptPath(f.subKey)); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	otherProject := filepath.Join(filepath.Dir(f.projectsDir), "atct-other")
	if err := os.MkdirAll(otherProject, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	fake := filepath.Join(otherProject, f.subKey+".jsonl")
	appendTranscriptLineAt(t, fake, "pid-fake", text, true, answerTime(d))
	if err := f.store.RecordHumanPrompt(t.Context(), store.HumanPromptInput{
		SessionKey: f.subKey, PromptID: "pid-fake", TranscriptPath: fake, Prompt: text, RecordedAt: answerTime(d),
	}); err != nil {
		t.Fatalf("RecordHumanPrompt: %v", err)
	}

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", text); !refused(rpcError) {
		t.Fatalf("answer accepted from a transcript other than the pinned one")
	}
}

// The pinned transcript sits in a folder the daemon does not read, so the answer is
// refused even though the copy in the folder the daemon does read is intact.
func TestChatAnswerRefusesPinnedTranscriptOutsideKnownFolders(t *testing.T) {
	f := newChatAnswerFixture(t)
	d := f.askChatDecision(t)
	text := "Aにしてください"
	f.recordPrompt(t, f.subKey, "pid-unknown", text, text, answerTime(d))
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())

	if _, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", text); !refused(rpcError) {
		t.Fatalf("answer accepted from a transcript outside the folders the daemon reads")
	}
}

// A folder the daemon cannot list refuses the answer. Otherwise the folder could be
// made unreadable so that a copy placed elsewhere is the only rollout left.
func TestChatAnswerRefusesWhenTranscriptFolderCannotBeListed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any folder")
	}
	f, sessions := newCodexChatAnswerFixture(t)
	d := f.askChatDecision(t)
	text := "Aにしてください"
	path := codexRolloutFile(t, sessions, f.subKey)
	f.recordCodexPrompt(t, path, f.subKey, "turn-locked", text, text, humanTUIClientID, answerTime(d))
	dayDir := filepath.Dir(path)
	if err := os.Chmod(dayDir, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dayDir, 0o700) })

	_, rpcError := f.answerRPC(t, f.subcommanderSessionID, d.ID, "A", "", text)
	if !refused(rpcError) {
		t.Fatalf("answer accepted with the rollout folder unreadable")
	}
	if !strings.Contains(string(rpcError), "could not be listed") {
		t.Fatalf("refusal = %s, want the listing reason", rpcError)
	}
}
