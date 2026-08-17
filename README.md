# ATCT

**The air traffic control tower for your coding agents.**

A controller tracks many aircraft at once without flying any of them. When an aircraft needs a
decision, the controller issues a *clearance*; until it arrives, the aircraft *holds short* — it
stops before the runway and waits, without anyone taking the controls.

ATCT is that tower for coding agents. You set a goal. Agents break it into tasks and work through
them. When an agent reaches something it should not decide alone, it parks the question for you and
keeps going where it can. You answer from one inbox. The agent picks the answer up and continues.
When the goal is met, it comes back to you for sign-off.

---

## Install

```bash
claude plugin marketplace add michiomochi/atct
claude plugin install atct@atct
```

That is the whole install. **No Homebrew, no Go toolchain, no `PATH` to edit.** The plugin
ships wrappers that fetch the two binaries the first time something needs them, verify them
against the release checksums, and cache them under `~/.atct/bin/`.

Restart Claude Code once — or run `/reload-plugins` — so the new MCP server is picked up.

macOS and Linux, on amd64 and arm64. Windows is not supported: the daemon talks over a Unix
domain socket.

<details>
<summary>Installing the binaries yourself</summary>

The wrappers exist so you don't have to. If you would rather manage the binaries — to pin a
version, to install offline, or to use `atct` from a shell outside any plugin:

```bash
brew install --cask michiomochi/tap/atct
# or, with a Go toolchain:
#   go install github.com/michiomochi/atct/cmd/atct@latest
#   go install github.com/michiomochi/atct/cmd/atct-mcp@latest
```

Note that the wrappers do not consult `PATH`. They always run the binary matching the
plugin's own version, so an agent never talks to a daemon built from a different release than
the tool definitions it was given. Installing the binaries yourself gives you `atct` in any
shell; it does not change what the plugin runs.

</details>

## Getting started

```bash
cd /path/to/your/repo
atct project add                 # put this repository under ATCT
atct goal add "Ship the thing"   # or create it from the web inbox
```

Open <http://127.0.0.1:8787/> for the inbox. The daemon starts on its own the first time
anything needs it.

From then on, opening Claude Code in a registered repository is enough: a session hook tells
the agent that this repository is managed through ATCT, and the eight MCP tools are already
connected. **Repositories you never registered are untouched** — the hook stays silent, so
unrelated work does not pay for a ceremony it did not ask for.

Two commands cover day-to-day use:

| Command | What it does |
|---|---|
| `atct project add` | Register a repository. Run once per repository |
| `atct goal add "…"` | Create a goal for the current repository |

`atct project list`, `atct goal list`, `atct ensure`, and `atct stop` round out the CLI. The
web inbox handles everything a human answers.

---

## The problem

Running several coding agents at once is now normal. Keeping track of them is not.

What you need to know is narrow: **which tasks are in flight, which ones are waiting on a decision
from me, and what is queued next.** What you get instead is a row of terminal panes, each holding a
conversation you would have to read to find out.

The gap is more fundamental than a missing dashboard:

- **Agent runtimes do not expose task state.** Claude Code's todo list has exactly three statuses —
  `pending`, `in_progress`, `completed`. None of them means "waiting on a human". Session
  transcripts carry no stable field for the current task, why a run stopped, or what it intends to
  do next. Codex is the same. Task state cannot be recovered by watching agents, however closely you
  watch them.
- **Approval workflows only handle decisions someone anticipated.** Jira Service Management, Asana
  approvals, and every BPM engine model approval steps configured by an administrator ahead of time.
  An agent that runs into a real design question mid-implementation cannot mint a new one with its
  own options.
- **Nothing delivers an answer back into a running session.** Tools in this space route around the
  problem rather than solve it — the closest one spawns a fresh one-shot process and files its
  output as a reply.

So decisions end up where nobody can see them: inside one agent's conversation, in one pane, on a
screen you happen not to be looking at. Work stalls and nothing surfaces it.

## What ATCT does

**Decisions are first-class objects, not messages in a transcript.** An agent creates one during a
run, with its own question and its own options, and hands it to a store that outlives the session
that created it.

