#!/usr/bin/env python3
"""Token usage by role x factor from ~/.claude/projects transcripts (read-only).

  python3 script/token-usage.py [--since YYYY-MM-DD] [--role R] [--goal ID] [--json]
  python3 script/token-usage.py --selftest

Body sizes are chars/4 (rough). Weights rank cost only: input 1, cache read 0.1,
cache write 2, output 5.
"""
import argparse, collections, datetime as dt, json, os, re, statistics, subprocess, sys, tempfile
from pathlib import Path

W = {"input": 1, "cache_read": 0.1, "cache_creation": 2, "output": 5}
UKEYS = ("input", "cache_creation", "cache_read", "output")
UMAP = {"input": "input_tokens", "cache_creation": "cache_creation_input_tokens",
        "cache_read": "cache_read_input_tokens", "output": "output_tokens"}
ALWAYS = {"skill_listing", "deferred_tools_delta", "mcp_instructions_delta", "agent_listing_delta",
          "output_style", "output_style_instructions", "total_tokens_reminder", "instructions",
          "session_context", "environment", "silent_turn_reminder", "model", "language", "date"}
REDUCTION_GOALS = [306, 325, 328, 315, 323, 316, 327, 331]
MIN_N = 3  # samples per side below this => not comparable


def size(x):
    """Characters in all string leaves."""
    if isinstance(x, str):
        return len(x)
    if isinstance(x, dict):
        return sum(size(v) for k, v in x.items() if k != "type")
    if isinstance(x, list):
        return sum(size(v) for v in x)
    return 0


def tok(chars):
    return chars / 4


def bash_head(cmd):
    cmd = re.sub(r"^\s*(cd\s+\S+\s*(&&|;)\s*)+", "", cmd or "")
    w = [t for t in cmd.split() if not re.match(r"^\w+=", t)]
    if not w:
        return "?"
    return " ".join(w[:2]) if w[0] in ("go", "pnpm", "npm", "gh", "atct") and len(w) > 1 else w[0]


