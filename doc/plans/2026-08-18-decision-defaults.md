# 決定の既定値とタイムアウト — 実装計画

> **For agentic workers:** この計画はタスクごとに実装する。各タスクは独立してテストできる
> 単位になっている。

**Goal:** 人間が答えないまま時間が過ぎても、決定が既定値で確定して作業が進むようにする。

**Architecture:** `decisions` テーブルに既定の選択肢と期限を持たせ、期限を過ぎた決定を
確定させる経路を daemon 側に置く。確定した決定は人間が答えたものと区別して記録する。

**Tech Stack:** Go 1.26、SQLite（`modernc.org/sqlite`）、React 19 + react-i18next

**Spec:** `doc/specs/2026-08-18-atct-autonomy.md` の 2.3 と 4.1

## Global Constraints

- **マイグレーション機構が存在しない。** スキーマは `CREATE TABLE IF NOT EXISTS` で
  作られるだけなので、既存の DB に列は追加されない。`PRAGMA user_version` を見て
  `ALTER TABLE` を実行する経路が要る。**別のタスクで同じ機構を作っている可能性があるので、
  着手前に `internal/store/schema.go` の現状を確認すること**
- **実ホームの `~/.atct/atct.db` を壊さない。** 検証は一時 HOME で行う
- **MCP のツール定義で既存の引数の形を壊さない。** 既定値を渡さない呼び出しが
  そのまま動くこと。定義を変えると既存のセッションが壊れる
- i18n のキーは `en.ts` と `ja.ts` の両方に足す。parity テストがある
- `web/dist/` は git 管理下なので、UI を変えたら `pnpm build` の結果もコミットする

---

### Task 1: スキーマに既定値と期限を足す

**Files:**
- Modify: `internal/store/schema.go`
- Modify: `internal/domain/model.go`（`Decision`）
- Test: `internal/store/decision_default_test.go`（新規）

**Interfaces:**
- Produces: `Decision.DefaultOption string`、`Decision.DefaultAfterMs *int64`、
  `Decision.DefaultAppliedAt *time.Time`

**設計の理由:**

`DefaultAfterMs` をポインタにするのは、**「指定なし」と「0」を区別する**ため。
`wait_ms` で同じ間違いを既にしている（`<= 0` で既定値に潰していた）。0 は
「即座に既定値で確定する」という有効な指定である。

`DefaultAppliedAt` を別に持つのは、**人間が答えたのか既定値で確定したのかを
区別する**ため。`answered_at` だけでは見分けが付かない。人間の判断と機械の
確定を同じ欄に混ぜると、後から追えなくなる。

- [ ] **Step 1: 失敗するテストを書く**

```go
func TestDecisionRoundTripsDefaultFields(t *testing.T) {
    s := newTestStore(t)
    after := int64(1800000)
    d := domain.Decision{
        GoalID: goalID, Question: "A か B か", Kind: "decision",
        DefaultOption: "A", DefaultAfterMs: &after,
    }
    saved, err := s.CreateDecision(ctx, d)
    if err != nil { t.Fatal(err) }
    got, err := s.GetDecision(ctx, saved.ID)
    if err != nil { t.Fatal(err) }
    if got.DefaultOption != "A" { t.Fatalf("DefaultOption = %q, want A", got.DefaultOption) }
    if got.DefaultAfterMs == nil || *got.DefaultAfterMs != 1800000 {
        t.Fatalf("DefaultAfterMs = %v, want 1800000", got.DefaultAfterMs)
    }
    if got.DefaultAppliedAt != nil { t.Fatal("DefaultAppliedAt should be nil before it fires") }
}
```

- [ ] **Step 2: 落ちることを確認**

`go test ./internal/store/ -run TestDecisionRoundTripsDefaultFields -v`
期待: `DefaultOption` が未定義でコンパイルエラー

- [ ] **Step 3: 列と型を足す**

`decisions` に `default_option TEXT NOT NULL DEFAULT ''`、
`default_after_ms INTEGER`、`default_applied_at TEXT` を足す。
`PRAGMA user_version` を上げ、既存 DB には `ALTER TABLE` で追加する。

- [ ] **Step 4: 通ることを確認**

- [ ] **Step 5: 既存 DB の移行テストを足す**

**列が無い状態の DB を作ってから開き、既存の decision が読めることを確かめる。**
これが無いと人間のデータが失われても気づけない。

- [ ] **Step 6: コミット**

---

### Task 2: `atct_decision_ask` で既定値を渡せるようにする

**Files:**
- Modify: `internal/mcpshim/tools.go`
- Modify: `internal/rpc`（`decision.ask` のパラメータ）
- Test: `internal/mcpshim/schema_test.go`

**Interfaces:**
- Consumes: Task 1 の `Decision` フィールド
- Produces: `atct_decision_ask` の入力に `default_option` と `default_after_ms`

**設計の理由:**

**`default_option` は `options` のいずれかの `label` と一致していなければならない。**
一致しない既定値は、期限が来たときに何を選べばよいか決まらない。
**受け取った時点で拒否すること。** 期限が来てから壊れるより、宣言した時点で
落ちるほうが直しやすい。

`options` が空の決定（自由記述）には既定値を置けない。同じ理由である。

- [ ] **Step 1: 失敗するテストを書く**

```go
func TestDecisionAskRejectsDefaultNotInOptions(t *testing.T) {
    // options に "A" と "B" しかないのに default_option が "C"
    // → エラーになり、decision は作られないこと
}
func TestDecisionAskAcceptsDefaultMatchingOption(t *testing.T) {
    // default_option が "A"、options に "A" がある → 通ること
}
func TestDecisionAskWithoutDefaultStillWorks(t *testing.T) {
    // 既定値を渡さない従来の呼び出しがそのまま動くこと（互換性）
}
```

