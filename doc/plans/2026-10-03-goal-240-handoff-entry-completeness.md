# Goal 240 handoff entry completeness plan

Spec: doc/specs/2026-10-03-goal-240-handoff-entry-completeness.md

## Task 1: 欠けている遷移に entry を同一 tx で書く
internal/store/task_handoff.go と goal_handoff.go の次の関数に `appendHandoffEntryTx` を足す:
RequestTaskHandoffReview / ReceiveTaskHandoffReview / RejectTaskHandoffReview /
ReceiveTaskHandoffReviewRejection / RecoverTaskHandoff / AmendTaskHandoffReport と goal 側の対応する関数。
いずれも状態更新と同じ tx（tx が無い関数は tx を開く）。kind と body と author は spec の表のとおり。
`appendHandoffEntryTx` の amend / system skip を除く。既存の entry の期待を持つ test が変わるなら、期待を新しい契約に合わせる。
検証: `go test ./internal/store/... -count=1`

## Task 2: 完全性を固定する test
internal/store/handoff_entry_completeness_test.go を新設する。spec の「2.」の 4 つ
（不変条件ヘルパー、全遷移の table-driven、sqlc query の目録、原子性）を task と goal の両方で。
検証: `go test ./internal/store/... -count=1`
