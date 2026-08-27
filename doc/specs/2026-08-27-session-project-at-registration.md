# セッション登録時に project を決める

ゴール 172。`agent_sessions.project_id` の 94% が NULL である件。

## 現況（実測 2026-08-27）

```
$ sqlite3 -readonly ~/.atct/atct.db "select case when project_id is null or project_id=''
    then 'NULL/empty' else 'set' end k, count(*) from agent_sessions group by k;"
NULL/empty|2213
set|146
```

`internal/store/queries/task.sql` の `RegisterAgentSession` が `project_id` に NULL を入れる。
`run.register`（`internal/daemon/handler.go:503`）が受け取るのは `pid` だけで、
project を知らないまま登録される。後から `ensureAgentSessionProject`
（`internal/daemon/handler.go:291`）が埋めているので動いている。

## 決定 1: 登録時に cwd から project を解決する

`run.register` に `cwd` を追加する。daemon は `store.ResolveProject(ctx, cwd)`
（`internal/store/project.go:159`）で project を解決し、`RegisterAgentSession` に渡す。

`ResolveProject` は既に worktree を主リポジトリへ正規化してから `root_path` の最長一致で引く。
**worktree で起動したセッションも主リポジトリの project に寄る。**新しい解決経路を作る必要は無い。

`cwd` を渡せる呼び出し元:

- `cmd/atct-mcp/main.go` — MCP シム。プロセスの cwd を持つ
- `internal/daemon/server.go:66` — HTTP 経由の登録
- `cmd/atct` — CLI

## 決定 2: project が登録されていないディレクトリでは NULL のまま登録を成功させる

**番兵 project を置かない。登録を失敗させない。`project_id` に NOT NULL を付けない。**

ゴール本文の起票者は③（番兵の project 行を 1 つ持つ）の検討を勧めているが、採らない。理由は 3 つ。

1. **番兵は `ensureAgentSessionProject` のスコープ違反判定を壊す。**
   この関数は 2 つの仕事をしている。(1) 空なら埋める、(2) **既に別 project なら拒否する。**
   番兵に割り当てられたセッションが後から本物の project の goal を触ると、(2) が
   「assigned=番兵 / target=本物」を検出して**エラーを返す。**これは完了条件(2)が名指ししている
   「MCP が使えなくなる壊れ方」そのものである。回避するには「番兵からの昇格だけ許す」特例が要る。
   **特例を入れた番兵は、意味としては NULL と同じである。**
2. **番兵は `ListProjects` に出る。**CLI・Web UI・役割導出の project 走査すべてに
   「どこでもない」を除外する分岐が要る。除外漏れが新しい壊れ方になる。
3. **NOT NULL 制約は、このゴールが求めているものではない。**
   完了条件は (1) 新しく登録されるセッションに project が入っている、
   (2) 未登録ディレクトリでの起動が決めたとおりに扱われる、である。**NOT NULL は書かれていない。**
   94% の NULL を解消するのは決定 1 で足りる。制約のために番兵を導入するのは、
   利得より複雑さが大きい。

①（登録を失敗させる）を採らない理由はゴール本文のとおり。`atct project add` を呼ぶ前の
セッションが登録できず、鶏と卵になる。

**したがって NULL の意味を変える。**いまの NULL は「登録時に知らなかった」である。
これからの NULL は「**解決を試みたが、この cwd はどの project にも属さない**」になる。
`ResolveProject` が `ErrProjectNotFound` を返した場合だけ NULL が残る。

## 決定 3: 既存の 2213 行は捨てない

**取り消せない操作を行わない。**捨てる動機は NOT NULL を付けるためだけであり、決定 2 で
NOT NULL を付けないので動機が消える。

数えた（完了条件(4)）。

```
$ sqlite3 -readonly ~/.atct/atct.db "select count(*) from decisions d
    join agent_sessions s on s.id=d.agent_session_id where s.project_id is null or s.project_id='';"
4
$ sqlite3 -readonly ~/.atct/atct.db "select count(*) from decisions;"
431
```

参照は 4 行だが、`4465feec`（goal 145、`decisions.agent_session_id` に FK が無い件）と
絡むため、そちらと切り離して先に捨てるのは危険である。**このゴールでは触らない。**

backfill もしない。どの project だったかの記録が無い。

## 決定 4: `deriveSessionRole` は変えない

**`deriveSessionRole`（`internal/daemon/handler.go:156`）は `project_id` 列を読んでいない。**
役割は次で決まる。