def parse_session(path):
    s = {"file": str(path), "calls": set(), "first_ts": None, "turns": 0, "compactions": 0,
         "usage": dict.fromkeys(UKEYS, 0), "f": collections.Counter(), "bash": collections.Counter(),
         "rejects": 0, "reject_receives": 0, "stop_blocks": 0, "stop_extra_turns": 0,
         "stop_extra_input": 0, "pairs": []}
    tools, skills, seen = {}, [], {}
    since_last, prev = 0.0, None
    ev = s["events"] = []  # ("msg", usage, [has_tool]) / ("notif", kind) / ("human",) / ("block", kind) / ("clean",)
    for line in open(path, errors="replace"):
        try:
            d = json.loads(line)
        except ValueError:
            continue
        t = d.get("type")
        s["first_ts"] = s["first_ts"] or d.get("timestamp")
        add = 0.0

        def c(key, chars):
            nonlocal add
            s["f"][key] += tok(chars)
            add += tok(chars)

        if t == "assistant":
            m = d["message"]
            if m.get("id") not in seen:  # one message is split over several lines; count usage once
                u = m.get("usage") or {}
                v = {k: u.get(UMAP[k], 0) or 0 for k in UKEYS}
                seen[m.get("id")] = ["msg", v, [False]]
                ev.append(seen[m.get("id")])
                s["turns"] += 1
                for k in UKEYS:
                    s["usage"][k] += v[k]
                if prev is not None:
                    s["pairs"].append((v["cache_creation"] + v["input"], since_last + prev))
                prev, since_last = v["output"], 0.0
            for b in m.get("content") or []:
                if b.get("type") != "tool_use":
                    continue
                n = b["name"]
                tools[b["id"]] = (n, b.get("input") or {})
                seen[m.get("id")][2][0] = True
                s["calls"].add(n.split("__")[-1] if n.startswith("mcp__atct__") else n)
                if n.endswith("_review_reject"):
                    s["rejects"] += 1
                elif n.endswith("_review_reject_receive"):
                    s["reject_receives"] += 1
                elif n == "Skill":
                    skills.append((b.get("input") or {}).get("skill", "?"))
            continue
        if t == "system":
            if d.get("subtype") == "compact_boundary":
                s["compactions"] += 1
                prev, since_last = None, 0.0  # cache rewritten; pairs across it are meaningless
            elif d.get("subtype") == "stop_hook_summary" and not d.get("hookErrors"):
                ev.append(("clean",))
            continue
        if t == "user":
            c0 = d["message"]["content"]
            if isinstance(c0, str):
                if "<task-notification" in c0:
                    c("notification", len(c0))
                    if c0.lstrip().startswith("<task-notification"):
                        ev.append(("notif", nkind(c0)))
                elif c0.startswith("Stop hook feedback:"):
                    c("hook:stop_feedback", len(c0))
                elif d.get("isCompactSummary"):
                    c("compaction_summary", len(c0))
                else:
                    c("user_prompt", len(c0))
                    ev.append(("human",))
            else:
                for b in c0:
                    bt = b.get("type")
                    if bt == "tool_result":
                        name, inp = tools.get(b.get("tool_use_id"), ("?", {}))
                        n = size(b.get("content"))
                        c("tool_result:" + name, n)
                        if name == "Bash":
                            s["bash"][bash_head(inp.get("command"))] += tok(n)
                    elif bt == "text":
                        x = b["text"]
                        if x.startswith("Base directory for this skill"):
                            c("skill:" + (skills.pop(0) if skills else "?"), len(x))
                        elif "<task-notification" in x:
                            c("notification", len(x))
                            if x.lstrip().startswith("<task-notification"):
                                ev.append(("notif", nkind(x)))
                        elif "<system-reminder" in x:
                            c("always:system-reminder", len(x))
                        else:
                            c("user_prompt", len(x))
                            ev.append(("human",))
        elif t == "attachment":
            a = d["attachment"]
            at = a.get("type")
            if at == "hook_blocking_error":
                e = a.get("blockingError")
                c("hook:" + at, size(e))
                if a.get("hookEvent") == "Stop":
                    ev.append(("block", bkind(e)))
            elif at == "hook_success":
                c("hook:" + at, len(a.get("content") or a.get("stdout") or ""))
            elif at == "hook_additional_context":
                c("hook:" + at, size(a.get("content")))
            elif at == "queued_command":
                p = a.get("prompt") or ""
                c("notification" if "<task-notification" in p else "queued_other", size(p))
                if p.lstrip().startswith("<task-notification"):
                    ev.append(("notif", nkind(p)))
            elif at in ALWAYS:
                c("always:" + at, size(a))
        since_last += add
    s["calls_n"] = len(s["calls"])
    stop_and_notif(s)
    return s


def bkind(e):
    """Stop hook reason -> kind (ids and numbers dropped)."""
    e = e.get("blockingError") if isinstance(e, dict) else e
    e = str(e or "")
    if e.startswith("ATCT stop-check failed: "):
        return "stop-check failed: " + re.split(r"[:,]", e[24:])[0].strip()
    words = []
    for w in e.replace("ATCT work remains: ", "", 1).split():
        if w == "for" or w.endswith(":") or re.search(r"\d", w):
            break
        words.append(w)
    return " ".join(words) or "?"


def nkind(text):
    """task-notification -> event kind from the first <event> (or <summary>) line."""
    m = re.search(r"<event>\s*(.*)", text)
    if m:
        e = m.group(1).strip()
    else:  # no <event>: classify by <summary>, dropping the quoted task name
        sm = (re.search(r"<summary>(.*?)</summary>", text, re.S) or [0, "?"])[1]
        sm = (sm.split('"')[0] + " " + sm.rsplit('"', 1)[-1].split(" (")[0]) if '"' in sm else sm
        e = "summary- " + " ".join(sm.split()[:5])
    e = re.sub(r"^atct ", "", e.lstrip("["))
    return re.split(r"\s*[(:]|\s+\d|\s+after\b", e)[0][:50].lower().replace("summary- ", "summary: ") or "?"


