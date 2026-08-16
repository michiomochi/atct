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

  "state.loadingLabel": "{{label}}を読み込み中",
  "state.retry": "再試行",

  "duration.seconds": "{{value}}秒",
  "duration.minutes": "{{value}}分",
  "duration.hours": "{{value}}時間",
  "duration.none": "-",
};