- `project.ClaimedBy == agentSessionID` なら commander
- 受領済みの goal handoff を持つなら subcommander
- それ以外は executor

**したがって「project を持たないセッション」も、いままでどおり役割が引ける。**
完了条件(5) の答えはこれである。決定 2 で NULL が残るケースがあっても、役割導出は壊れない。

ゴール本文は「役割の導出が project を見る」と書いているが、**現在のコードではそうなっていない。**
本文が並べている `deriveSessionRole` / `ProjectIDForAgentSession` / `callerProjectID` は
別々の関数であり、`deriveSessionRole` は後 2 つを呼んでいない。

## 決定 5: `40a7e4b0`（goal 156）とは根が違う

（executor の調査 D で確定させる。以下は subcommander の読みで、確定は調査結果による）

`unappliedDecisionsForSessionInProject`（`internal/daemon/handler.go:218`）は
role が `subcommander` かつ `GoalID != 0` のときだけ goal スコープに絞り、
それ以外は project 全体の未適用決定を返す。**この分岐は `project_id` を見ていない。**
`deriveSessionRole` が `project_id` に依存しない（決定 4）ので、
**`project_id` が空でも役割は引ける。**よって「project_id が空だから役割が引けない」という
ゴール本文の見立ては成り立たない。

## 未確定（executor の調査待ち）

- `ensureAgentSessionProject` の 11 箇所の棚卸し（完了条件(7)）
- `session.identify` の reattach 時に `project_id` がどうなるか
- 移行機構の書き方

## 実測で判明したこと（2026-08-27・subcommander が作業中に踏んだ）

### NULL の主因は「登録時に project を知らない」の手前にある

**同じプロセスが `run.register` を何度も呼び、そのたびに新しい行が作られている。**

```
$ sqlite3 -readonly ~/.atct/atct.db "select pid, count(*) n, min(registered_at) first_seen,
    max(registered_at) last_seen from agent_sessions group by pid order by n desc limit 3;"
pid|n|first_seen|last_seen
3376|2019|2026-08-25T09:07:39.433958Z|2026-08-27T01:00:16.381665Z
97141|124|2026-08-27T01:21:10.0678Z|2026-08-27T03:03:10.358357Z
74691|119|2026-08-25T06:06:39.036741Z|2026-08-25T09:01:01.212921Z
```

**pid 3376 が単独で 2019 行**。総 2408 行の 84% である。

```
$ sqlite3 -readonly ~/.atct/atct.db "select case when session_key='' then 'no key' else 'has key' end k,
    count(*) n, sum(case when project_id is null then 1 else 0 end) null_project
    from agent_sessions group by k;"
k|n|null_project
has key|90|48
no key|2318|2210
```

**`session_key` を持たない 2318 行のうち 2210 行（95%）が project NULL。**
`session_key` を持つ 90 行では NULL は 53% に下がる。
`atct_session_identify` を呼んだセッションだけが後から project を埋められている。

`RegisterAgentSession` は `INSERT OR IGNORE` だが `id` を指定しないため、
**呼ぶたびに新しい行になる。冪等性が効いていない。**

**決定 1 はこの構造を変えない。**cwd を渡せば新しい行にも project が入るので完了条件(1) は
満たすが、**1 プロセスから 2019 行が生まれること自体は残る。**
これは 172 の完了条件に無いので、**このゴールでは直さない。別ゴールとして人間に出す。**

### 完了条件(5)(6) の答え — 役割が引けなくなる実例を踏んだ

作業中に `atct_role` が `subcommander` から `executor` に変わった。切り分けた結果:

```
$ (socket へ直接 JSON-RPC)
role(agent_session_id=2359): {"role":"subcommander","goal_id":172,...}
role(agent_session_id=0):    {"role":"executor",...}
```

**daemon 側は正しい。**`agent_session_id=2359` を渡せば subcommander が返る。
`atct_session_identify` を呼び直したら `reattached: true` が返り、役割も復旧した。

**したがって原因はこうである。**MCP シムが再起動すると、`cmd/atct-mcp/main.go` が
`run.register` を呼んで**新しいセッション行**を作り、`agentSessionIDHolder`
（`internal/mcpshim/tools.go:209`）にその新しい ID を入れる。
新しい行は goal handoff も project claim も持たないので、
`deriveSessionRole` は **executor** を返す。`atct_session_identify` を呼ぶまで復旧しない。

