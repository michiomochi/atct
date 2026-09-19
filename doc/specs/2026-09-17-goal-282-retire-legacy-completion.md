# Goal 282: legacy completion compatibility を廃止する

基準ブランチは、現在の `main` / `v0.63.11` (`5c726f9`) に rebase 済みである。

## 決定

`KindCompletion` を残す段階的 deprecation ではなく、live consumer まで含めて一括で撤去する。

現状の `KindCompletion` は domain/store だけの型ではない。daemon の RPC、HTTP の approve/reject、wakeup、CLI の pending 表示、E2E fixture、Web UI がそれを参照しているため、旧 Task 1 のように approved file boundary 内だけで削除するとコンパイルできない。段階的 deprecation は旧 completion authority と分岐を次のゴールへ残すことになる。

一括撤去なら、名前付き goal-review を唯一の完了 authority にしたまま、旧 public entry point は fail-closed の移行案内としてだけ残せる。結果として変更は広がるが、同じ移行を二度実施せず、旧経路から goal を完了できる穴も残さない。

## 正規 lifecycle

1. subcommander が `atct_goal_handoff_review_request` で commander review を依頼する。
2. commander が `atct_goal_review_request` で human review を開く。
3. approval 後に commander が merge し、`atct_goal_review_complete` で finalize する。
4. rejection は同じ goal handoff の reject/receive/resubmit lifecycle に戻る。

`atct_goal_complete` / RPC `goal.complete` は decision を作らず、`atct_goal_review_request` と `atct_goal_review_complete` を案内する stable diagnostic を返す。既存の入力 schema と登録名は transport migration のため維持する。

## 撤去する authority と維持する compatibility

- `domain.KindCompletion`、completion 専用の Store adapter/query、HTTP の completion approve/reject branch、completion 専用 wakeup/UI action を削除する。
- `CompletionReport` と goal の report fields は named goal-review request/finalize が使うため維持する。
- `decisions.kind` の historical string は audit history として保持する。open row だけを migration で `withdrawn` にし、closed row・goal status・goal report は変更しない。
- generic HTTP decision endpoint と historical decision の read-only 表示は維持する。ただし withdrawn/closed の legacy completion に action を表示しない。
- task/goal handoff completion は nonzero `agent_session_id`、reviewer role、handoff ownership を必須にする。session 0 は Store fallback を呼ばず、`atct_session_identify` と named handoff review を案内する。
- migration は data-only のため fresh schema `schema.sql` は変更しない。main の `0044_fixed_width_timestamps.sql` に続けて `0045_retire_legacy_completion.sql` を追加する。

## 受入条件

- `KindCompletion` を作成・approve・reject して goal を完了する live code path がない。
- pre-upgrade の open `completion` decision は `0045` 適用後に `withdrawn` となり、理由と `answered_at` が保存される。answered/applied/closed row は変更されない。
- `atct_goal_complete` / `goal.complete` は mutation せず、named goal-review の二段階を示す stable diagnostic を返す。
- session 0 の task/goal handoff completion は mutation せず、identity-required diagnostic を返す。nonzero reviewer completion は維持される。
- goal-review の request、human approve、merge 後 finalize、reject、same-handoff resubmission が通る。
- pending/wakeup、MCP instruction、skills、Web UI に `atct_goal_complete` を正規完了手順として案内する箇所、または completion approve/reject card が残らない。

## 非対象

- historical `completion` string の削除、decisions table の rebuild、既存 closed history の書き換え。
- named goal-review が使う report fields の削除。
- goal 282 と無関係な no-session compatibility の変更。
