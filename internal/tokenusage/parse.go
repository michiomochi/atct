// Package tokenusage measures token usage by role x factor from Claude Code
// transcripts (~/.claude/projects/**/*.jsonl). Read-only; body sizes are
// chars/4 (rough), and weights only rank cost.
package tokenusage

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Usage is one message's (or a sum of messages') token counts.
type Usage struct {
	Input         int64 `json:"input"`
	CacheCreation int64 `json:"cache_creation"`
	CacheRead     int64 `json:"cache_read"`
	Output        int64 `json:"output"`
}

// W ranks cost: input 1, cache read 0.1, cache write 2, output 5.
func (u Usage) W() float64 {
	return float64(u.Input) + float64(u.CacheCreation)*2 + float64(u.CacheRead)*0.1 + float64(u.Output)*5
}

// In is everything read into the context: input + cache read + cache write.
func (u Usage) In() int64 { return u.Input + u.CacheRead + u.CacheCreation }

func (u *Usage) add(v Usage) {
	u.Input += v.Input
	u.CacheCreation += v.CacheCreation
	u.CacheRead += v.CacheRead
	u.Output += v.Output
}

// Bucket is a count of assistant turns and their usage.
type Bucket struct {
	N     int
	Usage Usage
}

// StopKind is one Stop hook reason: blocks, and the follow-up turns that had
// no tool_use (Idle) or had one (Work).
type StopKind struct {
	N    int
	Idle *Bucket
	Work *Bucket
}

// Pair is (actual cache_creation+input, estimated tokens from bodies) of one turn.
type Pair struct{ Actual, Estimated float64 }

// Session is the aggregate of one transcript file.
type Session struct {
	File           string
	Calls          map[string]bool
	FirstTS        string
	Turns          int
	Compactions    int
	Usage          Usage
	F              map[string]float64 // factor -> estimated tokens
	Bash           map[string]float64
	Rejects        int
	RejectReceives int
	Stop           map[string]*StopKind
	Notif          map[string]*Bucket
	StopBlocks     int
	StopExtraTurns int
	StopExtraInput int64
	Pairs          []Pair
	// set by Collect
	Goal    *int
	Role    string
	Session string
}

var alwaysAttachments = map[string]bool{
	"skill_listing": true, "deferred_tools_delta": true, "mcp_instructions_delta": true, "agent_listing_delta": true,
	"output_style": true, "output_style_instructions": true, "total_tokens_reminder": true, "instructions": true,
	"session_context": true, "environment": true, "silent_turn_reminder": true, "model": true, "language": true, "date": true,
}

type event struct {
	kind  string // msg, notif, human, block, clean
	usage Usage
	tool  bool
	label string
}

type toolCall struct {
	name  string
	input map[string]any
}

type parser struct {
	s         *Session
	tools     map[string]toolCall
	skills    []string
	seen      map[string]*event
	events    []*event
	sinceLast float64
	prev      int64
	hasPrev   bool
}

// ParseSession streams one jsonl file in a single pass.
func ParseSession(path string) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s, err := Parse(f)
	if s != nil {
		s.File = path
	}
	return s, err
}

// Parse streams jsonl lines of any length (a line can be several MB).
func Parse(r io.Reader) (*Session, error) {
	p := &parser{
		s:     &Session{Calls: map[string]bool{}, F: map[string]float64{}, Bash: map[string]float64{}},
		tools: map[string]toolCall{}, seen: map[string]*event{},
	}
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			p.line(line)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	stopAndNotif(p.s, p.events)
	return p.s, nil
}

func (p *parser) line(raw []byte) {
	var d map[string]any
	if json.Unmarshal(raw, &d) != nil || d == nil {
		return
	}
	s := p.s
	if s.FirstTS == "" {
		s.FirstTS = str(d["timestamp"])
	}
	add := 0.0
	c := func(key string, chars int) {
		v := float64(chars) / 4
		s.F[key] += v
		add += v
	}
	switch str(d["type"]) {
	case "assistant":
		p.assistant(asMap(d["message"]))
		return
	case "system":
		switch str(d["subtype"]) {
		case "compact_boundary":
			s.Compactions++
			p.hasPrev, p.sinceLast = false, 0 // cache rewritten; pairs across it are meaningless
		case "stop_hook_summary":
			if !truthy(d["hookErrors"]) {
				p.events = append(p.events, &event{kind: "clean"})
			}
		}
		return
	case "user":
		switch c0 := asMap(d["message"])["content"].(type) {
		case string:
			switch {
			case strings.Contains(c0, "<task-notification"):
				c("notification", runes(c0))
				if startsNotif(c0) {
					p.events = append(p.events, &event{kind: "notif", label: nkind(c0)})
				}
			case strings.HasPrefix(c0, "Stop hook feedback:"):
				c("hook:stop_feedback", runes(c0))
			case truthy(d["isCompactSummary"]):
				c("compaction_summary", runes(c0))
			default:
				c("user_prompt", runes(c0))
				p.events = append(p.events, &event{kind: "human"})
			}
		case []any:
			for _, b := range c0 {
				p.userBlock(asMap(b), c)
			}
		}
	case "attachment":
		a := asMap(d["attachment"])
		at := str(a["type"])
		switch {
		case at == "hook_blocking_error":
			e := a["blockingError"]
			c("hook:"+at, size(e))
			if str(a["hookEvent"]) == "Stop" {
				p.events = append(p.events, &event{kind: "block", label: bkind(e)})
			}
		case at == "hook_success":
			v := a["content"]
			if !truthy(v) {
				v = a["stdout"]
			}
			c("hook:"+at, pyLen(v))
		case at == "hook_additional_context":
			c("hook:"+at, size(a["content"]))
		case at == "queued_command":
			pr := str(a["prompt"])
			key := "queued_other"
			if strings.Contains(pr, "<task-notification") {
				key = "notification"
			}
			c(key, size(a["prompt"]))
			if startsNotif(pr) {
				p.events = append(p.events, &event{kind: "notif", label: nkind(pr)})
			}
		case alwaysAttachments[at]:
			c("always:"+at, size(a))
		}
	}
	p.sinceLast += add
}

