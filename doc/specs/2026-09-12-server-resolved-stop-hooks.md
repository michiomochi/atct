# Server-resolved stop hooks

## Goal

Claude Code と Codex の Stop hook を、harness の `session_id` から ATCT server が解決する
同一の停止判定へ統一する。未完了作業がある role は同じ理由文で continuation を受け、
`handoff yielded` は廃止する。

## Session identity

両 harness の SessionStart / Stop hook input は stable な `session_id` を持つ。SessionStart
は登録済み ATCT project でだけ、この値を `atct_session_identify` の `session_key` として
使うよう agent に指示する。任意の agent 名や process ID を session key に使わない。

`session.identify` 済みの ATCT server は、`session_key` から canonical agent session を
引ける。Stop hook は role、project、goal、task を環境変数や cwd から推測しない。

## Common hook contract

両方の hook は raw JSON input を `atct stop-check --hook-input` へ渡す。

- `stop_hook_active: true` は空出力で許可する。
- `session_id` が無い、未識別、daemon RPC 失敗、または state 読取り失敗は
  `{"decision":"block","reason":"ATCT stop-check failed: ..."}` を返す。
- 作業がない identified session は空出力で許可する。
- 作業が残る session は `{"decision":"block","reason":"ATCT work remains: ..."}` を返す。

Claude と Codex はともにこの JSON を native Stop response として扱う。binary の場所だけは
harness ごとに異なってよい。Claude は plugin 内の binary を、role-scoped Codex monitor は
その wrapper が渡す `ATCT_BIN` を使う。

## Server resolution

`stop-check` は daemon の `session.stop_check` RPC を呼ぶ。daemon は次を atomically read-only
に解決する。

1. session key で canonical agent session を取得する。
2. project claim なら commander、受領済み open goal handoff なら subcommander、受領済み
   open task handoff なら executor と導出する。
3. commander は active goal、subcommander は自身の open goal / plan / task-create handoff と
   子 task review、executor は自身が受領した open task handoff を調べる。

複数の executor handoff が残る不整合時も、いずれかが open なら block する。ATCT に未登録の
session key は role を推測せず、識別を求める block response とする。

## Removal

`handoff yielded` CLI、daemon RPC、event、watch formatter / filter / selector、Stop hook 呼出し、
関連テストと現行挙動の文書を削除する。これは handoff を完了・回復しない重複可能なヒントであり、
`detection.monitor_lost` と server-resolved stop check に置き換えられる。

Codex の `ATCT_ROLE`、`ATCT_PROJECT_ID`、`ATCT_GOAL_ID`、`ATCT_TASK_ID` は Stop hook 用には
不要になる。explicit monitor は binary 解決のため `ATCT_BIN` だけを渡す。

## Verification

- Claude と Codex の同じ hook input が同一の stop-check JSON を得る。
- `session_id` を session key として identify した commander、subcommander、executor が、それぞれ
  自分の未完了作業で block され、他 role の作業だけでは block されない。
- 未識別 session と daemon RPC failure は fail closed、active hook は silent に通る。
- `handoff yielded` の command、RPC、event、watch action が残らない。
