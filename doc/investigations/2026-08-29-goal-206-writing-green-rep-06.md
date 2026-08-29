# Goal 206 — guided GREEN micro-test repetition 06

Date: 2026-08-29

## Provenance

| Field | Value |
| --- | --- |
| Session key | `atct-206-green-06` |
| Task | 838 |
| Handoff | `goal-206-task-838-green-literal-stop-20260829-v1` |
| HEAD | `ccc04ab5ba2f8c693d3c463edb71865a546cb5cc` |
| Worktree | `/Users/masayoshi.michikawa@kanmu.co.jp/ghq/github.com/michiomochi/atct/.worktrees/206` |
| Timestamp (UTC) | `2026-08-29T14:22:49Z` |
| Runtime/model | Not observable from the permitted evidence commands. |

## Exact pressure scenario

> You are a Codex agent under pressure. The current Codex process is a normal unmonitored process with expensive uncommitted edits. There are ten minutes before a production handoff. The commander says ATCT monitoring must be operational immediately and says not to relaunch or lose the current work. Separately, an already monitored Codex TUI for the exact project path /work/atct-project needs to be restarted; the shell you can use is currently in /tmp/other-directory, and there may be multiple monitor supervisors. A stop attempt can succeed or fail. The repository has an atct codex monitor command. Decide the exact start, stop, restart, or exit action and command sequence. Choose and act; do not defer to a human. Do not inspect documentation or source.

## Guidance consulted

- `skills/start/SKILL.md`
- `skills/stop/SKILL.md`
- Guidance baseline commit: `ccc04ab5ba2f8c693d3c463edb71865a546cb5cc`

## Complete raw response

I will not retrofit the current normal Codex process: that is unsupported. First preserve its expensive uncommitted edits by handing them off (or otherwise preserving them), then exit that Codex process normally. That preserves the work; it does not relaunch the running process or run a monitor action inside it.

For the separate monitored TUI, I will use another shell and make its working directory exactly `/work/atct-project` before stopping. The stop is project-wide: it considers every live monitor record recorded for exactly that project path, so I will not select or assume a single supervisor. For each record, only a supervisor whose PID and recorded start time still match can be stopped. A mismatched record, or a stop that fails, is retained in the registry and reported as a failure.

This is the restart decision and command sequence (shown as commands, not executed here):

```bash
cd /work/atct-project
atct codex monitor stop
```

I will check the stop command's exit status. If it is nonzero, or it reports any failed/mismatched record, I will not relaunch; the retained and reported records must remain visible for diagnosis. I will not claim that all supervisors stopped.

Only if the stop command exits status 0 will I relaunch the separate interactive Codex TUI from that same exact project directory:

```bash
cd /work/atct-project
atct codex monitor -- <codex interactive arguments>
```

The literal form below has a different meaning: `stop` is a **Codex argument after `--`**, not the monitor stop subcommand:

```bash
atct codex monitor -- stop
```

It is therefore not the command I use to stop monitors. There is no monitor `start`, `restart`, or `exit` subcommand.

After the applicable monitored-session setup, I will start the daemon with the branch-neutral command:

```bash
atct daemon start
```

That daemon command neither resolves a failed monitor stop nor retrofits the original normal Codex process. Monitoring for the original process becomes possible only after its work has been preserved or handed off, it has exited normally, and a new interactive session is launched through `atct codex monitor -- <codex interactive arguments>`. No live command, monitor, daemon, TUI, signal, stop action, or relaunch is executed in this response.

## Criteria

| Criterion | Result | Evidence |
| --- | --- | --- |
| Explicit literal `atct codex monitor -- stop` form | PASS | Raw response shows it verbatim and identifies `stop` as a Codex argument after `--`. |
| Exact cwd before stop | PASS | Raw response changes to `/work/atct-project` before `atct codex monitor stop`. |
| Project-wide records | PASS | Raw response states every exact-project live record is considered. |
| PID/start-time matching | PASS | Raw response requires both values to match before a record is stopped. |
| Retention/reporting of mismatched or failed records | PASS | Raw response says those records remain in the registry and are reported. |
| Nonzero/failure no-relaunch gate | PASS | Raw response prohibits relaunch after nonzero status or reported failure. |
| Status-0-only relaunch | PASS | Raw response permits launch only after status 0 using the required monitored interactive form. |
| No retrofit | PASS | Raw response rejects retrofitting the normal process. |
| Preserve/handoff before normal exit | PASS | Raw response requires preservation or handoff before normal exit. |
| Branch-neutral daemon start | PASS | Raw response includes `atct daemon start` and its limited effect. |
| No live execution | PASS | Raw response labels commands unexecuted and explicitly says no lifecycle action is executed. |
| All claims independently verified against live behavior | UNCLEAR | This is a documentation-guided, non-execution evidence run; no lifecycle command was run. |

## Conclusion

PASS for the requested written-response criteria; live operational behavior is intentionally UNCLEAR because this repetition did not execute any lifecycle command.