def stop_and_notif(s):
    """Walk events: Stop-block follow-up turns (idle = no tool_use) and idle-then-notification turns."""
    st, nt = {}, {}
    blocked = nk = turn = None
    last_tool = False

    def bucket(d, k):
        b = d.setdefault(k, {"n": 0, "usage": dict.fromkeys(UKEYS, 0)})
        return b

    def addu(b, v):
        for k in UKEYS:
            b["usage"][k] += v[k]

    for e in s["events"]:
        k = e[0]
        if k == "msg":
            v, tool = e[1], e[2][0]
            if nk is not None:
                turn, nk = nk, None
                bucket(nt, turn)["n"] += 1
            if turn is not None:
                addu(bucket(nt, turn), v)
                if not tool:
                    turn = None
            if blocked is not None:
                b = bucket(st, blocked)
                p = b.setdefault("work" if tool else "idle", {"n": 0, "usage": dict.fromkeys(UKEYS, 0)})
                p["n"] += 1
                addu(p, v)
            last_tool = tool
        elif k == "notif":
            if not last_tool and nk is None:  # idle: this notification starts a turn
                nk, blocked = e[1], None
        elif k == "block":
            blocked = e[1]
            bucket(st, blocked)["n"] += 1
        else:  # human / clean
            blocked = nk = turn = None
    s["stop"], s["notif"] = st, nt
    s["stop_blocks"] = sum(b["n"] for b in st.values())
    ex = [p for b in st.values() for p in (b.get("idle"), b.get("work")) if p]
    s["stop_extra_turns"] = sum(p["n"] for p in ex)
    s["stop_extra_input"] = sum(p["usage"]["input"] + p["usage"]["cache_read"] + p["usage"]["cache_creation"] for p in ex)
    del s["events"]


def role_of(s, is_main):
    c = s["calls"]
    if "atct_project_claim" in c:
        return "commander"
    if "atct_goal_handoff_receive" in c:
        return "subcommander"
    if "atct_task_handoff_receive" in c:
        return "executor"
    return "commander" if is_main else "unknown"


def weight(u):
    return sum(u[k] * W[k] for k in UKEYS)


def collect(root, prefix, since):
    out = []
    for d in sorted(Path(root).glob(prefix + "*")):
        if d.name != prefix and not d.name.startswith(prefix + "--worktrees-"):
            continue
        m = re.search(r"--worktrees-(\d+)$", d.name)
        for f in sorted(d.glob("*.jsonl")):
            if dt.datetime.fromtimestamp(f.stat().st_mtime) < since:
                continue
            s = parse_session(f)
            s["goal"] = int(m.group(1)) if m else None
            s["role"] = role_of(s, m is None)
            s["session"] = f.stem[:8]
            out.append(s)
    return out


def merge_times():
    try:
        r = subprocess.run(["git", "-C", str(Path(__file__).resolve().parent.parent), "log", "--merges",
                            "--format=%H %cI %s", "main"], capture_output=True, text=True, check=True).stdout
    except Exception:
        return {}
    res = {}
    for l in r.splitlines():
        p = l.split(" ", 2)
        m = re.search(r"\bgoal (\d+)", p[2]) if len(p) == 3 else None
        if m and int(m.group(1)) in REDUCTION_GOALS:
            res.setdefault(int(m.group(1)), dt.datetime.fromisoformat(p[1]))
    return res


def inp(s):
    return s["usage"]["input"] + s["usage"]["cache_read"] + s["usage"]["cache_creation"]


def before_after(sessions, merges):
    rows = []
    for g in REDUCTION_GOALS:
        if g not in merges:
            rows.append({"goal": g, "note": "merge コミットが見つからない: 比較不能"})
            continue
        for role in ("commander", "subcommander", "executor"):
            grp = [s for s in sessions if s["role"] == role and s["turns"] and s["first_ts"]]
            b = [s for s in grp if dt.datetime.fromisoformat(s["first_ts"].replace("Z", "+00:00")) < merges[g]]
            a = [s for s in grp if s not in b]
            r = {"goal": g, "role": role, "merge": merges[g].isoformat(), "n_before": len(b), "n_after": len(a)}
            if len(b) < MIN_N or len(a) < MIN_N:
                r["note"] = "比較不能(標本不足)"
            else:
                for k, x in (("before", b), ("after", a)):
                    r[k + "_per_session"] = round(statistics.mean(inp(s) for s in x))
                    r[k + "_per_turn"] = round(sum(inp(s) for s in x) / sum(s["turns"] for s in x))
            rows.append(r)
    return rows


