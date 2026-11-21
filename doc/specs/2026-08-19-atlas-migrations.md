# スキーマ移行を atlas で管理する

日付: 2026-08-19
ゴール: db migration を https://github.com/ariga/atlas でやるようにする

## 解く問題

移行コードが `internal/store/store.go` の `migrateSchema` に手書きで積み上がっている。
v1 から v6 まで 6 段あり、段ごとに条件分岐と DDL が混ざっている。次の段を足すたびに、
既存の段を読み直さないと正しい位置が分からない。

## 決定

### atlas は開発者だけが使う（既決・変更しない）

atlas が生成した SQL をリポジトリへ置き、Go に埋め込む。適用は自前のコードで行う。
利用者に atlas バイナリを要求しない。ATCT は「プラグインを入れるだけで動く」ことを
売りにしており、実行時に外部バイナリを要求するとその前提が崩れる。

### 版の管理は自前のテーブル 1 つだけ

`schema_migrations` テーブルを持ち、適用済みの migration ファイル名を記録する。
atlas の `atlas_schema_revisions` は作らない。利用者環境で atlas CLI を走らせないので
互換性は要らず、方式を 2 つ持つと「未適用がどれか」の判定が分裂する。

### baseline は現行 v6 スキーマと完全に同一にする

`migrations/0001_baseline.sql` は、今の v6 スキーマを 1 文字も変えずに写したものにする。
**DDL を 1 つも変えない。** テーブルを増やすのは `schema_migrations` だけ。

理由は互換性である。リリース済みの旧バイナリは `PRAGMA user_version >= 6` を見て移行を
skip し、v6 スキーマをそのまま読む。baseline でスキーマを変えなければ、旧バイナリは
今までどおり動く。ここを変えると、新しい DB を旧バイナリで開いた瞬間に
`no such column` で落ちる。v0.7.0 で `answered_by` を消したときに同じことを起こしており、
復旧手段がリリースしか無い状態を作った。

### `user_version` は 6 に据え置く

真実は `schema_migrations` に移すが、`PRAGMA user_version` は旧バイナリ向けの下限表示
として残す。スキーマを実際に変える migration を入れたときに初めて上げる。上げた時点で
旧バイナリを弾けるようにするため、`user_version > schemaVersion` のときは明示的に
エラーを返す（現在は静かに skip しており、これは欠陥である）。

### desired state は `schema.sql` 1 つ、実行時には使わない

`schema.sql` を desired state として置き、開発者が `atlas migrate diff` の `--to` に渡す。
**実行時には読まない。** 空の DB にも `migrations/` を順に適用する。
Go 側の `schemaSQL` 文字列（`internal/store/schema.go`）は廃止する。定義が 2 箇所に
あると必ずずれる。

`completionReportMaxLength = 2000` は Go の定数として残す（`goal.go` のバリデーションが
使う）。SQL 側のリテラルと一致することをテストで固定する。

### 歴史的 bridge は消さない

`migrateSchema` のうち「v6 スキーマを作る部分」は migrations へ移す。
**「v1 から v6 への変換」は残す。** 利用者の DB が v3 で止まっている可能性があり、
そこから v6 へ運ぶコードは baseline では代替できない（baseline は変換ではなく採用である）。
タスク「migrateSchema を退役させ、重複した経路を残さない」は、スキーマ定義の重複を
消すことを指し、歴史的変換の削除は含まない。

### 適用の手順

1. `BEGIN IMMEDIATE` で書き込みロックを取り、同じトランザクションで
   `schema_migrations` と `user_version` を読む
2. `user_version == 6` かつ v6 の必須テーブルと列が揃っていれば、baseline を
   「適用済み」として記録する。**baseline SQL は実行しない**
3. 空の DB（`user_version == 0` かつ対象テーブルなし）なら baseline SQL を実行し、
   同じトランザクションで記録する
4. `user_version < 6` なら既存の bridge を先に走らせ、v6 に到達してから baseline を記録する
5. `user_version > 6`、未知の記録、`user_version` と実体スキーマの不一致は中断する。
   静かに進めない
6. baseline より後の SQL をファイル名の昇順で、1 ファイル 1 トランザクションで適用する。
   SQL と記録の更新は同じトランザクションに入れる

## 検証

- 既存 DB（`user_version=6`、19 ゴール）の `VACUUM INTO` コピーで、baseline が
  記録だけを作り、行を 1 つも書き換えないこと
- 空 DB から migrations だけで v6 スキーマができ、`schema.sql` と差分が無いこと
- `user_version=3` のコピーから bridge 経由で v6 に到達し、baseline が記録されること
- `user_version=7` の DB を開くとエラーになること
- `atlas migrate diff` が差分なしを返すこと

実 HOME（`~/.atct`）では検証しない。一時 HOME と専用ポートを使う。

## やらないこと

- リリースはしない。回答は「実装と検証まで。リリースは承認後」（2026-08-19）
- atlas を Go の依存に追加しない
