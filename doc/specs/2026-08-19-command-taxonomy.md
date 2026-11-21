# エージェント側と daemon 側でコマンドを分ける

日付: 2026-08-19
ゴール: 回答が AI エージェントに確実に届くようにする（コマンド体系の整理）

## 解く問題

`atct stop`（CLI・daemon を止める）と `atct:stop`（スキル・Monitor を止める）は
名前がコロン 1 つ違いで、止まるものが違う。

今日この混同を避けるために、README・`atct:start`・`atct:stop` の 3 箇所へ
「どちらが何を止めるか」を書いた。**説明を 3 箇所に書かないと使えない設計は、
名前が間違っている。**

## 決定

**スキルはエージェント側、CLI は daemon 側**の二層で切る。

| コマンド | 層 | すること |
|---|---|---|
| `/atct:start` | スキル | エージェントの作業開始。daemon を起動（動いていなければ）。watch を張る |
| `/atct:stop` | スキル | エージェント側の停止。watch を止める。**daemon には触らない** |
| `atct daemon start` | CLI | daemon を起動する（現 `atct ensure`） |
| `atct daemon stop` | CLI | daemon を停止する（現 `atct stop`） |

これで「止める」対象が名前から読める。**README とスキルに書いた「違いの説明」は削除する。**

人間が覚えるのはこの 4 つだけにする。

### 前面実行は内部専用の入口として残す

`atct daemon`（引数なし）は前面実行のままにするが、**使い方の一覧に出さない。**

理由は実装である。`Ensure` は自分自身を `exec.Command(executable, "daemon")` として
`setsid` で起動する。**前面実行の入口は `atct daemon start` の実装が依存している経路**で、
消せない。ログは `cmd.Stdout` でファイルに向いているので、人間が前面で動かす理由も無い。

これにより改名の影響は `ensure` と `stop` の 2 つに縮み、`Ensure` の内部呼び出しは
書き換えずに済む。

### 廃止するもの

- `atct ensure` → `atct daemon start`
- `atct stop` → `atct daemon stop`

**廃止したコマンドは黙って旧挙動を続けない。** エラーを返し、新しい名前を案内する。
新体系では `atct stop` が「エージェント側の停止」を意味するので、それが daemon を
止め続けるのは意味が食い違う。**動くが意味が違うのは、動かないより悪い。**

### 影響範囲（実測）

| 対象 | 件数 | 扱い |
|---|---|---|
| エラーメッセージ内の `run atct stop first` | 3 | 新しい名前へ |
| README | 3 箇所 | 1 つは説明ごと削除 |
| `plugin/skills/{start,stop}/SKILL.md` | 各 1 | 説明ごと削除 |
| **hook** | **0** | `context` しか使っていない。リリース済み hook は無傷 |
| `ensure` の呼び出し元 | 0 | `main.go` の定義以外に無い |

hook が無傷なので、既存プラグインを壊さずに移行できる。

## 検証

- `atct daemon start` / `stop` が従来の `ensure` / `stop` と同じ結果になること
- **`atct daemon start` が実際に daemon を起こせること**（内部の前面実行の入口を
  壊していないことの確認。ここが壊れると起動そのものが死ぬ）
- `atct stop` と `atct ensure` がエラーになり、**新しい名前を案内すること**
- 使い方の表示に前面実行の入口が出ないこと
- `tests/wrapper_test.bash` が通ること（ラッパー経由の終了コードが固定されている）

実 HOME（`~/.atct`）では検証しない。一時ディレクトリを使う。
