import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { fetchTaskCommitDiff, type TaskCommit, type TaskCommitDiff } from "../lib/api";
import { AreaLoading } from "./StateMessage";

interface Props {
  taskID: string;
  commits: TaskCommit[];
}

type CommitDiffState =
  | { kind: "loading" }
  | { kind: "ready"; data: TaskCommitDiff }
  | { kind: "error"; message: string };

function errorMessage(reason: unknown, fallback: string): string {
  return reason instanceof Error ? reason.message : fallback;
}

export function TaskCommitList({ taskID, commits }: Props) {
  const { t } = useTranslation();
  const [commitDiffStates, setCommitDiffStates] = useState<Record<string, CommitDiffState>>({});
  const commitDiffStatesRef = useRef<Record<string, CommitDiffState>>({});

  const setCommitDiffState = useCallback((sha: string, next: CommitDiffState) => {
    const nextStates = { ...commitDiffStatesRef.current, [sha]: next };
    commitDiffStatesRef.current = nextStates;
    setCommitDiffStates(nextStates);
  }, []);

  const loadCommitDiff = useCallback(
    async (sha: string) => {
      if (commitDiffStatesRef.current[sha]) {
        return;
      }
      setCommitDiffState(sha, { kind: "loading" });
      try {
        const diff = await fetchTaskCommitDiff(taskID, sha);
        setCommitDiffState(sha, { kind: "ready", data: diff });
      } catch (reason) {
        setCommitDiffState(sha, { kind: "error", message: errorMessage(reason, t("goal.error.load")) });
      }
    },
    [setCommitDiffState, t, taskID],
  );

  useEffect(() => {
    commitDiffStatesRef.current = {};
    setCommitDiffStates({});
  }, [taskID]);

  if (commits.length === 0) {
    return null;
  }

  return (
    <ul className="space-y-4">
      {commits.map((commit) => {
        const diffState = commitDiffStates[commit.sha];

        return (
          <li key={commit.sha} className="min-w-0 border-t border-line pt-4 first:border-t-0 first:pt-0">
            <div className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1 text-base">
              <span className="font-mono text-[0.9em] text-ink-950">{commit.short_sha}</span>
              <span className="min-w-0 break-words text-ink-950">{commit.subject}</span>
            </div>
            <div className="mt-2 flex flex-wrap items-baseline gap-3 text-base text-ink-700">
              <span className="whitespace-nowrap">
                {commit.files_changed} {t("task.detail.commitFiles")} · +{commit.insertions} −{commit.deletions}
              </span>
              {!commit.in_history && <span className="text-accent-700">{t("task.detail.commitMissing")}</span>}
            </div>
            <details
              className="mt-4 min-w-0"
              onToggle={(event) => {
                if (event.currentTarget.open) {
                  void loadCommitDiff(commit.sha);
                }
              }}
            >
              <summary className="focus-ring cursor-pointer text-base font-medium text-accent-700 hover:text-accent-600">
                {t("task.detail.commitDiff")}
              </summary>
              {diffState?.kind === "loading" && <AreaLoading label={t("task.detail.commitDiff")} />}
              {diffState?.kind === "error" && (
                <p className="mt-4 break-words text-base text-danger-700" role="alert">
                  {diffState.message}
                </p>
              )}
              {diffState?.kind === "ready" &&
                (diffState.data.in_history ? (
                  <div className="mt-4 min-w-0 max-w-full space-y-4">
                    <ul className="min-w-0 space-y-2 text-base text-ink-700">
                      {diffState.data.files.map((file) => (
                        <li key={file.path} className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1">
                          <span className="min-w-0 break-words font-mono text-[0.9em]">{file.path}</span>
                          {file.binary ? (
                            <span className="text-ink-700">{t("task.detail.commitDiffBinary")}</span>
                          ) : (
                            <span className="whitespace-nowrap">
                              +{file.insertions} −{file.deletions}
                            </span>
                          )}
                        </li>
                      ))}
                    </ul>
                    <pre className="min-w-0 max-w-full overflow-x-auto whitespace-pre rounded border border-line bg-surface p-4 font-mono text-base leading-5 text-ink-800">
                      {diffState.data.body}
                    </pre>
                    {diffState.data.omitted_lines > 0 && (
                      <p className="text-base text-ink-700">
                        {t("task.detail.commitDiffOmitted", {
                          count: diffState.data.omitted_lines,
                        })}
                      </p>
                    )}
                  </div>
                ) : (
                  <p className="mt-4 text-base text-ink-700">{t("task.detail.commitDiffEmpty")}</p>
                ))}
            </details>
          </li>
        );
      })}
    </ul>
  );
}