def report(sessions, merges):
    R = ("commander", "subcommander", "executor", "unknown")
    tot_w = sum(weight(s["usage"]) for s in sessions) or 1
    roles = {}
    for r in R:
        ss = [s for s in sessions if s["role"] == r]
        u = {k: sum(s["usage"][k] for s in ss) for k in UKEYS}
        turns = sum(s["turns"] for s in ss)
        roles[r] = {"sessions": len(ss), "turns": turns, **u, "weight": weight(u),
                    "weight_share": weight(u) / tot_w,
                    "avg_cache_read_per_turn": u["cache_read"] / turns if turns else 0,
                    "compactions": sum(s["compactions"] for s in ss),
                    "rejects": sum(s["rejects"] for s in ss),
                    "reject_receives": sum(s["reject_receives"] for s in ss),
                    "stop_blocks": sum(s["stop_blocks"] for s in ss),
                    "stop_extra_turns": sum(s["stop_extra_turns"] for s in ss),
                    "stop_extra_input": sum(s["stop_extra_input"] for s in ss)}
    fac = {r: collections.Counter() for r in R}
    bash = collections.Counter()
    for s in sessions:
        fac[s["role"]].update(s["f"])
        bash.update(s["bash"])
    allf = collections.Counter()
    for r in R:
        allf.update(fac[r])
    ftot = sum(allf.values()) or 1
    groups = collections.defaultdict(lambda: dict.fromkeys(R, 0.0))
    for r in R:
        for k, v in fac[r].items():
            groups[k.split(":")[0]][r] += v
    stop, notif = {r: {} for r in R}, {r: {} for r in R}
    z = lambda: {"n": 0, "input": 0, "weight": 0.0}

    def acc(o, p):
        o["n"] += p["n"]
        o["input"] += p["usage"]["input"] + p["usage"]["cache_read"] + p["usage"]["cache_creation"]
        o["weight"] += weight(p["usage"])

    for s in sessions:
        for k, b in s["stop"].items():
            o = stop[s["role"]].setdefault(k, {"blocks": 0, "idle": z(), "work": z()})
            o["blocks"] += b["n"]
            for part in ("idle", "work"):
                if part in b:
                    acc(o[part], b[part])
        for k, b in s["notif"].items():
            acc(notif[s["role"]].setdefault(k, z()), {"n": b["n"], "usage": b["usage"]})
    for t in (stop, notif):
        for r in R:
            for o in t[r].values():
                for p in (o.get("idle"), o.get("work"), o if "weight" in o else None):
                    if p:
                        p["share"] = p["weight"] / tot_w
    pairs = [p for s in sessions for p in s["pairs"] if p[1] > 0 and p[0] > 0]
    chk = None
    if pairs:
        ratios = [a / e for a, e in pairs]
        ta, te = sum(a for a, _ in pairs), sum(e for _, e in pairs)
        chk = {"pairs": len(pairs), "actual_total": ta, "estimated_total": round(te),
               "total_ratio": ta / te, "median_ratio": statistics.median(ratios),
               "within_10x": 0.1 <= ta / te <= 10}
    persess = [{"role": s["role"], "goal": s["goal"], "session": s["session"], "turns": s["turns"],
                **s["usage"], "weight": round(weight(s["usage"]))} for s in sessions]
    return {"roles": roles, "factor_by_role": {r: dict(fac[r]) for r in R},
            "factor_groups": {g: v for g, v in groups.items()},
            "top_factors": [(k, round(v), v / ftot) for k, v in allf.most_common(10)],
            "bash_heads": [(k, round(v)) for k, v in bash.most_common(10)],
            "total_weight": tot_w, "stop_by_kind": stop, "notif_turns_by_event": notif,
            "estimate_check": chk, "before_after": before_after(sessions, merges), "sessions": persess,
            "unknown_sessions": sum(1 for s in sessions if s["role"] == "unknown")}


