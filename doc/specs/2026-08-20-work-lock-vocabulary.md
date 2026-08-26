# 「作業ロック」の語彙と、コード上の名前との対応

日付: 2026-08-20
ゴール: claim を外したタスクが doing のまま残り、どの検知にも出ない（goal 45）

## 発端

人間が完了報告を却下し、理由が 1 行だった。

> release は作業ロックの解除という概念にして。

**このプロダクトで `release` が 2 つの意味に使われていた。**

| 語 | 意味 |
|---|---|
| リリース | 版を出す（`script/release.sh`・goreleaser・GitHub の release） |
| release | タスクの claim を外す |

さらに画面の日本語が **「保持を解放」** で、**何の保持か読めなかった。**

## 決定: 人間が読む言葉だけを変える

| 変えた | 変えない |
|---|---|
| 画面の文言（ja / en） | API の経路 `/api/tasks/<id>/release` |
| `pending` の理由文 9 種 | 関数名（`ClaimTask`・`ReleaseTask`） |
| store の拒否メッセージ 3 種 | DB の列名（`claimed_by`・`claimed_at`） |

**概念は人間が読む言葉に宿る。** 列名の移行はリスクだけが増えて、読みやすさは変わらない。

## 対応表（両方を読む人のために）

| 人間が見る語 | 英語 | コード上の名前 |
|---|---|---|
| 作業ロックを取得 | acquire the work lock | `atct_task_claim` / `ClaimTask` / `claimed_by` |
| 作業ロックを解除 | release the work lock | `POST /api/tasks/<id>/release` / `ReleaseTask` |
| ロック保持者 | lock holder | `claimed_by` / `task.claim.noHolder` |
| 作業ロック（列見出し） | Work lock | `task.column.claim` |

**「保持」「解放」を単独で使わない。** 何の保持か読めないのが指摘の中身だった。

## 解除が `status` を `todo` に戻す理由

**ロックの模型では「ロックが無いのに doing」は意味を持たない。**
解除したら、その作業は未着手の状態に戻る。

これは同日の別の決定（`decision 88`）で決めた挙動であり、**語彙の変更で挙動は変えていない。**
むしろ語彙を揃えたことで、挙動の説明が短くなった。

## 実測（2026-08-20、v0.26.0 で稼働確認）

画面:

```
ボタン: 作業ロックを解除 / Release the work lock
列見出し: 作業ロック / Work lock
```

`pending`:

```
A task with a work lock held by this agent session is still open. ...
Unfinished tasks with work locks:
```

**稼働中の daemon で確認した**（手元のビルドではなく）。

## 副産物: 参照されていない i18n キーが 2 つ見つかった

    task.column.claimedBy      参照なし
    task.column.claimDuration  参照なし

同日削除した `AttentionTaskTable` の残りだった。**両方の locale から消した。**
逆に `task.claim.noHolder` は `TaskTable.tsx` が参照しているのに**どちらの locale にも
無く、キー名がそのままラベルとして描画されていた。** 足した。

**未参照コンポーネントの検査は入れたが、未参照 i18n キーの検査は入れていない。**
同じ形の残骸がまた出る。
