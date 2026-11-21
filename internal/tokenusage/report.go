package tokenusage

import (
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Roles in report order.
var Roles = []string{"commander", "subcommander", "executor", "unknown"}

// ReductionGoals are the goals whose merge splits the before/after comparison.
var ReductionGoals = []int{306, 325, 328, 315, 323, 316, 327, 331}

const minN = 3 // samples per side below this => not comparable

var worktreeSuffix = regexp.MustCompile(`--worktrees-(\d+)$`)

func roleOf(s *Session, isMain bool) string {
	switch {
	case s.Calls["atct_project_claim"]:
		return "commander"
	case s.Calls["atct_goal_handoff_receive"]:
		return "subcommander"
	case s.Calls["atct_task_handoff_receive"]:
		return "executor"
	case isMain:
		return "commander"
	}
	return "unknown"
}

// Collect parses every transcript under root/<prefix>[--worktrees-N] modified
// at or after since.
func Collect(root, prefix string, since time.Time) ([]*Session, error) {
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []*Session
	for _, d := range dirs {
		name := d.Name()
		if name != prefix && !strings.HasPrefix(name, prefix+"--worktrees-") {
			continue
		}
		var goal *int
		if m := worktreeSuffix.FindStringSubmatch(name); m != nil {
			g, _ := strconv.Atoi(m[1])
			goal = &g
		}
		files, _ := filepath.Glob(filepath.Join(root, name, "*.jsonl"))
		sort.Strings(files)
		for _, f := range files {
			fi, err := os.Stat(f)
			if err != nil || fi.ModTime().Before(since) {
				continue
			}
			s, err := ParseSession(f)
			if err != nil {
				return nil, err
			}
			s.Goal = goal
			s.Role = roleOf(s, goal == nil)
			s.Session = strings.TrimSuffix(filepath.Base(f), ".jsonl")
			if r := []rune(s.Session); len(r) > 8 {
				s.Session = string(r[:8])
			}
			out = append(out, s)
		}
	}
	return out, nil
}

var mergeGoal = regexp.MustCompile(`\bgoal (\d+)`)

// MergeTimes reads the merge commit time of each reduction goal from `git log`
// of main in repoDir (the newest merge wins). A git failure yields no times.
func MergeTimes(repoDir string) map[int]time.Time {
	res := map[int]time.Time{}
	out, err := exec.Command("git", "-C", repoDir, "log", "--merges", "--format=%H %cI %s", "main").Output()
	if err != nil {
		return res
	}
	for _, l := range strings.Split(string(out), "\n") {
		p := strings.SplitN(l, " ", 3)
		if len(p) != 3 {
			continue
		}
		m := mergeGoal.FindStringSubmatch(p[2])
		if m == nil {
			continue
		}
		g, _ := strconv.Atoi(m[1])
		t, err := time.Parse(time.RFC3339, p[1])
		if err != nil || !isReduction(g) {
			continue
		}
		if _, ok := res[g]; !ok {
			res[g] = t
		}
	}
	return res
}

func isReduction(g int) bool {
	for _, x := range ReductionGoals {
		if x == g {
			return true
		}
	}
	return false
}

// Part is a count of follow-up turns with their input and cost.
type Part struct {
	N      int     `json:"n"`
	Input  int64   `json:"input"`
	Weight float64 `json:"weight"`
	Share  float64 `json:"share"`
}

func (p *Part) acc(n int, u Usage) {
	p.N += n
	p.Input += u.In()
	p.Weight += u.W()
}

type StopRow struct {
	Blocks int  `json:"blocks"`
	Idle   Part `json:"idle"`
	Work   Part `json:"work"`
}

type RoleStat struct {
	Sessions int `json:"sessions"`
	Turns    int `json:"turns"`
	Usage
	Weight              float64 `json:"weight"`
	WeightShare         float64 `json:"weight_share"`
	AvgCacheReadPerTurn float64 `json:"avg_cache_read_per_turn"`
	Compactions         int     `json:"compactions"`
	Rejects             int     `json:"rejects"`
	RejectReceives      int     `json:"reject_receives"`
	StopBlocks          int     `json:"stop_blocks"`
	StopExtraTurns      int     `json:"stop_extra_turns"`
	StopExtraInput      int64   `json:"stop_extra_input"`
}

type EstimateCheck struct {
	Pairs          int     `json:"pairs"`
	ActualTotal    float64 `json:"actual_total"`
	EstimatedTotal float64 `json:"estimated_total"`
	TotalRatio     float64 `json:"total_ratio"`
	MedianRatio    float64 `json:"median_ratio"`
	Within10x      bool    `json:"within_10x"`
}

type SessionRow struct {
	Role    string `json:"role"`
	Goal    *int   `json:"goal"`
	Session string `json:"session"`
	Turns   int    `json:"turns"`
	Usage
	Weight int64 `json:"weight"`
}

// Report is the aggregate; its json keys are the tool's output contract.
type Report struct {
	Roles          map[string]*RoleStat           `json:"roles"`
	FactorByRole   map[string]map[string]float64  `json:"factor_by_role"`
	FactorGroups   map[string]map[string]float64  `json:"factor_groups"`
	TopFactors     [][]any                        `json:"top_factors"`
	BashHeads      [][]any                        `json:"bash_heads"`
	TotalWeight    float64                        `json:"total_weight"`
	StopByKind     map[string]map[string]*StopRow `json:"stop_by_kind"`
	NotifTurns     map[string]map[string]*Part    `json:"notif_turns_by_event"`
	EstimateCheck  *EstimateCheck                 `json:"estimate_check"`
	BeforeAfter    []map[string]any               `json:"before_after"`
	Sessions       []SessionRow                   `json:"sessions"`
	UnknownSession int                            `json:"unknown_sessions"`
}

func round(x float64) int64 { return int64(math.RoundToEven(x)) }

func median(x []float64) float64 {
	sort.Float64s(x)
	n := len(x)
	if n%2 == 1 {
		return x[n/2]
	}
	return (x[n/2-1] + x[n/2]) / 2
}

type kv struct {
	k string
	v float64
}

// top is Counter.most_common(10): descending by value, ties by key.
func top(m map[string]float64) []kv {
	var out []kv
	for k, v := range m {
		out = append(out, kv{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].v != out[j].v {
			return out[i].v > out[j].v
		}
		return out[i].k < out[j].k
	})
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

// Build aggregates sessions; all is the unfiltered set the before/after
// comparison needs.
func Build(sessions, all []*Session, merges map[int]time.Time) *Report {
	totW := 0.0
	for _, s := range sessions {
		totW += s.Usage.W()
	}
	if totW == 0 {
		totW = 1
	}
	rep := &Report{Roles: map[string]*RoleStat{}, FactorByRole: map[string]map[string]float64{},
		FactorGroups: map[string]map[string]float64{}, TotalWeight: totW,
		StopByKind: map[string]map[string]*StopRow{}, NotifTurns: map[string]map[string]*Part{},
		TopFactors: [][]any{}, BashHeads: [][]any{}, Sessions: []SessionRow{}}
	allF, bash := map[string]float64{}, map[string]float64{}
	for _, r := range Roles {
		rs := &RoleStat{}
		rep.Roles[r] = rs
		fac := map[string]float64{}
		rep.FactorByRole[r] = fac
		rep.StopByKind[r] = map[string]*StopRow{}
		rep.NotifTurns[r] = map[string]*Part{}
		for _, s := range sessions {
			if s.Role != r {
				continue
			}
			rs.Sessions++
			rs.Turns += s.Turns
			rs.Usage.add(s.Usage)
			rs.Compactions += s.Compactions
			rs.Rejects += s.Rejects
			rs.RejectReceives += s.RejectReceives
			rs.StopBlocks += s.StopBlocks
			rs.StopExtraTurns += s.StopExtraTurns
			rs.StopExtraInput += s.StopExtraInput
			for k, v := range s.F {
				fac[k] += v
				allF[k] += v
				g := k[:strings.Index(k+":", ":")]
				if rep.FactorGroups[g] == nil {
					rep.FactorGroups[g] = map[string]float64{}
					for _, r := range Roles {
						rep.FactorGroups[g][r] = 0
					}
				}
				rep.FactorGroups[g][r] += v
			}
			for k, v := range s.Bash {
				bash[k] += v
			}
			for k, b := range s.Stop {
				o := rep.StopByKind[r][k]
				if o == nil {
					o = &StopRow{}
					rep.StopByKind[r][k] = o
				}
				o.Blocks += b.N
				if b.Idle != nil {
					o.Idle.acc(b.Idle.N, b.Idle.Usage)
				}
				if b.Work != nil {
					o.Work.acc(b.Work.N, b.Work.Usage)
				}
			}
			for k, b := range s.Notif {
				o := rep.NotifTurns[r][k]
				if o == nil {
					o = &Part{}
					rep.NotifTurns[r][k] = o
				}
				o.acc(b.N, b.Usage)
			}
		}
		rs.Weight = rs.Usage.W()
		rs.WeightShare = rs.Weight / totW
		if rs.Turns > 0 {
			rs.AvgCacheReadPerTurn = float64(rs.CacheRead) / float64(rs.Turns)
		}
		for _, o := range rep.StopByKind[r] {
			o.Idle.Share, o.Work.Share = o.Idle.Weight/totW, o.Work.Weight/totW
		}
		for _, o := range rep.NotifTurns[r] {
			o.Share = o.Weight / totW
		}
	}
	ftot := 0.0
	for _, v := range allF {
		ftot += v
	}
	if ftot == 0 {
		ftot = 1
	}
	for _, e := range top(allF) {
		rep.TopFactors = append(rep.TopFactors, []any{e.k, round(e.v), e.v / ftot})
	}
	for _, e := range top(bash) {
		rep.BashHeads = append(rep.BashHeads, []any{e.k, round(e.v)})
	}
	var ratios []float64
	var ta, te float64
	for _, s := range sessions {
		for _, p := range s.Pairs {
			if p.Estimated > 0 && p.Actual > 0 {
				ratios = append(ratios, p.Actual/p.Estimated)
				ta += p.Actual
				te += p.Estimated
			}
		}
		rep.Sessions = append(rep.Sessions, SessionRow{Role: s.Role, Goal: s.Goal, Session: s.Session,
			Turns: s.Turns, Usage: s.Usage, Weight: round(s.Usage.W())})
		if s.Role == "unknown" {
			rep.UnknownSession++
		}
	}
	if len(ratios) > 0 {
		rep.EstimateCheck = &EstimateCheck{Pairs: len(ratios), ActualTotal: ta, EstimatedTotal: float64(round(te)),
			TotalRatio: ta / te, MedianRatio: median(ratios), Within10x: ta/te >= 0.1 && ta/te <= 10}
	}
	rep.BeforeAfter = beforeAfter(all, merges)
	return rep
}

func beforeAfter(sessions []*Session, merges map[int]time.Time) []map[string]any {
	var rows []map[string]any
	for _, g := range ReductionGoals {
		m, ok := merges[g]
		if !ok {
			rows = append(rows, map[string]any{"goal": g, "note": "merge コミットが見つからない: 比較不能"})
			continue
		}
		for _, role := range []string{"commander", "subcommander", "executor"} {
			var before, after []*Session
			for _, s := range sessions {
				if s.Role != role || s.Turns == 0 || s.FirstTS == "" {
					continue
				}
				t, err := time.Parse(time.RFC3339Nano, s.FirstTS)
				if err != nil {
					continue
				}
				if t.Before(m) {
					before = append(before, s)
				} else {
					after = append(after, s)
				}
			}
			r := map[string]any{"goal": g, "role": role, "merge": m.Format("2006-01-02T15:04:05-07:00"),
				"n_before": len(before), "n_after": len(after)}
			if len(before) < minN || len(after) < minN {
				r["note"] = "比較不能(標本不足)"
			} else {
				for k, x := range map[string][]*Session{"before": before, "after": after} {
					var in, turns int64
					for _, s := range x {
						in += s.Usage.In()
						turns += int64(s.Turns)
					}
					r[k+"_per_session"] = round(float64(in) / float64(len(x)))
					r[k+"_per_turn"] = round(float64(in) / float64(turns))
				}
			}
			rows = append(rows, r)
		}
	}
	return rows
}

func f0(n float64) string {
	s := strconv.FormatFloat(math.Abs(n), 'f', 0, 64)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if n < 0 {
		s = "-" + s
	}
	return s
}

func pct(x float64, d int) string { return fmt.Sprintf("%.*f%%", d, x*100) }

func sortedKeys[V any](m map[string]V, weight func(V) float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if a, b := weight(m[keys[i]]), weight(m[keys[j]]); a != b {
			return a > b
		}
		return keys[i] < keys[j]
	})
	return keys
}