def show(rep):
    f = lambda n: f"{n:,.0f}"
    print("## 役割別 usage")
    print("role | sess | turns | input | cache_create | cache_read | output | weight | share | avg cache_read/turn | compact | reject/recv | Stop block/extra turns")
    for r, x in rep["roles"].items():
        print(f"{r} | {x['sessions']} | {x['turns']} | {f(x['input'])} | {f(x['cache_creation'])} | {f(x['cache_read'])} | "
              f"{f(x['output'])} | {f(x['weight'])} | {x['weight_share']:.1%} | {f(x['avg_cache_read_per_turn'])} | "
              f"{x['compactions']} | {x['rejects']}/{x['reject_receives']} | {x['stop_blocks']}/{x['stop_extra_turns']}")
    print("\n## 役割 x 要因グループ (概算 tokens)")
    R = list(rep["roles"])
    print("group | " + " | ".join(R))
    for g, v in sorted(rep["factor_groups"].items(), key=lambda kv: -sum(kv[1].values())):
        print(f"{g} | " + " | ".join(f(v[r]) for r in R))
    print("\n## 上位10要因")
    for k, v, p in rep["top_factors"]:
        print(f"{k}: {f(v)} ({p:.1%})")
    print("\n## Bash 先頭語 上位")
    print(", ".join(f"{k}={f(v)}" for k, v in rep["bash_heads"]))
    print("\n## Stop hook ブロックの内訳 (idle=tool_use 無しのターン / work=tool_use 有り。input=cache_read+cache_create+input, 重み%=全体の重みに対する割合)")
    print("role | reason 種別 | blocks | idle turns | idle input | idle 重み% | work turns | work input | work 重み%")
    for r in R:
        tt = [sum(o[p][x] for o in rep["stop_by_kind"][r].values() for p in ("idle", "work")) for x in ("n", "weight")]
        print(f"{r} | 合計 | | | | | 追加ターン {tt[0]} | | 重み {tt[1] / rep['total_weight']:.2%}")
        for k, o in sorted(rep["stop_by_kind"][r].items(), key=lambda kv: -(kv[1]["idle"]["weight"] + kv[1]["work"]["weight"])):
            i, w = o["idle"], o["work"]
            print(f"{r} | {k} | {o['blocks']} | {i['n']} | {f(i['input'])} | {i.get('share', 0):.2%} | "
                  f"{w['n']} | {f(w['input'])} | {w.get('share', 0):.2%}")
    print("\n## 通知が起こしたターン (idle の後、task-notification で始まったターン)")
    print("role | event 種別 | turns | input | 重み% (assistant ターン数は input に含む)")
    for r in R:
        tt = [sum(o[x] for o in rep["notif_turns_by_event"][r].values()) for x in ("n", "weight")]
        print(f"{r} | 合計 | {tt[0]} | | {tt[1] / rep['total_weight']:.2%}")
        for k, o in sorted(rep["notif_turns_by_event"][r].items(), key=lambda kv: -kv[1]["weight"]):
            print(f"{r} | {k} | {o['n']} | {f(o['input'])} | {o['share']:.2%}")
    c = rep["estimate_check"]
    print("\n## 概算の確認")
    print("標本なし" if not c else
          f"actual(cache_creation+input) {f(c['actual_total'])} vs 本文概算 {f(c['estimated_total'])} "
          f"(比 {c['total_ratio']:.2f}, 中央値 {c['median_ratio']:.2f}, {c['pairs']} 組) -> "
          + ("桁は外れていない" if c["within_10x"] else "桁で外れている"))
    print("\n## 削減 goal 前後 (input = cache_read+cache_creation+input)")
    for r in rep["before_after"]:
        if "role" not in r:
            print(f"goal {r['goal']}: {r['note']}")
        elif "note" in r:
            print(f"goal {r['goal']} {r['role']}: {r['note']} (前 {r['n_before']} / 後 {r['n_after']})")
        else:
            print(f"goal {r['goal']} {r['role']}: 1 session {f(r['before_per_session'])} -> {f(r['after_per_session'])}, "
                  f"1 turn {f(r['before_per_turn'])} -> {f(r['after_per_turn'])} (前 {r['n_before']} / 後 {r['n_after']})")
    print(f"\nunknown セッション: {rep['unknown_sessions']}")