func (p *parser) assistant(m map[string]any) {
	s := p.s
	id := str(m["id"])
	if _, ok := p.seen[id]; !ok { // one message is split over several lines; count usage once
		u := asMap(m["usage"])
		v := Usage{Input: num(u["input_tokens"]), CacheCreation: num(u["cache_creation_input_tokens"]),
			CacheRead: num(u["cache_read_input_tokens"]), Output: num(u["output_tokens"])}
		e := &event{kind: "msg", usage: v}
		p.seen[id] = e
		p.events = append(p.events, e)
		s.Turns++
		s.Usage.add(v)
		if p.hasPrev {
			s.Pairs = append(s.Pairs, Pair{float64(v.CacheCreation + v.Input), p.sinceLast + float64(p.prev)})
		}
		p.prev, p.hasPrev, p.sinceLast = v.Output, true, 0
	}
	blocks, _ := m["content"].([]any)
	for _, b := range blocks {
		bm := asMap(b)
		if str(bm["type"]) != "tool_use" {
			continue
		}
		n := str(bm["name"])
		input := asMap(bm["input"])
		p.tools[str(bm["id"])] = toolCall{n, input}
		p.seen[id].tool = true
		call := n
		if strings.HasPrefix(n, "mcp__atct__") {
			call = n[strings.LastIndex(n, "__")+2:]
		}
		s.Calls[call] = true
		switch {
		case strings.HasSuffix(n, "_review_reject"):
			s.Rejects++
		case strings.HasSuffix(n, "_review_reject_receive"):
			s.RejectReceives++
		case n == "Skill":
			sk := "?"
			if v, ok := input["skill"]; ok {
				sk = str(v)
			}
			p.skills = append(p.skills, sk)
		}
	}
}

func (p *parser) userBlock(b map[string]any, c func(string, int)) {
	s := p.s
	switch str(b["type"]) {
	case "tool_result":
		tc, ok := p.tools[str(b["tool_use_id"])]
		if !ok {
			tc.name = "?"
		}
		n := size(b["content"])
		c("tool_result:"+tc.name, n)
		if tc.name == "Bash" {
			s.Bash[bashHead(str(tc.input["command"]))] += float64(n) / 4
		}
	case "text":
		x := str(b["text"])
		switch {
		case strings.HasPrefix(x, "Base directory for this skill"):
			name := "?"
			if len(p.skills) > 0 {
				name, p.skills = p.skills[0], p.skills[1:]
			}
			c("skill:"+name, runes(x))
		case strings.Contains(x, "<task-notification"):
			c("notification", runes(x))
			if startsNotif(x) {
				p.events = append(p.events, &event{kind: "notif", label: nkind(x)})
			}
		case strings.Contains(x, "<system-reminder"):
			c("always:system-reminder", runes(x))
		default:
			c("user_prompt", runes(x))
			p.events = append(p.events, &event{kind: "human"})
		}
	}
}

// stopAndNotif walks events: Stop-block follow-up turns (idle = no tool_use)
// and idle-then-notification turns.
func stopAndNotif(s *Session, events []*event) {
	st, nt := map[string]*StopKind{}, map[string]*Bucket{}
	var blocked, nk, turn string
	lastTool := false
	notif := func(k string) *Bucket {
		if nt[k] == nil {
			nt[k] = &Bucket{}
		}
		return nt[k]
	}
	stop := func(k string) *StopKind {
		if st[k] == nil {
			st[k] = &StopKind{}
		}
		return st[k]
	}
	for _, e := range events {
		switch e.kind {
		case "msg":
			if nk != "" {
				turn, nk = nk, ""
				notif(turn).N++
			}
			if turn != "" {
				notif(turn).Usage.add(e.usage)
				if !e.tool {
					turn = ""
				}
			}
			if blocked != "" {
				b := stop(blocked)
				part := &b.Idle
				if e.tool {
					part = &b.Work
				}
				if *part == nil {
					*part = &Bucket{}
				}
				(*part).N++
				(*part).Usage.add(e.usage)
			}
			lastTool = e.tool
		case "notif":
			if !lastTool && nk == "" { // idle: this notification starts a turn
				nk, blocked = e.label, ""
			}
		case "block":
			blocked = e.label
			stop(blocked).N++
		default: // human / clean
			blocked, nk, turn = "", "", ""
		}
	}
	s.Stop, s.Notif = st, nt
	for _, b := range st {
		s.StopBlocks += b.N
		for _, p := range []*Bucket{b.Idle, b.Work} {
			if p != nil {
				s.StopExtraTurns += p.N
				s.StopExtraInput += p.Usage.In()
			}
		}
	}
}