**`project_id` は原因ではない。**`deriveSessionRole` は `project_id` を読んでいない（決定 4）。
**役割が引けないのは「シムが持つ ID が、identify 前の新規セッション ID だから」である。**

**よって `40a7e4b0`（goal 156）とは根が違う**（完了条件(6)）。あちらは
`unappliedDecisionsForSessionInProject` が subcommander 以外に project 全体を返す
fail-open であり、`project_id` に依存しない。**片方を直しても他方は直らない。両方要る。**

ただし**入口は共有している**: `run.register` がセッションを作るとき、cwd も session_key も
知らないこと。決定 1 で cwd を渡す経路を作るなら、同じ経路で session_key も渡せる。
**それは 172 のスコープ外なので、別ゴールとして人間に出す。**

## 決定 6: HTTP 経路は対象外。stdio だけ cwd を渡す

`run.register` を発行する本番コードは 2 箇所しかない（調査 B）。

| 発行元 | cwd を知っているか |
| --- | --- |
| `cmd/atct-mcp/main.go:57` | **知っている**（`os.Getwd()`） |
| `internal/daemon/server.go:66` | **知らない**。daemon 側の cwd であり、HTTP クライアントの cwd ではない |

HTTP MCP には client cwd を登録時に運ぶフィールドが無い。**新設すれば transport の契約変更**になる。
172 の完了条件(1) は実際に使われている stdio 経路で満たせるので、**HTTP 経路は NULL のままにする。**
決定 2 と同じ扱いである（解決できなければ NULL）。

## 決定 7: `RegisterAgentSession` の既存 signature を変えない

`RegisterAgentSession(ctx, pid)` の本番呼び出し元は `internal/daemon/handler.go:510` の 1 箇所だけだが、
**テストからの直接呼び出しが 16 ファイルにある**（調査 F）。signature を変えると
機械的な追随修正が設計判断を含む実装に混ざる。

**`RegisterAgentSessionInProject(ctx, pid, projectID)` を足し、既存
`RegisterAgentSession(ctx, pid)` は `projectID=0` で新関数へ委譲する形で残す。**
本番は新関数を使う。テストは無傷。

## 決定 8: 登録は atomic に行う。二段階にしない

`RegisterAgentSession` の後に `AssociateAgentSessionProject` を呼ぶ二段階案なら SQL も生成物も
触らずに済むが、**2 回の書き込みの間に project_id が NULL の窓が残る。**
完了条件(1) は「新しく登録されるセッションに project が入っている」なので、
**INSERT の時点で入れる。**

`internal/store/sqlcgen/` が `sqlc generate` の出力と一致していない既知の地雷があるため、
**`sqlc generate` は実行しない。**既存の `RegisterAgentSession` query を残したまま、
`RegisterAgentSessionWithProject` を新しく足し、生成物側にも対応関数を**手で足す。**
既存 query を書き換えないので、生成物の他の部分に影響しない。

## 決定 9: `ensureAgentSessionProject` の 11 箇所は 1 つも消さない

完了条件(7) の答え。調査 C の棚卸しで、**11 箇所すべてが越境拒否として生きている。**

この関数の 2 つの仕事のうち、(1) 空なら埋める は登録時に project が入れば通常経路で不要になるが、
(2) 既に別 project なら拒否する は残る。各箇所の `targetProjectID` は
`GetGoal(...).ProjectID` や `ProjectIDForTask(...)` など**RPC の入力から計算される**ので、
登録時 cwd の project と一致する保証が無い。**別 project の entity ID を渡された場合の
guard がここにしか無い。**

加えて、既存 2213 行（決定 3 で残す）と HTTP 経路の NULL 行（決定 6）は
これからも association branch を通る。**削除する根拠は無い。**

## 実装項目

1. `internal/store/queries/task.sql` — `RegisterAgentSessionWithProject` を足す
2. `internal/store/sqlcgen/task.sql.go` — 対応関数を手で足す（`sqlc generate` を実行しない）
3. `internal/store/store.go` — `RegisterAgentSessionInProject(ctx, pid, projectID)` を足し、
   既存 `RegisterAgentSession(ctx, pid)` は委譲して残す
4. `internal/daemon/handler.go` の `run.register` — `cwd` を parse し `ResolveProject` で解決。
   `ErrProjectNotFound` なら `projectID=0` で登録を成功させる。
   **`resolveOrRegisterProject` を呼ばない**（未登録 cwd に project 行を勝手に作ってしまう）
5. `cmd/atct-mcp/main.go` — `os.Getwd()` を `run.register` の payload に足す
