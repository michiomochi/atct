package tokenusage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type m = map[string]any

func asst(i int, u [4]int64, blocks ...any) m {
	return m{"type": "assistant", "timestamp": "2026-10-03T00:00:00Z", "message": m{"id": "m" + string(rune('0'+i)),
		"usage":   m{"input_tokens": u[0], "cache_creation_input_tokens": u[1], "cache_read_input_tokens": u[2], "output_tokens": u[3]},
		"content": blocks}}
}

func tu(i int, name string, input m) m {
	return m{"type": "tool_use", "id": "t" + string(rune('0'+i)), "name": name, "input": input}
}

func tr(i int, text string) m {
	return m{"type": "user", "message": m{"content": []any{m{"type": "tool_result", "tool_use_id": "t" + string(rune('0'+i)), "content": text}}}}
}

var blk = m{"type": "attachment", "attachment": m{"type": "hook_blocking_error", "hookEvent": "Stop", "blockingError": m{
	"blockingError": "ATCT work remains: executor has open task handoff goal-9-task-1394-x for task 1394"}}}

func note(ev string) m {
	return m{"type": "user", "message": m{"content": "<task-notification>\n<task-id>x</task-id>\n<event>" + ev + "</event>\n</task-notification>"}}
}

const prefix = "-tmp-x"