var (
	cdPrefix   = regexp.MustCompile(`^\s*(?:cd\s+\S+\s*(?:&&|;)\s*)+`)
	assignment = regexp.MustCompile(`^\w+=`)
)

func bashHead(cmd string) string {
	var w []string
	for _, t := range strings.Fields(cdPrefix.ReplaceAllString(cmd, "")) {
		if !assignment.MatchString(t) {
			w = append(w, t)
		}
	}
	if len(w) == 0 {
		return "?"
	}
	switch w[0] {
	case "go", "pnpm", "npm", "gh", "atct":
		if len(w) > 1 {
			return w[0] + " " + w[1]
		}
	}
	return w[0]
}

var hasDigit = func(w string) bool { return strings.IndexFunc(w, unicode.IsDigit) >= 0 }

// bkind turns a Stop hook reason into a kind (ids and numbers dropped).
func bkind(e any) string {
	if m, ok := e.(map[string]any); ok {
		e = m["blockingError"]
	}
	t := str(e)
	if strings.HasPrefix(t, "ATCT stop-check failed: ") {
		r := t[len("ATCT stop-check failed: "):]
		if i := strings.IndexAny(r, ":,"); i >= 0 {
			r = r[:i]
		}
		return "stop-check failed: " + strings.TrimSpace(r)
	}
	var words []string
	for _, w := range strings.Fields(strings.Replace(t, "ATCT work remains: ", "", 1)) {
		if w == "for" || strings.HasSuffix(w, ":") || hasDigit(w) {
			break
		}
		words = append(words, w)
	}
	if len(words) == 0 {
		return "?"
	}
	return strings.Join(words, " ")
}

var (
	eventLine = regexp.MustCompile(`<event>\s*(.*)`)
	summary   = regexp.MustCompile(`(?s)<summary>(.*?)</summary>`)
	kindEnd   = regexp.MustCompile(`\s*[(:]|\s+\d|\s+after\b`)
)

// nkind turns a task-notification into an event kind from the first <event>
// (or <summary>) line.
func nkind(text string) string {
	var e string
	if m := eventLine.FindStringSubmatch(text); m != nil {
		e = strings.TrimSpace(m[1])
	} else { // no <event>: classify by <summary>, dropping the quoted task name
		sm := "?"
		if m := summary.FindStringSubmatch(text); m != nil {
			sm = m[1]
		}
		if strings.Contains(sm, `"`) {
			tail := sm[strings.LastIndex(sm, `"`)+1:]
			if i := strings.Index(tail, " ("); i >= 0 {
				tail = tail[:i]
			}
			sm = sm[:strings.Index(sm, `"`)] + " " + tail
		}
		f := strings.Fields(sm)
		if len(f) > 5 {
			f = f[:5]
		}
		e = "summary- " + strings.Join(f, " ")
	}
	e = strings.TrimPrefix(strings.TrimLeft(e, "["), "atct ")
	if loc := kindEnd.FindStringIndex(e); loc != nil {
		e = e[:loc[0]]
	}
	if r := []rune(e); len(r) > 50 {
		e = string(r[:50])
	}
	e = strings.Replace(strings.ToLower(e), "summary- ", "summary: ", -1)
	if e == "" {
		return "?"
	}
	return e
}

func startsNotif(s string) bool {
	return strings.HasPrefix(strings.TrimLeftFunc(s, unicode.IsSpace), "<task-notification")
}

// size counts characters in all string leaves (keys named "type" are skipped).
func size(x any) int {
	switch v := x.(type) {
	case string:
		return runes(v)
	case map[string]any:
		n := 0
		for k, e := range v {
			if k != "type" {
				n += size(e)
			}
		}
		return n
	case []any:
		n := 0
		for _, e := range v {
			n += size(e)
		}
		return n
	}
	return 0
}

func runes(s string) int { return utf8.RuneCountInString(s) }

func pyLen(x any) int {
	switch v := x.(type) {
	case string:
		return runes(v)
	case []any:
		return len(v)
	case map[string]any:
		return len(v)
	}
	return 0
}

func str(x any) string { s, _ := x.(string); return s }

func asMap(x any) map[string]any { m, _ := x.(map[string]any); return m }

func num(x any) int64 { f, _ := x.(float64); return int64(f) }

func truthy(x any) bool {
	switch v := x.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case float64:
		return v != 0
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	}
	return true
}
