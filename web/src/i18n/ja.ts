import type { TranslationKey } from "./en";

export const ja: Record<TranslationKey, string> = {
  "app.name": "ATCT",
  "nav.inbox": "受信箱",
  "locale.label": "言語",
  "locale.en": "English",
  "locale.ja": "日本語",

  "inbox.eyebrow": "受信箱",
  "inbox.title": "受信箱",
  "inbox.description":
    "回答待ちの判断、回答済みでまだ適用されていない判断、対応が必要なタスク、進行中のゴールを確認します。",
  "inbox.openDecisions.title": "回答待ちの判断",
  "inbox.openDecisions.empty": "回答待ちの判断はありません。判断に答えると先へ進みます。",
  "inbox.unapplied.title": "回答済み・未適用の判断",
  "inbox.unapplied.empty":
    "適用待ちの回答はありません。エージェントが受け取ると適用されます。",
  "inbox.attention.title": "対応が必要なタスク",
  "inbox.attention.empty":
    "未解決の判断に関係するタスクはありません。判断が必要になるとここに表示されます。",
  "inbox.activeGoals.title": "進行中のゴール",
  "inbox.activeGoals.empty":
    "進行中のゴールはありません。ゴールを再開するとここに表示されます。",

  "inbox.error.load": "受信箱を読み込めませんでした。",

  "decision.caption.list": "判断一覧",
  "decision.column.question": "質問",
  "decision.column.status": "状態",
  "decision.column.answer": "回答",
  "decision.column.answeredBy": "回答者",
  "decision.column.goal": "ゴール",
  "decision.column.createdAt": "作成日時",

  "goal.caption.activeList": "進行中のゴール一覧",
  "goal.column.goal": "ゴール",
  "goal.column.status": "状態",
  "goal.column.updatedAt": "更新日時",

  "task.caption.attention": "対応が必要なタスク一覧",
  "task.column.task": "タスク",
  "task.column.goal": "ゴール",
  "task.column.status": "状態",
  "task.column.claimedBy": "保持者",
  "task.column.claimDuration": "保持時間",
  "task.claim.noHolder": "未保持",

  "form.goal.project.label": "プロジェクト",
  "form.goal.title.label": "タイトル",
  "form.goal.description.label": "説明",
  "form.goal.project.placeholder": "プロジェクトを選択",
  "form.goal.submit": "ゴールを作成",
  "form.goal.cancel": "キャンセル",
  "form.goal.action.new": "新しいゴール",
  "form.goal.action.creating": "作成中...",
  "form.goal.noProject":
    "リポジトリで atct project add を実行して、最初のプロジェクトを登録してください。",
  "form.goal.overload.description":
    "登録済みのプロジェクトを{{count}}件すべて表示しています。セレクターで1つ選択してください。",
  "form.goal.error.load": "プロジェクトを読み込めませんでした。",
  "form.goal.error.create": "ゴールを作成できませんでした。",
  "form.goal.error.required": "プロジェクトを選択してタイトルを入力してください。",
  "form.goal.error.conflict":
    "このゴールの作成中にプロジェクトが変更されました。プロジェクトを再読み込みしてもう一度試してください。",

  "state.loadingLabel": "{{label}}を読み込み中",
  "state.retry": "再試行",

  "duration.seconds": "{{value}}秒",
  "duration.minutes": "{{value}}分",
  "duration.hours": "{{value}}時間",
  "duration.none": "-",
};
