# 手書き SQL を sqlc に移す

日付: 2026-08-19
ゴール: db orm を https://github.com/sqlc-dev/sqlc を使うようにする

## 解く問題

手書き SQL が 47 箇所ある（decision 14、task 13、goal・project 12、その他 8）。
列を 1 つ増やすと、Scan の順序と引数の数を人間が目で合わせることになる。ずれても
コンパイルは通り、実行時に落ちる。実際 v0.7.0 で `answered_by` を消したとき、
この形の取りこぼしが出た。

## 決定

### 生成型は store の内側に閉じる（人間が 2026-08-19 に回答）

`internal/store` の公開関数と公開型は変えない。生成コードは store の実装だけが使う。
呼び出し側（daemon・httpapi・mcpshim）に変更を及ばせない。

理由: 今の store の型は 3 経路が使っており、生成型に置き換えると変更が全経路へ広がって
1 ゴールでは収まらない。store の中に変換が残るのは受け入れる。

### 置き場所

| もの | 場所 |
|---|---|
| 設定 | リポジトリ直下の `sqlc.yaml` |
| スキーマ | `schema.sql`（atlas 移行で作った唯一の定義。sqlc もこれを読む） |
| クエリ | `internal/store/queries/{decision,task,goal,project}.sql` |
| 生成コード | `internal/store/sqlcgen`（パッケージ名 `sqlcgen`） |

生成するときは `go tool sqlc generate` を実行する。

`schema.sql` を sqlc のスキーマ入力にするのが要点である。定義が 2 箇所になると、
sqlc が古いスキーマでコード生成しても誰も気づかない。

### 生成コードはコミットする

利用者に sqlc を要求しない。`go generate` を通す必要も無い。生成物をリポジトリに置く。
プラグインを入れるだけで動くという前提を崩さない。

### 段階的に移す（既決）

領域ごとに移す。1 領域ずつテストを通してから次へ進む。順序は decision（14 箇所）→
task（13 箇所）→ goal・project（12 箇所）。**decision を最初にするのは最も大きく、
方式の問題がここで全部出るからである。** 小さいところから始めると、最後に方式を
変えることになる。

### トランザクションは既存の形を残す

store には `*sql.DB` を使う経路と `*sql.Tx` を使う経路の両方がある。sqlc の
`Queries.WithTx(tx)` で後者を賄う。既存のトランザクション境界は変えない。移行と
境界の変更を同時にやると、壊れたときにどちらが原因か分からない。

### 移行しないもの

`migrateSchema` と `internal/store/migrations.go` の SQL は sqlc に移さない。
DDL であり、sqlc が扱うのは DML である。

## 検証

- 領域ごとに `go test -count=1 ./internal/store/...` が通ること
- 全領域を移した後、実 DB の `VACUUM INTO` コピーに対して daemon を起動し、
  ゴール・タスク・決定の件数が移行前と一致すること
- 生成コードを消して `go tool sqlc generate` を再実行しても差分が出ないこと

実 HOME（`~/.atct`）では検証しない。一時 HOME と専用ポートを使う。

## やらないこと

- リリースはしない。回答は「実装と検証まで。リリースは承認後」（2026-08-19）
- 公開 API の型を生成型に置き換えない
