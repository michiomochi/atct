# Unified wakeup conditions

## Goal

daemon の定期評価で発行する actionable wakeup と `detection.*` を、一つの wakeup condition
概念として実装する。利用者向け文書では Detection を独立した通知機構として扱わない。

## Current state

`wakeupTracker.evaluateWith` が両方を同じ定期評価で実行している一方、状態は二重である。

- actionable wakeup: `activeSince map[int64]time.Time` と `published map[int64]time.Time`
- `detection.*`: `detectionActiveSince map[string]time.Time` と
  `detectionPublished map[string]bool`

前者は初回待ち後に再送し、後者は待ち時間後に一度だけ送る。差は通知ポリシーであり、追跡する
概念はどちらも「condition がいつから継続しているか」である。

## Design

`wakeupTracker` は key ごとの一つの condition state を持つ。

```go
type wakeupConditionState struct {
    ActiveSince time.Time
    LastPublished time.Time
    Published bool
}
```

actionable wakeup は key `"actionable:<project-id>"`、`InitialWait=3m`、
`ResendInterval=3m` の condition として評価する。`detection.*` は
`"<event-name>\x00<target-id>"`、その event 固有の wait、`ResendInterval=0` の condition
として評価する。

共通 helper は condition の開始、開始時刻の更新、wait 判定、再送判定、条件消滅時の削除を行う。
イベントの名前・payload・scope filter・表示文・action 判定は変更しない。既存の
`detection.*` event 名は外部 contract として維持するが、内部・文書で別 tracker としては扱わない。

## Constraints

- DB migration や永続的な tracker state を追加しない。
- `EventWakeup` の初回 3 分・3 分再送を変えない。
- 各 `detection.*` の wait と一度だけ発行する挙動を変えない。
- condition が消えた後の再出現は fresh condition として扱う。
- `wakeup.discrepancy` と `wakeup.evaluate_failed` の既存挙動は変えない。

## Verification

- actionable condition の初回待ち、再送、消滅後の再出現を既存どおり確認する。
- condition-specific event の初回待ち、一度だけの発行、消滅後の再出現を確認する。
- `go test ./internal/daemon -run 'Wakeup|Detection' -count=1` と `go test ./... -count=1` を通す。
