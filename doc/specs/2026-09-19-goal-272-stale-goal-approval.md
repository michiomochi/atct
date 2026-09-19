# 古くなった goal 開始承認待ちを安全に取り下げる仕様

日付: 2026-09-19
ゴール: 272

## 目的

proposed のまま長期間残り、実行記録もない agent-created goal の
goal_approval を daemon の maintenance で自動的に取り下げる。承認待ちの
放置だけを片付け、すでに着手された goal や、完了した変更を持つ goal を
自動で消さない。

## 根拠

- goal_approval の作成時刻から 14 日以上経過している。
- goals.updated_at も同じ期間更新されていない。提案文の編集や spec/plan の
  更新が最近あれば、まだ人間の判断対象として残す。
- 次の canonical record が goal に 1 件も存在しない。
  - tasks（状態は問わない）
  - task_handoffs
  - goal_handoffs
  - plan_handoffs
  - task_create_handoffs
  - goal_approval 以外の decisions

この組み合わせだけを「未着手で、記録上の進行もない」と判定する。期限だけ、
または goal の年齢だけでは取り下げない。完了済み task や完了済み handoff も
作業が存在した証拠なので、自動取り下げの除外対象である。

## 動作

### 候補の選択

既存の open な goal_approval と proposed goal を対象に、approval の作成時刻と
goal の最終更新時刻の新しい方から 14 日経過した候補だけを maintenance ごとに
調べる。人間が回答した、承認された、または goal が別の状態になった候補は
conditional update で自然に対象外になる。

### 自動取り下げ

候補に実行記録がなければ、既存の goal withdrawal の store 経路を使って 1 つの
transaction で次を行う。

1. goal を proposed から dropped にする。
2. result_summary に自動取り下げの理由を保存する。
3. open な goal_approval を withdrawn にし、同じ理由を answer_text に保存する。
4. 既存の goal.withdrawn と decision.withdrawn event、notification を発行する。

同じ transaction 内で「まだ proposed」「実行記録なし」「approval が open」を
条件にするため、maintenance の再実行は冪等である。既存の commander-only
goal.withdraw も、実行記録のない proposed goal なら同じ安全な withdrawal
経路で即時に取り下げられるようにする。記録がある proposed goal は
ErrGoalHasWork で拒否し、goal・decision・task・handoff を変更しない。active
goal の従来の withdrawal 動作は変えない。

### 判定不能な候補

task、handoff、追加 decision、または最近の goal 更新がある候補は自動処理しない。
元の goal_approval がそのまま human decision として残り、人間が approve/reject
を選ぶ。重複した「撤回するか維持するか」decision は作らない。

これは Goal 294 のように proposed 状態でも実装記録が残るケースを守る境界である。
canonical record に現れない作業を推測して撤回しないことも、この仕様の安全側の
制限とする。

## 実行場所

新しい worker や設定値は追加せず、既存 daemon の 30 秒 maintenance loop から
store の reconciliation を呼ぶ。候補がない tick は何もしない。reconciliation
の DB エラーは既存の maintenance failure 通知に載せ、次の tick で再試行する。

## 非目標

- active、done、または dropped goal の自動変更
- goal_approval に default option を付けて放置時に承認・却下すること
- task/handoff が存在する goal の age-only cleanup
- 新しい decision kind、migration、MCP tool、UI、設定項目
- worktree の filesystem 差分を canonical record の代わりに推測すること

## 受け入れ条件

1. 14 日以上更新されていない、記録なしの proposed goal が maintenance で
   dropped になり、approval が withdrawn になる。
2. goal と approval の理由、withdrawn events が記録される。
3. 14 日未満の候補は残る。
4. task が todo/doing/done/dropped のいずれでも存在する候補は残る。
5. goal/task/plan/task-create handoff、追加 decision、最近の goal 更新がある候補は
   残り、既存 approval 以外の decision を自動生成しない。
6. commander の goal.withdraw は未着手 proposed goal に使えるが、記録ありの
   proposed goalには拒否を返す。
7. active goal の既存 withdrawal 回帰テストが変わらず通る。