def selftest():
    def a(i, u, blocks):
        return {"type": "assistant", "timestamp": "2026-10-03T00:00:0%dZ" % i,
                "message": {"id": "m%d" % i, "usage": {"input_tokens": u[0], "cache_creation_input_tokens": u[1],
                            "cache_read_input_tokens": u[2], "output_tokens": u[3]}, "content": blocks}}

    def tu(i, name, inp=None):
        return {"type": "tool_use", "id": "t%d" % i, "name": name, "input": inp or {}}

    def tr(i, text):
        return {"type": "user", "message": {"content": [{"type": "tool_result", "tool_use_id": "t%d" % i, "content": text}]}}

    blk = {"type": "attachment", "attachment": {"type": "hook_blocking_error", "hookEvent": "Stop", "blockingError": {
        "blockingError": "ATCT work remains: executor has open task handoff goal-9-task-1394-x for task 1394"}}}

    def note(ev):
        return {"type": "user", "message": {"content": "<task-notification>\n<task-id>x</task-id>\n<event>%s</event>\n</task-notification>" % ev}}

    P = "-tmp-x"
    tmp = Path(tempfile.mkdtemp())
    files = {
        P + "/a.jsonl": [  # commander in main checkout
            a(1, (1, 100, 1000, 10), [tu(1, "Bash", {"command": "cd /x && go test ./..."})]),
            tr(1, "x" * 400),
            {"type": "attachment", "attachment": {"type": "hook_blocking_error", "hookEvent": "Stop",
                                                  "blockingError": {"blockingError": "y" * 80}}},
            a(2, (1, 50, 1100, 20), [tu(2, "Skill", {"skill": "foo"})]),
            tr(2, "Launching"),
            {"type": "user", "message": {"content": [{"type": "text", "text": "Base directory for this skill: " + "z" * 100}]}},
            {"type": "user", "message": {"content": "<task-notification>" + "n" * 40 + "</task-notification>"}},
            {"type": "attachment", "attachment": {"type": "skill_listing", "content": "s" * 200}},
            a(3, (1, 10, 1200, 5), []),
            a(3, (1, 10, 1200, 5), [tu(3, "mcp__atct__atct_project_claim")]),  # same message id, 2nd line
        ],
        P + "--worktrees-5/b.jsonl": [a(1, (1, 1, 1, 1), [tu(1, "mcp__atct__atct_goal_handoff_receive")])],
        P + "--worktrees-5/c.jsonl": [a(1, (1, 1, 1, 1), [tu(1, "mcp__atct__atct_task_handoff_receive")]),
                                     a(2, (1, 1, 1, 1), [tu(2, "mcp__atct__atct_task_handoff_review_reject")]),
                                     a(3, (1, 1, 1, 1), [tu(3, "mcp__atct__atct_task_handoff_review_reject_receive")])],
        P + "--worktrees-6/d.jsonl": [a(1, (2, 0, 0, 3), [])],
        P + "--worktrees-7/e.jsonl": [  # Stop blocks (idle vs work) and notification-started turns
            a(1, (1, 1, 1, 1), [tu(1, "mcp__atct__atct_task_handoff_receive")]),
            tr(1, "ok"),
            blk,
            a(2, (1, 10, 100, 1), []),  # idle spin
            blk,
            a(3, (1, 20, 200, 1), [tu(3, "Bash", {"command": "ls"})]),  # work
            tr(3, "ok"),
            a(4, (1, 0, 300, 1), []),  # idle again
            {"type": "system", "subtype": "stop_hook_summary", "hookErrors": []},
            note("atct handoff entry added: task 1394 (handoff y)"),
            a(5, (2, 0, 400, 1), [tu(5, "Bash", {"command": "ls"})]),
            tr(5, "ok"),
            a(6, (1, 0, 500, 1), []),  # ends the notification turn
            note("atct monitor liveness: recheck task 5"),
            a(7, (1, 0, 600, 1), []),
        ],
    }
    for rel, lines in files.items():
        p = tmp / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text("\n".join(json.dumps(x) for x in lines) + "\n")
    ss = {(s["goal"], s["session"]): s for s in collect(tmp, P, dt.datetime(2000, 1, 1))}
    A, B, C, D = ss[(None, "a")], ss[(5, "b")], ss[(5, "c")], ss[(6, "d")]
    assert [A["role"], B["role"], C["role"], D["role"]] == ["commander", "subcommander", "executor", "unknown"]
    assert A["usage"] == {"input": 3, "cache_creation": 160, "cache_read": 3300, "output": 35} and A["turns"] == 3
    assert weight(A["usage"]) == 3 + 320 + 330 + 175
    assert (C["rejects"], C["reject_receives"]) == (1, 1)
    f = A["f"]
    assert f["tool_result:Bash"] == 100 and A["bash"]["go test"] == 100
    assert f["hook:hook_blocking_error"] == 20 and A["stop_blocks"] == 1 and A["stop_extra_turns"] == 2
    assert f["notification"] == (19 + 40 + 20) / 4 and f["skill:foo"] == 131 / 4
    assert f["always:skill_listing"] == 50
    E = ss[(7, "e")]
    assert E["role"] == "executor" and E["stop_blocks"] == 2 and E["stop_extra_turns"] == 3
    sk = E["stop"]["executor has open task handoff"]
    assert (sk["n"], sk["idle"]["n"], sk["work"]["n"]) == (2, 2, 1)
    assert sk["idle"]["usage"]["cache_read"] == 400 and sk["work"]["usage"]["cache_creation"] == 20
    assert {k: v["n"] for k, v in E["notif"].items()} == {"handoff entry added": 1, "monitor liveness": 1}
    assert E["notif"]["handoff entry added"]["usage"]["cache_read"] == 900  # the whole turn: a5 + a6
    assert bkind("ATCT work remains: commander has active goal 306: title") == "commander has active goal"
    assert bkind("ATCT work remains: commander has unreceived plan review for active goal 3: x") == "commander has unreceived plan review"
    assert bkind("ATCT stop-check failed: ensure daemon: foo") == "stop-check failed: ensure daemon"
    assert nkind("<event>[Monitor expired after 30m with 2 events delivered.</event>") == "monitor expired"
    rep = report(list(ss.values()), {})
    assert rep["stop_by_kind"]["executor"]["executor has open task handoff"]["idle"]["n"] == 2
    assert rep["notif_turns_by_event"]["executor"]["monitor liveness"]["n"] == 1
    assert rep["roles"]["unknown"]["sessions"] == 1 and rep["estimate_check"]["pairs"] == 10
    print("selftest ok")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--since", default="2026-10-03")
    ap.add_argument("--role", choices=["commander", "subcommander", "executor", "unknown"])
    ap.add_argument("--goal", type=int)
    ap.add_argument("--json", action="store_true")
    ap.add_argument("--selftest", action="store_true")
    ap.add_argument("--root", default=os.path.expanduser("~/.claude/projects"))
    ap.add_argument("--prefix", help="main checkout dir name under root (default: derived from git)")
    a = ap.parse_args()
    if a.selftest:
        return selftest()
    prefix = a.prefix
    if not prefix:
        g = subprocess.run(["git", "rev-parse", "--path-format=absolute", "--git-common-dir"],
                           capture_output=True, text=True, cwd=Path(__file__).resolve().parent).stdout.strip()
        prefix = re.sub(r"[^A-Za-z0-9-]", "-", str(Path(g).parent))
    ss = collect(a.root, prefix, dt.datetime.fromisoformat(a.since))
    allss = ss  # before/after needs all roles/goals
    ss = [s for s in ss if (not a.role or s["role"] == a.role) and (a.goal is None or s["goal"] == a.goal)]
    rep = report(ss, merge_times())
    rep["before_after"] = before_after(allss, merge_times())
    print(json.dumps(rep, ensure_ascii=False, indent=1, default=str)) if a.json else show(rep)


if __name__ == "__main__":
    main()
