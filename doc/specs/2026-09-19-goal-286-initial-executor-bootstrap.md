# Goal 286: 空の goal space で初回 executor pane が作れない

## 問題

task handoff を委譲する subcommander が、空の goal space にいるときも「既存の
executor を再利用し、新しい pane は例外時だけ作る」と読める契約に従うと、
最初の executor pane を作らず handoff だけが未受領で残る。handoff は記録されるが、
受領する worker が起動されないため、通常の request → receive 経路へ戻れない。

## 根拠

- 現在の main は `9f519a8`。その `skills/subcommander/SKILL.md` は idle executor の
  再利用と追加 pane の条件を定めるが、space に executor が 0 台の場合の bootstrap
  を明記していない。
- `doc/execution-flow.md` は task handoff の順序を示すが、最初の executor pane を
  用意する分岐を示していない。
- `orchestration` skill は pane/space の配置方法を扱い、最初の pane を禁止していない。
- daemon は handoff の request/receive を記録できるが、terminal pane を作成できない。
  未受領 handoff の liveness guard は Goal 292 の範囲であり、この goal の修正対象ではない。

## 決定

canonical な subcommander の task-delegation guidance に、executor workspace の状態を
先に分ける明示的な bootstrap 規則を追加する。goal space に executor pane が無ければ、
同じ space に最初の 1 台を用意してから、既存の順序どおり handoff request → monitored
worker 起動 → worker の receive を進める。idle executor が存在する場合はそれを再利用し、
2 台目以降の pane は既存の parallel work、worktree isolation、context exhaustion、topic
change の条件に限る。

「新しい pane は例外時だけ」という制限は、executor がすでに存在する space への追加
pane にだけ適用する。初回 pane の作成をその制限に含めないことで、空の space が task
handoff を抱えたまま停止する読み替えをなくす。

実装は `skills/subcommander/SKILL.md` と `doc/execution-flow.md` の文面だけに置く。
daemon guard、MCP schema、`skills/atct/SKILL.md`、`tests/wrapper_test.bash` は変更しない。

## 受け入れ条件

1. subcommander skill が、executor 0 台の space では最初の pane を作ることを明記している。
2. 同 skill が、初回 pane と追加 pane の条件を区別している。
3. handoff request 前に worker を起動しない既存の順序が維持されている。
4. execution flow が、同じ space での初回 pane 準備と、その後の handoff request・monitor
   起動・receive の関係を矛盾なく説明している。
5. 既存の skill contract 検証が実行でき、変更は whitespace/error を含まない。

## 境界と確認事項

- Goal 284 が `skills/atct/SKILL.md` と `tests/wrapper_test.bash` を所有しているため、
  その human-approved main integration までは同一ファイルに実装 task を開始しない。
- Goal 292 が `RequestedAt != nil && ReceivedAt == nil` の runtime guard を所有する。
  その guard と今回の guidance は補完関係にあり、同じ guard を重複実装しない。
- Goal 290 の token-compression work と `internal/mcpshim/instructions.go` には触れない。
