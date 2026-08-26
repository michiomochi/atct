# 39fa8e55 の設計（atct-b01a92b8-subcommander、2026-08-26）

**実装は 0 行。**以下はタスク description に足す想定の本文。

## 現状（HEAD e7871e3 で確認。行番号ではなく関数名で指す）

`internal/daemon/wakeup.go` の `runMaintenance` に、握り潰しが **2 つ**ある。

    _, _ = d.store.ApplyExpiredDefaults(ctx, now)   ← 1 つ目。戻り値を捨てている
    d.store.PublishEvent(keepalive)
    events, err := tracker.evaluate(ctx, d.store, now)
    if err != nil { return }                        ← 2 つ目。タスク本文が指しているもの

**タスク本文の `return` は 1 行も変わっていない。**「未着手のものがある」は文字どおり。
**1 つ目は本文に無い分**なので、直すなら報告に明記する。片側だけ直すと同じ形が残る。

## 決定: ログと SSE の両方に出す

完了条件は「どちらかを決めて理由を書く」だが、**引いた結果、片方では穴が残る。**

    SSE だけ   その瞬間に Monitor が張られていなければ購読者がいない → 消える。いまと同じ
    ログだけ   ~/.atct/daemon.log を通常のループで読む者がいない → 誰も気づかない。いまと同じ

**両方入れる理由は、互いの盲点を埋めるからである。**ログは購読者が要らず残り、SSE は読む人に届く。

`daemon.log` は `internal/daemonctl` が daemon の stdout/stderr を受けており、
標準の `log` パッケージがそこへ出る（既存の "atct daemon listening on ..." が同じ経路）。

## 罠: SSE を足すだけでは Monitor に出ない

`cmd/atct/watch.go` の `formatWatchDecision` は、知らないイベント名に対して

    line, ok := formatWatchDecision(eventName, decision)
    if !ok { return nil }        // 黙って捨てる

を返す。**整形を足さないと、失敗イベントは Monitor に着かない。**
**修正そのものが、直そうとしている沈黙に落ちる形である。**

## 触る必要のあるファイル

    宣言済み   internal/daemon/wakeup.go
    宣言外     cmd/atct/watch.go            整形。無いと SSE が届かない
               internal/daemon/wakeup_test.go
               cmd/atct/watch_test.go

**`files` は宣言後に足せない**（d98183b6 の主題）ので、報告に明記する。

## 完了条件（本文のまま）と、検査の作り方

(1) 評価が失敗したことが外から分かる (2) 成功時の振る舞いは変わらない
(3) 両方を検査で固める。**評価が失敗する状態を作って、黙らないことを示す。**

`tracker.evaluate` を失敗させる形は、下の DB を閉じてから `runMaintenance` を呼ぶのが素直。
**「失敗しても何も起きない」でも (2) は通る**ので、(1) の肯定側を必ず書く。

## 検証は executor に打たせられない

`internal/daemon` のテストは `daemon.Serve` が unix socket を作る。
**Codex の sandbox は bind を拒む**（2026-08-26 の実測: `bind: operation not permitted`）。

    executor      socket を使わない検査だけ。-run に渡す名前は -v の === RUN で実在を確認してから
    subcommander  go test ./internal/daemon -count=1 を自分で打つ。報告に載せるのはこちら

**「sandbox が拒んだ」をテスト側で許容に変えないこと。**測定の不在が緑に化ける。

## 主が立たないときの測り方（2026-08-26 に実測）

    git rev-parse --short HEAD
    git archive HEAD | tar -x -C /private/tmp/atct-<goal8>-verify
    rsync -a web/dist/ /private/tmp/atct-<goal8>-verify/web/dist/    ← **必須。無いと埋め込みが落ちる**
    cd /private/tmp/atct-<goal8>-verify && GOCACHE=/private/tmp/atct-<goal8>-gocache go test ./internal/daemon -count=1

`web/dist` を写さないと `TestHTTPHandlerServesEmbeddedIndex` が
`response does not contain embedded HTML` で落ちる。**文言は自分の変更の失敗に見える。**
worktree を使う場合も同じ（`dist` は git 管理外）。

**この形なら .git に書かず、ブランチも作らず、正本の二重化も起きない**
（複製は使い捨てで、測る直前に取り直す）。

## 前提の確認（2026-08-26 時点）

4 ファイルとも、宣言（自分のタスクを除く）と `git status` の両方で空きだった。
