# Wakeup notification implementation plan

設計 `doc/specs/2026-08-20-wakeup-notification.md` と依頼書に従い、検知条件は第5項
（実行中 claim がなく、着手されていないタスクがある active goal）だけを追加する。

## 手順

1. watch の wakeup 分岐・再通知キー・keepalive 欠落通知を RED テストで固定する。
2. `DecisionEvent` を任意 payload のイベントへ一般化し、HTTP SSE が decision、wakeup、keepalive を運ぶようにする。
3. ClaimLiveness を利用する store の wakeup 判定と、独立した単純カウントを追加する。
4. daemon の既存30秒 tickに、15分継続判定、状態変化時のリセット、keepalive、不一致検知を追加する。
5. pending と watch の表示を追加し、既存の decision 理由と「作業なし」の無出力を維持する。
6. 指定の go test、wrapper test、実 daemon/watch の2分無作業測定を実行する。

## 制約

- 第6〜9項、ClaimLiveness の本体、web、移行ファイルは変更しない。
- 15分と keepalive の時間は注入・短縮可能なテスト形にし、実時間待ちはしない。
- 変更は既存の internal store/httpapi/daemon/cmd と本計画ファイルに限定する。
