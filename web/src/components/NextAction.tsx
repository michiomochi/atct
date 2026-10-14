import { useTranslation } from "react-i18next";
import type { NextAction as NextActionValue } from "../lib/api";
import type { TranslationKey } from "../i18n/en";

interface Props {
  action?: NextActionValue;
}

// Keys are literal so the i18n reference test can find each one in this file.
const actorKeys: Partial<Record<string, TranslationKey>> = {
  human: "nextAction.actor.human",
  commander: "nextAction.actor.commander",
  subcommander: "nextAction.actor.subcommander",
  executor: "nextAction.actor.executor",
};

const codeKeys: Partial<Record<string, TranslationKey>> = {
  done: "nextAction.code.done",
  withdrawn: "nextAction.code.withdrawn",
  human_review: "nextAction.code.human_review",
  human_decision: "nextAction.code.human_decision",
  commander_complete: "nextAction.code.commander_complete",
  commander_reject_handoff: "nextAction.code.commander_reject_handoff",
  commander_request_handoff: "nextAction.code.commander_request_handoff",
  subcommander_receive_handoff: "nextAction.code.subcommander_receive_handoff",
  subcommander_receive_rejection: "nextAction.code.subcommander_receive_rejection",
  commander_receive_goal_review: "nextAction.code.commander_receive_goal_review",
  commander_review_goal: "nextAction.code.commander_review_goal",
  subcommander_plan: "nextAction.code.subcommander_plan",
  subcommander_receive_plan_rejection: "nextAction.code.subcommander_receive_plan_rejection",
  commander_receive_plan_review: "nextAction.code.commander_receive_plan_review",
  commander_review_plan: "nextAction.code.commander_review_plan",
  subcommander_receive_task_create: "nextAction.code.subcommander_receive_task_create",
  subcommander_create_tasks: "nextAction.code.subcommander_create_tasks",
  subcommander_review_task: "nextAction.code.subcommander_review_task",
  subcommander_delegate_task: "nextAction.code.subcommander_delegate_task",
  executor_receive_task: "nextAction.code.executor_receive_task",
  executor_implement: "nextAction.code.executor_implement",
  subcommander_request_goal_review: "nextAction.code.subcommander_request_goal_review",
};

// Read-only: shows the step the goal waits on. No control to act on it.
export function NextAction({ action }: Props) {
  const { t } = useTranslation();
  if (!action?.code) return null;

  const actorKey = actorKeys[action.actor];
  const codeKey = codeKeys[action.code];
  return (
    <span>
      {actorKey && `${t(actorKey)} · `}
      {codeKey ? t(codeKey, { id: action.decision_id }) : action.code}
      {action.task_count !== undefined && ` (${t("nextAction.taskCount", { count: action.task_count })})`}
    </span>
  );
}
