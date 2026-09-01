# execution-flow B1〜B4 設計決定

## 目的

`doc/execution-flow.md` の B1〜B4 を、全体フローの手順順序を変えずに整合させる。

## 決定

### B1: 人間却下は commander が再 handoff する

原則2の「自力回復」は、状態遷移で復旧可能な通常の呼び出し失敗に適用する。人間却下は外部の判断であり、手順25で handoff が閉じて subcommander の claim と役割が消えた後に起きる例外である。却下時は commander が新しい `atct_goal_handoff_request` を作り、subcommander は手順5から受領し直す。

### B2: 「最大の差」を削除する

責務移管と review 基盤は異なる変更であり、最大性を比較してもフローの正しさ・実装順・責務は決まらない。冒頭の「現状との最大の差」を削除し、両方を独立した事実として記述する。差分0の「他のすべての前提」は review 基盤の依存関係だけを意味する。

### B3: 二つの報告は別事実で、どちらも commander が書く

手順25の `atct_goal_handoff_complete` は、commander が goal handoff を受理して閉じたことの要約報告である。手順28の `atct_goal_complete` は、goals に保存する唯一の6部完成報告である。二つは同じ内容の二重入力ではない。文書は各 report の保存先・目的・書き手を区別する。

### B4: 手順25〜29に新しい待機状態を作らない

手順25後、subcommander は closed handoff・claimなし・役割なしである。pane は手順29まで生存するが、作業・状態更新・判断をしない。人間却下時だけ commander の新 handoff を受領して手順5から再開し、承認時は commander が27〜29を進めて pane を閉じる。

## 文書反映

- 原則2、claim 表、全体フロー図
- ゴール handoff の詳細、再発行の説明、人間レビュー、完了報告の理由
- 「現状との差」の差分0・4

## 非目標

- 手順1〜29の順序変更
- Goal 216、launcher/shim/monitor、ユーザー設定、dotfiles、ホーム、README、Claude/Codex manifests・skills の変更
- 現行実装の変更
