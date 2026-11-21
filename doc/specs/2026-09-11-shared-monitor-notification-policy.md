# Shared monitor notification policy

## Goal

Claude monitor と Codex monitor が、同じ監視 scope・同じ canonical watch event に対して、
同じ通知条件と同じ本文を受け取るようにする。transport は配信だけを担当し、通知対象の
選別を持たない。

## Current fault

`formatWatchDecision` は `detection.monitor_lost` を通知文へ整形するが、
`selectWatchAgentAction` の手動ホワイトリストに同 event がない。そのため Claude
monitor にも Codex bridge にも action として届かない。新しい整形済み event を追加する
たびに同じ漏れを起こせる構造である。

## Design

`selectWatchAgentAction` を唯一の policy boundary とする。空行を除き、scope filter と
delivery deduplication を通過して整形された event は action として選ぶ。通知不要な
観測・状態変化だけをこの関数で明示的に除外する。

除外する event は次に限定する。

- `decision.pending`、`decision.opened`、default が適用された `decision.answered`
- 通常の `plan.handoff.request`、`plan.handoff.receive`、`plan.handoff.complete`
- `detection.handoff_unreceived`、`detection.handoff_unreported`
- `keepalive` と event 名を持たない接続診断

それ以外の既存および将来の整形済み canonical event は、追加の transport 別判定なしに
action となる。これにより `detection.monitor_lost` も action になる。

Claude monitor は選択済み `watchAgentAction.line` を標準出力へ一度だけ書く。Codex
monitor は同一の `watchAgentAction` を bridge queue へ渡す。いずれも raw line を独自に
再分類しない。

## Scope

watch scope のサポート範囲はこの変更で揃えない。Claude の CLI が executor task scope を
持たず、Codex は持つという起動面の差異は残る。両者に共通して存在する scope では、
scope filter 通過後の通知条件と本文は同一である。

## Verification

- `detection.monitor_lost` を shared selector の action regression に加える。
- 通知対象・除外対象を含む matrix を、Claude writer と Codex bridge の両方に流し、同じ
  event、本文、順序を検査する。
- `go test ./cmd/atct` と `go test ./...` を実行する。
