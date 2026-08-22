# 通っているテストが何を保証しているかは、通っているだけでは分からない

2026-08-22 に atct で 2 件、dotfiles で 2 件、同じ形の見落としが出た。
**どれも「検証はすべて期待どおり」という報告のあとにレビューで出ている。**

## 形 1: 消しても落ちないのは、消してよい証拠ではない

`internal/store/store.go` のセッション追い出し
（`DeleteOlderProjectAgentSessions`）を消して全テストを走らせた。

    internal/store    ok
    internal/daemon   基準と同じ 2 件のみ（web/dist が無いことが原因で無関係）
    cmd/atct          ok
    cmd/atct-mcp      ok

**落ちるテストが 1 件も無かった。**

このとき「消してよい」と読むのは誤りである。正しい読みは
**「あの制約を守っているテストが存在しない」**。実際、その追い出しは
「1 プロジェクト 1 commander」を意図した実装（`61e7659`）だった。
**意図のある制約が、検査されないまま 4 日間動いていた。**

だから `0fca9e51`（追い出しを消すタスク）の検査は
**既存テストの修正ではなくゼロから書く**必要があった。依頼書にそう明記した。

## 形 2: 通っているテストが、順序に依存して偶然通っていた

`internal/daemon/server_test.go` の `TestGoalClaimRejectsLiveOtherSession` は
「同じゴールに 2 人目が入れない」を検査していて、**緑だった。**

    DELETE FROM agent_sessions WHERE project_id = ? AND id <> ? AND registered_at < ?
                                                                  ← 自分より古いものだけ消す

    既存のテスト  other を先に登録 → owner が claim → other が claim
                  other は古いので新しい owner を消せない       → 拒否が効く

    順序を逆に    owner を先に登録 → owner が claim → other が claim
                  other は新しいので古い owner を消す           → 奪えてしまう

**登録順を入れ替えるだけで通り抜ける。**executor が新しく書いた
`TestGoalClaimRejectsLiveOtherSessionAfterProjectClaim` が落ちて初めて分かった。
**executor のテストのほうが正しく、既存のテストが偶然通っていた。**

このとき executor へ「あなたの実装の誤りではない」と伝え、
`t.Skip` に理由を書かせて次のタスクへ渡した。**Skip を外すことを次のタスクの
完了条件に入れた**ので、忘れられない形になっている。

## 形 3: exit code は正しく、中身が壊れている（dotfiles 側の 2 件）

dotfiles-commander の報告（2026-08-22）:

> 私の側でも今日、executor から「検証 12 件すべて期待どおり」と報告を受けた実装に、
> レビューで欠陥が 2 件出ています。**どちらも exit code は正しく、メッセージの中身が
> 壊れていました。**

`exit 0` / `exit 2` を見るだけでは足りない。**何を印字したかを見る必要がある。**

## 形 4: 検査そのものが無かった（この日の atct 側 2 件目）

`project.claim` の daemon 側エラー差し替えを消した。

```go
if errors.Is(err, store.ErrProjectAlreadyClaimed) {
    return nil, ErrProjectAlreadyClaimed
}
```

**テストが 1 件も落ちなかった。**依頼書の検査項目に goal の拒否は書いたのに、
**project の拒否を書き忘れていた**（commander の落ち度）。
一番肝心な制約なのに、それを守る検査が無かった。

## どうするか

**1. 通ったことを報告に書くだけでは足りない。壊して落ちることを確かめる。**

このゴールでは store の 6 本を**両方向**に壊した。

    拒否を外す（false）  → 2 人目拒否のテストが落ちる
    常に拒む（true）      → 死んだセッションの奪取のテストが落ちる

**片方だけでは、どちらの誤りも通り抜ける。**検査 4 と 5 が互いを支えている。

**2. 「落ちるテストが 0 件」を見たら、まず検査の有無を疑う。**
消した記号を守るテストが本当にあるかを、名前で grep して確かめる。

**3. 依頼書の検査項目は「新しくできること」と「壊れてはいけないこと」を同じ数だけ書く。**
そして**書き落としがないかを、実装の各分岐と突き合わせる。**
形 4 は分岐（project の拒否）に対応する項目が無かった。

**4. exit code だけを検収に使わない。印字の中身を見る。**

**5. 緑のテストが順序や環境に依存していないかを疑う。**
フィクスチャを 2 件以上置き、順序を入れ替えたものを別のテストとして足す。
このゴールでは、既存テストを書き換えず**逆順のものを別に足す**方針にした
（既存が何を保証していたかの記録が消えないため）。

## 関連

- `doc/specs/2026-08-22-three-tier-orchestration.md` — 追い出しが 3 層を妨げていた経緯
- `doc/specs/2026-08-22-worktrees-inside-the-checkout.md` — 同じ日に「理由を実測で差し替えた」例
