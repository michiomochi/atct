# atlas を実際に入れる

日付: 2026-08-27
ゴール: 144「atlas で移行を管理する形へ移す」

## 解く問題

`doc/specs/2026-08-19-atlas-migrations.md` は atlas を前提に移行機構を設計した。
**移行機構だけが作られ、atlas は入らなかった。**

    $ atlas を要求する記述
    2026-08-19-atlas-migrations.md 「開発者が atlas migrate diff の --to に渡す」
    2026-08-19-atlas-migrations.md 「atlas migrate diff が差分なしを返すこと」

    $ 実際に入っているもの
    なし。go.mod にも、PATH にも、（そもそも aqua.yaml が無いので）aqua にも無い

結果、「移行を流した結果」と「`schema.sql`」の一致は誰も検査していない。
いま在る唯一の同期機構は `internal/store/migrations_test.go` の
`TestSchemaSQLCompletionReportCheckMatchesGoLimit` で、**`schema.sql` に
`length(<列>) <= 2000` というリテラルが在るかを見るだけ**である。

`doc/specs/2026-08-19-sqlc-adoption.md` は末尾で同じことを宿題に残している
（「検査（未実装）」）。**本ゴールはその宿題を atlas で片付ける。**

## 実測（2026-08-27）

### 移行を流した結果と `schema.sql` はテキストでは一致しない

    $ for f in internal/store/migrations/*.sql; do sqlite3 mig.db < "$f"; done
    $ sqlite3 dec.db < schema.sql
    $ diff <(dump mig.db) <(dump dec.db)

差は 3 種類。

    1. 空白と改行     schema.sql は CREATE INDEX を複数行に折る。sqlite_master は 1 行
    2. 識別子の引用   移行側は CREATE TABLE "goals"、宣言側は CREATE TABLE goals
                      （0018〜0021 の表再構築を SQLite が引用付きで書き戻すため）
    3. schema_migrations  0001_baseline.sql:85 が作る。schema.sql には無い

**1 と 2 があるので、`sqlite_master` のテキスト比較は検査にならない。**
`2026-08-19-sqlc-adoption.md` が「文字列の完全一致では比べない」と書いたとおりである。
**意味で比べる道具が要る。それが atlas を入れる理由である。**

### 稼働個体の `runs` 幽霊はもう無い

ゴール本文は稼働 DB に `runs` 表と余分な FK 1 本が残ると書いている。**もう無い。**

    $ sqlite3 ~/.atct/atct.db 'SELECT name FROM sqlite_master WHERE type="table"'
    agent_sessions decisions goal_handoffs goals projects
    schema_migrations task_commits task_handoffs tasks
    （runs は無い）

    $ FK の本数（稼働 / 移行を流した新規）
    13 / 13

`0018`〜`0020`（ID の連番化）が全表を再構築したときに一緒に落ちた。
稼働個体と新規 DB の唯一の差は **`tasks.files` 列**で、これは稼働 daemon が
`0021_drop_task_files.sql` を含まない旧バイナリだからである。新バイナリに入れ替われば消える。

**したがって完了条件 4 は「直す」ではなく「再発を捕まえる検査を持つ」ことである。**

## 決定

### D1. atlas は Go ライブラリとして、テストからだけ使う。CLI は入れない

    import (
        atlas "ariga.io/atlas/sql/schema"
        "ariga.io/atlas/sql/sqlite"
    )

**CLI を入れる道は 2 つとも潰れた。実測である。**

`go tool ariga.io/atlas/cmd/atlas` は依存衝突で入らない。

    $ go get -tool ariga.io/atlas/cmd/atlas
    google.golang.org/genproto/googleapis/rpc/code: ambiguous import: found package
    in multiple modules:
      google.golang.org/genproto v0.0.0-20220802133213-ce4fa296bf78
      google.golang.org/genproto/googleapis/rpc v0.0.0-20260825221802-da73d73af1c5

    $ go get -tool ariga.io/atlas/cmd/atlas google.golang.org/genproto@latest
    gocloud.dev/runtimevar/gcpsecretmanager imports
      google.golang.org/genproto/googleapis/cloud/secretmanager/v1:
      cannot find module providing package

`cmd/atlas` は gocloud.dev 経由で GCP Secret Manager を引き込み、genproto の monorepo 版を
要求する。このリポジトリは sqlc 経由で分割版 `genproto/googleapis/{api,rpc}` を持つ。
**両立する genproto の版を探す作業になり、当たっても脆い。**

aqua も採らない。**aqua 自体がリポジトリ外の前提である**（この machine では Homebrew 由来、
root-dir は `~/.local/share/aquaproj-aqua`）。人間の判断（2026-08-27）:
「aqua でいれると atct レポジトリだけで完結しないから避けたい」。

**ライブラリなら入る。**

    $ go get ariga.io/atlas/sql/sqlite ariga.io/atlas/sql/schema
    go: added ariga.io/atlas v1.3.0     （依存 11 個、衝突なし）
    $ go build ./...                    BUILD OK

**配布バイナリには入らない。**

    $ go list -deps ./cmd/... | grep -c '^ariga.io/atlas'
    0

`_test.go` からだけ import するので、`./cmd/...` の依存グラフに現れない。
`2026-08-19-atlas-migrations.md` の「利用者に atlas バイナリを要求しない」を満たす。

副作用が 2 つある。**go directive が `1.26.0` → `1.26.4` に上がり**（atlas v1.3.0 の要求）、
`golang.org/x/text` が `v0.36.0` → `v0.37.0` に上がった。

### D2. atlas の担当は「生成」と「検査」だけ。実行時の適用器は据え置く

`2026-08-19-atlas-migrations.md` の既決をそのまま引き継ぐ。

- 適用は `internal/store/migrations.go` の `applyEmbeddedMigrations` が行う
- 版の記録は `schema_migrations` 表。`atlas_schema_revisions` は作らない
- 利用者に atlas バイナリを要求しない

atct は単一バイナリで配布される。実行時に外部 CLI を呼べない。

### D3. baseline は既存 21 本をそのまま採用する。改名も再生成もしない

**既存ファイルの中身を 1 文字も変えない。**変えても `schema_migrations` の記録は
ファイル名だけなので誰も気づかず、既に適用済みの個体と新規個体が静かに分岐する。

`atlas.sum` は作らない。atlas CLI を入れない（D1）ので、ディレクトリの
ハッシュ整合を検査する主体が居ない。**代わりに D6 の一致検査がその役目を果たす**——
移行を流した結果が宣言と食い違えば落ちるので、ファイルが書き換われば検出される。

baseline に「取り込む」作業は実質ゼロである。atlas は移行ディレクトリを読まず、
**移行を流した結果の DB を読む。**ファイル名の形式も版番号の付け方も atlas に関係しない。

### D4. sqlc の入力は移行ディレクトリにする（当初の判断を撤回した）

**最初はこう決めていた**——「sqlc の入力は `schema.sql` のまま。`sqlc generate` は走らせない」。
理由は生成物が既にずれていて再生成すると build が壊れるからだった。

**人間が却下した（decision 452）。**

> sqlc と atlas は https://docs.sqlc.dev/en/latest/howto/ddl.html に従い連携するようになってる？

**なっていなかった。**sqlc のドキュメントは移行ツール（atlas / dbmate / golang-migrate /
goose / sql-migrate / tern）を使う場合、`schema` に**移行ディレクトリ**を指すよう明示している。
既存のファイル名 `%04d_name.sql` はドキュメントが求めるゼロ埋め連番をすでに満たしていた。

**指摘は正しい。**正本を 2 つ残したうえで乖離を検出する仕組みを作るより、正本を 1 つにして
乖離が起こらないようにするほうがよい。**検査で捕まえるのと、起こらなくするのは別である。**

    sqlc.yaml:  schema: internal/store/migrations

**入力を差し替えても生成結果はほぼ変わらない（実測）。**一時ディレクトリで
`schema.sql` からと移行ディレクトリからの 2 通りを生成して比べた。

    差は SchemaMigration 構造体 1 つだけ（0001_baseline.sql が schema_migrations を作るため）
    他は完全に同一

### D4a. 生成物は手で書き換えられていた

**再生成が build を壊した本当の理由は、生成ファイルが手編集されていたことである。**
クエリ原本を 1 文字も変えずに再生成して SQL 定数を突き合わせると、2 箇所が食い違った。

    getGoal                      NULLIF(CAST(derived_from_goal_id AS INTEGER), 0) AS derived_from_goal_id
    listAppliedDecisionsForTask  COALESCE(task_id, '') AS task_id

どちらも `internal/store/queries/` には無い。**生成ファイル側だけにあった。**
加えて `getGoal` は Go の型も手で `sql.NullInt64` に直されていた。

    resolveGoalIDByLegacyPrefix / resolveDecisionIDByLegacyPrefix
    resolveProjectIDByLegacyPrefix / resolveTaskIDByLegacyPrefix

この 4 つはクエリ原本に既に存在せず、生成ファイルにだけ残っていた。削除して build は通る。

**扱いを分けた。**

`getGoal` の CAST は残す。壊れた参照を持つゴールでも詳細画面が描けることを
`TestHTTPGoalDetailOmitsMissingDerivedFromGoal` が担保している。**クエリ原本へ移した。**
sqlc は計算列の型を推論できず `any` にするので、`internal/store/goal.go` の
`derivedFromGoalID` で明示的に絞る。

`listAppliedDecisionsForTask` の `COALESCE(task_id, '')` は消す。文字列 ID 時代の名残で、
この query は `task_id = ?` で絞るため NULL は来ない。`Decision.TaskID` が
`sql.NullInt64` になった今は素の `task_id` が正しい。

### D4b. 再生成に差が出ないことを検査する

**この乖離が残り続けたのは、誰も検査していなかったからである。**
`script/schema-check.sh` が `go tool sqlc generate` を走らせ、
`internal/store/sqlcgen` に差分が出たら落ちる。

`*Row` 型が消えて共有モデルに寄ったため、`internal/store/decision.go` の
10 個の変換クロージャが同一になった。`decisionRowFromSQLC` 1 つに畳んだ。

### D5. `schema_migrations` は比較から除外する。`schema.sql` には足さない

足すと `schema.sql` は sqlc の入力なので、sqlc が使いもしないモデルを生成する対象になる
（そして D4 のとおり生成は走らせられない）。`schema.sql` は「アプリのスキーマ」の
意味を保つ。atlas 側で除外する。

### D6. 一致検査は `internal/store` の Go テスト

移行を全部流した一時 DB と、`schema.sql` を流した一時 DB を atlas に inspect させ、
`SchemaDiff` が 0 件であることを検査する。`schema_migrations` は
`atlas.InspectOptions{Exclude: []string{"schema_migrations"}}` で外す（D5）。

CLI を使わないので `go test ./...` だけで完結する。外部バイナリの用意が要らない。

**リテラル存在チェック `TestSchemaSQLCompletionReportCheckMatchesGoLimit` は置き換える。**
Go の定数 `completionReportMaxLength`（`internal/store/schema.go:5`）と SQL の CHECK の
一致は別の関心なので、**移行を流した DB の CHECK 制約を読んで定数と突き合わせる**形にする。
ファイルのリテラル検索ではなく materialize した結果を見る。

### D6a. 差分を取る前に 2 つ正規化する

**どちらも「SQLite にとって同じ意味なのに atlas が差分と言う」形である。**

**1. CHECK 式は生テキストで比較される。**`schema.sql` の CHECK 本文は
`0018_integer_primary_keys.sql` より 2 スペース浅く、意味は同じなのに差分になる。

`schema.sql` のインデントを移行に合わせる手もあるが採らない。**次にどれかの表を
再構築するたびに `schema.sql` を合わせ直す必要が生じ、意味と無関係な失敗で
検査が信用されなくなる。**`strings.Join(strings.Fields(expr), " ")` で潰す。

**2. 暗黙インデックスの名前。**インライン `UNIQUE` から SQLite が作る
`sqlite_autoindex_<表>_<n>` を、atlas の diff は列から導いた明示名へ改名しようとする。
**片側にだけそれを行うので、同じスキーマ同士でも改名として出る。**
両側で `<表>_<列>` へ正規化する。**落とすのではなく揃える**ので、
UNIQUE が本当に消えていればインデックスの不在として見える。

**正規化は本物の変更を隠さない（実測）。**

    2000 -> 1999 に変えた schema      schema drift: DropCheck ... / AddCheck ...
    列を 1 本足した schema            schema drift: AddColumn projects.probe_col
    runs を注入した稼働 DB のコピー   schema drift: DropTable runs
    正しい schema                     PASS

### D6b. `projects` の inline UNIQUE を明示インデックスへ移す（新しい移行 0022）

D6a-2 の正規化があれば検査は通る。**それでも原因は消しておく。**

`projects` だけが UNIQUE をインラインで宣言していた。

    name       TEXT NOT NULL UNIQUE
    root_path  TEXT NOT NULL UNIQUE

**この schema の他の UNIQUE は全て `CREATE UNIQUE INDEX` である。`projects` だけが例外だった。**

`0022_projects_explicit_unique_indexes.sql` で `projects` を再構築し、
`idx_projects_name` と `idx_projects_root_path` を明示的に作る。
列は 1 つも変わらないので `migrations.go` の `requiredCurrentV6Columns["projects"]` は無変更。
旧バイナリも読める（列が変わらず UNIQUE の強制も残る）。

**正規化と 0022 は役割が違う。**0022 は宣言を揃える。正規化は、
0022 より前を記録している稼働個体を検査するときに要る（D7 は過去の状態と比べるため、
0022 が適用されるまで両側に `sqlite_autoindex_*` が残る）。

### D7. 稼働個体の drift 検査は、その個体自身の記録と比べる

同じ atlas ライブラリで、稼働 DB の**コピー**を inspect する。
**稼働 DB 本体には触らない。**`script/schema-check.sh` が `VACUUM INTO` でコピーを作り、
パスを `ATCT_DRIFT_DB` でテストへ渡す。DB が無ければ何もせず 0 で抜ける。

**比較先は `schema.sql` ではない。**その個体の `schema_migrations` が「適用済み」と
記録している移行だけを流した DB と比べる。

理由: **未適用の移行はずれではない。**リリースは必ず「移行を含む新バイナリを出す →
daemon を入れ替える → 適用される」の順で進むので、リリース直前の稼働 DB は必ず
最新の移行を持たない。`schema.sql` と比べる形にすると、**移行を足すたびに
`script/release.sh` が落ちる。**実測でも `DropColumn tasks.files` が出た（`0021` 未適用）。

捕まえたいのは**どの移行でも説明できない形**である。`migrateSchema` の歴史的ブリッジが
残した `runs` がその実例である（D9）。

    $ 稼働 DB のコピーに runs を注入して実行
    schema drift: DropTable runs

### D8. CI は無い。ゲートは `script/release.sh` である

`.github/` は存在しない。`script/release.sh` の `go test -count=1 ./...` が唯一の
自動ゲートである。atlas を要する検査をここへ入れる。**CI を新設しない**（本ゴールの範囲外）。

### D9. `migrateSchema` の歴史的ブリッジは残す

`2026-08-19-atlas-migrations.md` の既決。`user_version` が 1..5 で止まった DB を
v6 へ運ぶ経路は baseline では代替できない。削除は取り消せない判断であり、本ゴールでは扱わない。

**ただしこれが `runs` 幽霊の出どころだった**ことを記録に残す
（`version == 1..5` の経路で `0001_baseline.sql` が再実行され、
`CREATE TABLE IF NOT EXISTS runs` が復活した）。D7 の検査はこの再発を捕まえる。

## 検証（2026-08-27 実測）

**新しくできるようになること**

    schema.sql に列を 1 本足す      schema drift: AddColumn projects.probe_col
    CHECK の 2000 を 1999 にする    schema drift: DropCheck / AddCheck
    稼働 DB のコピーに runs を注入  schema drift: DropTable runs
    正しい状態                      TestSchemaParity / TestSchemaParityDrift ともに PASS

**壊れてはいけないこと**

    go build ./...                                          BUILD OK
    go vet ./...                                            VET OK
    gofmt -l .                                              空
    go test -count=1 -timeout 600s ./...                    全パッケージ ok
    cd web && pnpm test                                     222/222 passed
    go list -deps ./cmd/... | grep -c '^ariga.io/atlas'     0
    git diff --stat internal/store/sqlcgen/                 空
    git diff --stat internal/store/migrations/00[01]*.sql   空（0001〜0021 は無変更）
    shasum -a 256 -c（稼働 DB の前後）                       OK

実 HOME（`~/.atct`）へは書き込まない。読むのは `VACUUM INTO` のコピーだけである。

## やらないこと

- 稼働 DB へ移行を適用しない（daemon の入れ替えで自然に当たる）
- `sqlc generate` を走らせない（D4）
- CI を新設しない（D8）
- `migrateSchema` の歴史的ブリッジを削らない（D9）
- リリースしない