func fixture(t *testing.T) map[string]*Session {
	t.Helper()
	files := map[string][]m{
		prefix + "/a.jsonl": { // commander in the main checkout
			asst(1, [4]int64{1, 100, 1000, 10}, tu(1, "Bash", m{"command": "cd /x && go test ./..."})),
			tr(1, strings.Repeat("x", 400)),
			{"type": "attachment", "attachment": m{"type": "hook_blocking_error", "hookEvent": "Stop",
				"blockingError": m{"blockingError": strings.Repeat("y", 80)}}},
			asst(2, [4]int64{1, 50, 1100, 20}, tu(2, "Skill", m{"skill": "foo"})),
			tr(2, "Launching"),
			{"type": "user", "message": m{"content": []any{m{"type": "text", "text": "Base directory for this skill: " + strings.Repeat("z", 100)}}}},
			{"type": "user", "message": m{"content": "<task-notification>" + strings.Repeat("n", 40) + "</task-notification>"}},
			{"type": "attachment", "attachment": m{"type": "skill_listing", "content": strings.Repeat("s", 200)}},
			asst(3, [4]int64{1, 10, 1200, 5}),
			asst(3, [4]int64{1, 10, 1200, 5}, tu(3, "mcp__atct__atct_project_claim", nil)), // same message id, 2nd line
		},
		prefix + "--worktrees-5/b.jsonl": {asst(1, [4]int64{1, 1, 1, 1}, tu(1, "mcp__atct__atct_goal_handoff_receive", nil))},
		prefix + "--worktrees-5/c.jsonl": {
			asst(1, [4]int64{1, 1, 1, 1}, tu(1, "mcp__atct__atct_task_handoff_receive", nil)),
			asst(2, [4]int64{1, 1, 1, 1}, tu(2, "mcp__atct__atct_task_handoff_review_reject", nil)),
			asst(3, [4]int64{1, 1, 1, 1}, tu(3, "mcp__atct__atct_task_handoff_review_reject_receive", nil)),
		},
		prefix + "--worktrees-6/d.jsonl": {asst(1, [4]int64{2, 0, 0, 3})},
		prefix + "--worktrees-7/e.jsonl": { // Stop blocks (idle vs work) and notification-started turns
			asst(1, [4]int64{1, 1, 1, 1}, tu(1, "mcp__atct__atct_task_handoff_receive", nil)),
			tr(1, "ok"),
			blk,
			asst(2, [4]int64{1, 10, 100, 1}), // idle spin
			blk,
			asst(3, [4]int64{1, 20, 200, 1}, tu(3, "Bash", m{"command": "ls"})), // work
			tr(3, "ok"),
			asst(4, [4]int64{1, 0, 300, 1}), // idle again
			{"type": "system", "subtype": "stop_hook_summary", "hookErrors": []any{}},
			note("atct handoff entry added: task 1394 (handoff y)"),
			asst(5, [4]int64{2, 0, 400, 1}, tu(5, "Bash", m{"command": "ls"})),
			tr(5, "ok"),
			asst(6, [4]int64{1, 0, 500, 1}), // ends the notification turn
			note("atct monitor liveness: recheck task 5"),
			asst(7, [4]int64{1, 0, 600, 1}),
		},
	}
	tmp := t.TempDir()
	for rel, lines := range files {
		var b strings.Builder
		for _, l := range lines {
			j, err := json.Marshal(l)
			if err != nil {
				t.Fatal(err)
			}
			b.Write(j)
			b.WriteByte('\n')
		}
		p := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	all, err := Collect(tmp, prefix, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*Session{}
	for _, s := range all {
		g := "main"
		if s.Goal != nil {
			g = string(rune('0' + *s.Goal))
		}
		out[g+"/"+s.Session] = s
	}
	return out
}

func TestRoleUsageAndFactors(t *testing.T) {
	ss := fixture(t)
	A, B, C, D := ss["main/a"], ss["5/b"], ss["5/c"], ss["6/d"]
	if got := []string{A.Role, B.Role, C.Role, D.Role}; strings.Join(got, ",") != "commander,subcommander,executor,unknown" {
		t.Fatalf("roles = %v", got)
	}
	if A.Usage != (Usage{Input: 3, CacheCreation: 160, CacheRead: 3300, Output: 35}) || A.Turns != 3 { // message.id dedup
		t.Fatalf("usage = %+v turns %d", A.Usage, A.Turns)
	}
	if w := A.Usage.W(); w != 3+320+330+175 {
		t.Fatalf("weight = %v", w)
	}
	if C.Rejects != 1 || C.RejectReceives != 1 {
		t.Fatalf("rejects = %d/%d", C.Rejects, C.RejectReceives)
	}
	f := A.F
	if f["tool_result:Bash"] != 100 || A.Bash["go test"] != 100 { // Bash head: cd prefix dropped
		t.Fatalf("bash = %v %v", f["tool_result:Bash"], A.Bash)
	}
	if f["hook:hook_blocking_error"] != 20 || A.StopBlocks != 1 || A.StopExtraTurns != 2 {
		t.Fatalf("hook = %v blocks %d extra %d", f["hook:hook_blocking_error"], A.StopBlocks, A.StopExtraTurns)
	}
	if f["notification"] != float64(19+40+20)/4 || f["skill:foo"] != 131.0/4 {
		t.Fatalf("notification %v skill %v", f["notification"], f["skill:foo"])
	}
	if f["always:skill_listing"] != 50 {
		t.Fatalf("skill_listing = %v", f["always:skill_listing"])
	}
}

func TestStopAndNotificationTurns(t *testing.T) {
	ss := fixture(t)
	E := ss["7/e"]
	if E.Role != "executor" || E.StopBlocks != 2 || E.StopExtraTurns != 3 {
		t.Fatalf("E = %s blocks %d extra %d", E.Role, E.StopBlocks, E.StopExtraTurns)
	}
	sk := E.Stop["executor has open task handoff"] // reason normalized: ids and numbers dropped
	if sk == nil || sk.N != 2 || sk.Idle.N != 2 || sk.Work.N != 1 {
		t.Fatalf("stop kind = %+v", sk)
	}
	if sk.Idle.Usage.CacheRead != 400 || sk.Work.Usage.CacheCreation != 20 {
		t.Fatalf("stop usage idle %+v work %+v", sk.Idle.Usage, sk.Work.Usage)
	}
	if len(E.Notif) != 2 || E.Notif["handoff entry added"].N != 1 || E.Notif["monitor liveness"].N != 1 {
		t.Fatalf("notif = %+v", E.Notif)
	}
	if E.Notif["handoff entry added"].Usage.CacheRead != 900 { // the whole turn: a5 + a6
		t.Fatalf("notif turn usage = %+v", E.Notif["handoff entry added"].Usage)
	}
}

func TestKinds(t *testing.T) {
	for in, want := range map[string]string{
		"ATCT work remains: commander has active goal 306: title":                      "commander has active goal",
		"ATCT work remains: commander has unreceived plan review for active goal 3: x": "commander has unreceived plan review",
		"ATCT stop-check failed: ensure daemon: foo":                                   "stop-check failed: ensure daemon",
	} {
		if got := bkind(in); got != want {
			t.Errorf("bkind(%q) = %q, want %q", in, got, want)
		}
	}
	if got := nkind("<event>[Monitor expired after 30m with 2 events delivered.</event>"); got != "monitor expired" {
		t.Errorf("nkind = %q", got)
	}
}

func TestReport(t *testing.T) {
	ss := fixture(t)
	var all []*Session
	for _, s := range ss {
		all = append(all, s)
	}
	rep := Build(all, all, nil)
	if k := rep.StopByKind["executor"]["executor has open task handoff"]; k == nil || k.Idle.N != 2 {
		t.Fatalf("stop_by_kind = %+v", k)
	}
	if n := rep.NotifTurns["executor"]["monitor liveness"]; n == nil || n.N != 1 {
		t.Fatalf("notif = %+v", n)
	}
	if rep.Roles["unknown"].Sessions != 1 || rep.EstimateCheck == nil || rep.EstimateCheck.Pairs != 10 {
		t.Fatalf("unknown %d check %+v", rep.Roles["unknown"].Sessions, rep.EstimateCheck)
	}
}

func TestLongLine(t *testing.T) { // a line far over bufio.Scanner's 64KiB default
	big := m{"type": "user", "message": m{"content": strings.Repeat("a", 5<<20)}}
	j, _ := json.Marshal(big)
	p := filepath.Join(t.TempDir(), "big.jsonl")
	os.WriteFile(p, append(j, '\n'), 0o644)
	s, err := ParseSession(p)
	if err != nil || s.F["user_prompt"] != float64(5<<20)/4 {
		t.Fatalf("err %v user_prompt %v", err, s.F["user_prompt"])
	}
}
