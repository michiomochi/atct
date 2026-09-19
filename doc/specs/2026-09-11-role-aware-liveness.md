# Role-aware one-minute monitor liveness

## Goal

Codex monitor が役割上すぐ実行できる ATCT 操作を持つ時だけ、1 分ごとに再確認 turn を
送る。人間判断待ちや delegate の作業待ちには送らない。

## Scope

- `watchLivenessPromptInterval` を 10 分から 1 分へ変更する。
- 判定は既存の `/api/events/reconcile` 応答だけを使う。保存状態、delivery cursor、process の
  再起動は追加しない。
- `open` の decision が scope にある時は全 role で prompt を抑止する。

## Role actions

| role | prompt を送る状態 |
| --- | --- |
| executor | 受領済み・未完了の task handoff が実装待ち、または差し戻し受領済み |
| subcommander | task-create handoff の処理待ち、task review の受領待ち、goal / plan review rejection の受領後、または executor が作業中でない受領済み goal handoff |
| commander | plan / goal handoff review の受領待ち、または承認済み goal review の完了待ち |

review request を提出済みで reviewer を待つ executor、executor が実装中の間の
subcommander、未回答の人間 decision、完了済み handoff は prompt を受けない。

## Non-goals

- Codex TUI / bridge の自動再起動
- human decision の deadline または default
- handoff state の変更

## Verification

`cmd/atct/watch_test.go` で 1 分境界、role ごとの actionable 状態、人間判断待ちと
reviewer/delegate 待ちの抑止を検査する。`go test ./cmd/atct` と `go test ./...` を実行する。