**Waiting is derived, never stored.** A task is waiting on you precisely when it has an open
decision attached. There is no separate status to fall out of sync, so the board cannot claim a task
is blocked when nothing blocks it, or show one running while a question sits unanswered.

**An answer is not finished until it lands.** Decisions move through three states:

```
open ──(you answer)──> answered ──(the agent picks it up)──> applied
```

You having answered and the agent having received that answer are different facts. If a session dies
between them, the decision stays at `answered` and is visible as such, instead of looking handled
while the work quietly stops.

**One inbox.** Every unanswered decision across every project lands in the same list, including
"this goal looks complete, approve it?". Watching that one list is enough to never miss one.

**Agents keep working.** A parked decision blocks one task, not the goal. The agent moves to
whatever else is ready and comes back when you answer.

## How it fits together

```mermaid
flowchart LR
  CC[Claude Code] --> SHIM
  CX[Codex] --> SHIM
  CU[Cursor / any MCP client] --> SHIM
  SHIM[atct-mcp<br/>stdio shim] -->|unix socket| D[atct daemon]
  D --> DB[(SQLite)]
  D -->|HTTP + SSE| UI[Web UI<br/>inbox / goals]
  H((You)) --> UI
```

Agents reach ATCT over MCP, so anything that speaks MCP works — the Claude Code plugin is a
convenience, not the interface. For other harnesses, point them at `atct-mcp` and you have the same
eight tools. Everything runs on your machine: two binaries, a SQLite file, and a browser tab. No
account, no server, no data leaving the host.

ATCT does not start, stop, or supervise agents. It holds the tasks and the decisions. How you run
agents stays your business.

## Concepts

| | |
|---|---|
| **Project** | A project. Derived from the working directory, so agents never have to name it |
| **Goal** | What you want. You write it; agents do not invent goals |
| **Task** | A unit of work toward the goal. Agents declare and claim these |
| **Decision** | A question an agent cannot settle alone, with options it wrote itself |

Every option carries a `consequence` — what happens if you pick it. You should be able to decide
without going back to ask what the choice actually means.

Several agents can work one goal at the same time. Tasks are claimed atomically, so two agents never
hold the same one.

## The screens

- **Inbox** — every unanswered decision, across every project. The one screen that matters.
- **Goal detail** — three columns: what is running now, what needs a decision, what is queued next.
- **Decision** — the question, the options and their consequences, and a free-text field for
  answers that are not on the list.

## What ATCT is not

- Not a dependency graph — that is [Beads](https://github.com/gastownhall/beads)' job
- Not an agent multiplexer or supervisor — that is a terminal multiplexer's job
- Not token and cost observability — that is Langfuse and AgentOps' job
- Not a general blocker tracker; the only thing ATCT waits on is a human decision

ATCT holds tasks and the decisions attached to them. That is the whole product.

## Design

The design is written up in full:

- [`doc/specs/2026-08-15-atct-design.md`](doc/specs/2026-08-15-atct-design.md) — data model, state
  machine and invariants, MCP contract, topology
- [`doc/plans/2026-08-15-atct-core.md`](doc/plans/2026-08-15-atct-core.md) — the implementation,
  written test-first, task by task

Requires Go 1.26+. Node 22.12+ and pnpm are needed only for the web UI; prebuilt assets are
committed so a Go-only build works without them.

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md). Open an issue before writing anything larger than a bug
fix — design decisions follow the spec, and a change that contradicts it needs the spec updated
first.

Contributions require signing a CLA. It grants permission, it does not transfer copyright; the
reasoning is in CONTRIBUTING.md.

## License

[AGPL-3.0](LICENSE).

Running ATCT — modified or not, personally or across your company — carries no obligations. The
copyleft applies if you modify it and serve it to others over a network.

**Using ATCT does not put your code under AGPL.** It runs as its own process and agents reach it
over MCP, which is inter-process communication, not linking. Your agents, your applications, and
everything else on your machine keep whatever license they already had.