- [ ] **Step 2: 落ちることを確認**
- [ ] **Step 3: 実装する**
- [ ] **Step 4: 通ることを確認**
- [ ] **Step 5: コミット**

---

### Task 3: 期限切れの決定を既定値で確定させる

**Files:**
- Modify: `internal/store/decision.go`
- Modify: `internal/daemon/server.go`（定期実行の起動）
- Test: `internal/store/decision_expire_test.go`（新規）

**Interfaces:**
- Produces: `(*Store).ApplyExpiredDefaults(ctx context.Context, now time.Time) (int, error)`

**設計の理由:**

**`now` を引数で受け取る。** テストで時間を進められるようにするためであり、
`time.Now()` を関数の中で呼ぶと期限切れのテストが書けない（30 分待つことになる）。

**確定は `status = 'open'` の決定に対してだけ行う。** 人間が既に答えていれば
`answered` になっているので対象外になり、**期限切れ前に人間が答えたら人間の選択が
勝つ**という要件が自然に満たされる。競合の窓を塞ぐため、判定と更新は同じ
トランザクションで行うこと。

- [ ] **Step 1: 失敗するテストを書く**

```go
func TestApplyExpiredDefaultsFiresAfterDeadline(t *testing.T) {
    // default_after_ms = 1000 の decision を作る
    // now = created_at + 2s で ApplyExpiredDefaults → 1 件確定
    // status が answered、answer_label が default_option、
    // default_applied_at が入っていること
}
func TestApplyExpiredDefaultsSkipsAnsweredDecision(t *testing.T) {
    // 人間が先に答えた decision は、期限を過ぎても既定値で上書きされないこと
    // ← 最重要。人間の判断が機械に潰されてはならない
}
func TestApplyExpiredDefaultsIgnoresDecisionWithoutDefault(t *testing.T) {
    // default_after_ms が nil の decision は永久に open のままであること
}
```

- [ ] **Step 2: 落ちることを確認**
- [ ] **Step 3: 実装する**
- [ ] **Step 4: 通ることを確認**
- [ ] **Step 5: daemon から定期的に呼ぶ**

間隔は 30 秒。**daemon の停止時に確実に止まること**（goroutine を残さない）。

- [ ] **Step 6: コミット**

---

### Task 4: 既定値で確定したことを人間に見せる

**Files:**
- Modify: `internal/httpapi/server.go`（`DecisionView` に追加）
- Modify: `web/src/lib/ui.ts`、`web/src/components/DecisionTable.tsx`
- Modify: `web/src/i18n/en.ts`、`web/src/i18n/ja.ts`
- Test: `internal/httpapi/server_test.go`、`web/src/lib/ui.test.ts`

**設計の理由:**

人間の判断と機械の確定が同じ見た目で並ぶと、**「自分が答えた」と誤認する**。
何を機械が決めたのかが分からなくなると、後から見直せない。

文言案:
- ja: `期限切れのため既定値で確定`
- en: `Settled by default after timeout`

- [ ] **Step 1: 失敗するテストを書く**
- [ ] **Step 2: 落ちることを確認**
- [ ] **Step 3: API に `settled_by_default` を足す**
- [ ] **Step 4: UI に表示を足す**
- [ ] **Step 5: `pnpm build` を実行して `web/dist/` もコミット**
- [ ] **Step 6: コミット**

---

### Task 5: SKILL.md に既定値の使いどころを書く

**Files:**
- Modify: `plugin/skills/atct/SKILL.md`

**設計の理由:**

**ツール側では既定値の妥当性を検証しない**と決めた（spec 2.3）。選択肢は自由文なので、
ATCT からは「A で進む」と「本番の DB を削除する」の区別が付かない。
**だから規律として書く必要がある。**

書く内容:

- 既定値を置いてよいのは、**その選択肢が取り消せるときだけ**。判定基準は
  「人間が元の状態を取り戻せるか」（spec 2.2 の列挙を参照させる）
- **既定値は「そのまま進む」側に置く。** 人間が黙っていることを「止まれ」と
  解釈すると、この仕組みの意味が無くなる
- 期限は、その決定が実際に人間を待たせる長さに合わせる。
  **すべてに 30 分を付けない。** 短すぎる期限は人間の判断を奪う

- [ ] **Step 1: 書く**
- [ ] **Step 2: `tests/wrapper_test.bash` に文言のテストを足す**
- [ ] **Step 3: コミット**

---

## Self-Review

**spec の 4.1 との対応:**

| spec の記述 | 実装するタスク |
|---|---|
| 既定値とタイムアウトを持たせる | Task 1, 2 |
| 人間の不在が進行を止めない | Task 3 |
| 人間が答えた決定と区別して記録 | Task 1（`default_applied_at`）, Task 4 |
| いつ、どの既定値で確定したかを残す | Task 1, Task 4 |
| 自己申告に任せる（強制しない） | Task 5（規律として書く） |

**型の一貫性:** `DefaultAfterMs` は Task 1 から 3 まで一貫して `*int64`。
`ApplyExpiredDefaults` の `now time.Time` は Task 3 の中で閉じている。

**未解決:** 既定値で確定した決定を、人間が後から覆せるようにするかは決めていない。
`answered` になった決定を再度開く経路は現在存在しない。**この計画には含めず、
必要になったら別のゴールとして立てる。**
