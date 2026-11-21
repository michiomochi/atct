# Unified Wakeups

## Goal

daemon の定期評価で発行するすべての `wakeup*` event を、一つの Wakeup として実装する。

## Current state

`wakeupTracker.evaluateWith` はすべての Wakeup を同じ定期評価で実行し、`wakeups` map に
対象ごとの状態を持つ。actionable wakeup は初回待ち後に再送し、それ以外の `wakeup.*` は
待ち時間後に一度だけ送る。差は通知ポリシーだけである。

## Design

`wakeupTracker` は key ごとの一つの Wakeup state を持つ。

```go
type wakeupState struct {
    activeSince time.Time
    lastPublished time.Time
    published bool
}
```

actionable wakeup は key `"actionable:<project-id>"`、`InitialWait=3m`、
`ResendInterval=3m` の Wakeup として評価する。その他の `wakeup.*` は
`"<event-name>\x00<target-id>"`、その event 固有の wait、`ResendInterval=0` の Wakeup
として評価する。

共通 helper は Wakeup の開始、開始時刻の更新、wait 判定、再送判定、対象状態が消えた時の削除を行う。
event 名・payload・scope filter・表示文・action 判定は Wakeup に統一する。

## Constraints

- DB migration や永続的な tracker state を追加しない。
- `EventWakeup` の初回 3 分・3 分再送を変えない。
- 各 `wakeup.*` の wait と一度だけ発行する挙動を変えない。
- Wakeup の対象状態が消えた後の再出現は新しい Wakeup として扱う。
- `wakeup.discrepancy` と `wakeup.evaluate_failed` の既存挙動は変えない。

## Verification

- actionable wakeup の初回待ち、再送、消滅後の再出現を既存どおり確認する。
- `wakeup.*` の初回待ち、一度だけの発行、消滅後の再出現を確認する。
- `go test ./internal/daemon -run 'Wakeup' -count=1` と `go test ./... -count=1` を通す。