// Show prints the human-readable tables.
func Show(w io.Writer, rep *Report) {
	p := func(format string, a ...any) { fmt.Fprintf(w, format+"\n", a...) }
	p("## 役割別 usage")
	p("role | sess | turns | input | cache_create | cache_read | output | weight | share | avg cache_read/turn | compact | reject/recv | Stop block/extra turns")
	for _, r := range Roles {
		x := rep.Roles[r]
		p("%s | %d | %d | %s | %s | %s | %s | %s | %s | %s | %d | %d/%d | %d/%d", r, x.Sessions, x.Turns, f0(float64(x.Input)),
			f0(float64(x.CacheCreation)), f0(float64(x.CacheRead)), f0(float64(x.Output)), f0(x.Weight), pct(x.WeightShare, 1),
			f0(x.AvgCacheReadPerTurn), x.Compactions, x.Rejects, x.RejectReceives, x.StopBlocks, x.StopExtraTurns)
	}
	p("\n## 役割 x 要因グループ (概算 tokens)")
	p("group | %s", strings.Join(Roles, " | "))
	for _, g := range sortedKeys(rep.FactorGroups, func(m map[string]float64) (t float64) {
		for _, v := range m {
			t += v
		}
		return
	}) {
		cols := make([]string, len(Roles))
		for i, r := range Roles {
			cols[i] = f0(rep.FactorGroups[g][r])
		}
		p("%s | %s", g, strings.Join(cols, " | "))
	}
	p("\n## 上位10要因")
	for _, t := range rep.TopFactors {
		p("%s: %s (%s)", t[0], f0(float64(t[1].(int64))), pct(t[2].(float64), 1))
	}
	p("\n## Bash 先頭語 上位")
	heads := make([]string, len(rep.BashHeads))
	for i, t := range rep.BashHeads {
		heads[i] = fmt.Sprintf("%s=%s", t[0], f0(float64(t[1].(int64))))
	}
	p("%s", strings.Join(heads, ", "))
	p("\n## Stop hook ブロックの内訳 (idle=tool_use 無しのターン / work=tool_use 有り。input=cache_read+cache_create+input, 重み%%=全体の重みに対する割合)")
	p("role | reason 種別 | blocks | idle turns | idle input | idle 重み%% | work turns | work input | work 重み%%")
	for _, r := range Roles {
		n, wt := 0, 0.0
		for _, o := range rep.StopByKind[r] {
			n += o.Idle.N + o.Work.N
			wt += o.Idle.Weight + o.Work.Weight
		}
		p("%s | 合計 | | | | | 追加ターン %d | | 重み %s", r, n, pct(wt/rep.TotalWeight, 2))
		for _, k := range sortedKeys(rep.StopByKind[r], func(o *StopRow) float64 { return o.Idle.Weight + o.Work.Weight }) {
			o := rep.StopByKind[r][k]
			p("%s | %s | %d | %d | %s | %s | %d | %s | %s", r, k, o.Blocks, o.Idle.N, f0(float64(o.Idle.Input)), pct(o.Idle.Share, 2),
				o.Work.N, f0(float64(o.Work.Input)), pct(o.Work.Share, 2))
		}
	}
	p("\n## 通知が起こしたターン (idle の後、task-notification で始まったターン)")
	p("role | event 種別 | turns | input | 重み%% (assistant ターン数は input に含む)")
	for _, r := range Roles {
		n, wt := 0, 0.0
		for _, o := range rep.NotifTurns[r] {
			n += o.N
			wt += o.Weight
		}
		p("%s | 合計 | %d | | %s", r, n, pct(wt/rep.TotalWeight, 2))
		for _, k := range sortedKeys(rep.NotifTurns[r], func(o *Part) float64 { return o.Weight }) {
			o := rep.NotifTurns[r][k]
			p("%s | %s | %d | %s | %s", r, k, o.N, f0(float64(o.Input)), pct(o.Share, 2))
		}
	}
	p("\n## 概算の確認")
	if c := rep.EstimateCheck; c == nil {
		p("標本なし")
	} else {
		verdict := "桁で外れている"
		if c.Within10x {
			verdict = "桁は外れていない"
		}
		p("actual(cache_creation+input) %s vs 本文概算 %s (比 %.2f, 中央値 %.2f, %d 組) -> %s",
			f0(c.ActualTotal), f0(c.EstimatedTotal), c.TotalRatio, c.MedianRatio, c.Pairs, verdict)
	}
	p("\n## 削減 goal 前後 (input = cache_read+cache_creation+input)")
	for _, r := range rep.BeforeAfter {
		switch {
		case r["role"] == nil:
			p("goal %d: %s", r["goal"], r["note"])
		case r["note"] != nil:
			p("goal %d %s: %s (前 %d / 後 %d)", r["goal"], r["role"], r["note"], r["n_before"], r["n_after"])
		default:
			p("goal %d %s: 1 session %s -> %s, 1 turn %s -> %s (前 %d / 後 %d)", r["goal"], r["role"],
				f0(float64(r["before_per_session"].(int64))), f0(float64(r["after_per_session"].(int64))),
				f0(float64(r["before_per_turn"].(int64))), f0(float64(r["after_per_turn"].(int64))), r["n_before"], r["n_after"])
		}
	}
	p("\nunknown セッション: %d", rep.UnknownSession)
}
