import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  fetchGoalDiff,
  fetchGoalDiffPatch,
  type GoalDiff as GoalDiffData,
  type GoalDiffPatch,
} from "../lib/api";
import { AreaLoading } from "./StateMessage";
import "@git-diff-view/react/styles/diff-view.css";

interface Props {
  goalID: string;
}

type LoadState =
  | { kind: "loading" }
  | { kind: "ready"; data: GoalDiffData }
  | { kind: "error"; message: string };

type PatchState =
  | { kind: "loading" }
  | { kind: "ready"; data: GoalDiffPatch }
  | { kind: "error"; message: string };

interface DiffViewProps {
  data: { hunks: string[] };
}

const DiffView = lazy(() =>
  import("@git-diff-view/react").then(({ DiffView: Component, DiffModeEnum }) => ({
    default: ({ data }: DiffViewProps) => (
      <Component data={data} diffViewMode={DiffModeEnum.Unified} diffViewWrap diffViewHighlight={false} />
    ),
  })),
);

function errorMessage(reason: unknown, fallback: string): string {
  return reason instanceof Error ? reason.message : fallback;
}

export function GoalDiff({ goalID }: Props) {
  const { t } = useTranslation();
  const [state, setState] = useState<LoadState>({ kind: "loading" });
  const [patchStates, setPatchStates] = useState<Record<string, PatchState>>({});
  const patchStatesRef = useRef<Record<string, PatchState>>({});

  const setPatchState = useCallback((path: string, next: PatchState) => {
    const nextStates = { ...patchStatesRef.current, [path]: next };
    patchStatesRef.current = nextStates;
    setPatchStates(nextStates);
  }, []);

  const loadPatch = useCallback(
    async (path: string) => {
      if (patchStatesRef.current[path]) {
        return;
      }
      setPatchState(path, { kind: "loading" });
      try {
        const patch = await fetchGoalDiffPatch(goalID, path);
        if (!patch) {
          throw new Error(t("goal.diff.error"));
        }
        setPatchState(path, { kind: "ready", data: patch });
      } catch (reason) {
        setPatchState(path, { kind: "error", message: errorMessage(reason, t("goal.diff.error")) });
      }
    },
    [goalID, setPatchState, t],
  );

  useEffect(() => {
    let cancelled = false;
    patchStatesRef.current = {};
    setPatchStates({});
    setState({ kind: "loading" });

    void fetchGoalDiff(goalID)
      .then((data) => {
        if (!data) {
          throw new Error(t("goal.diff.error"));
        }
        if (!cancelled) {
          setState({ kind: "ready", data });
        }
      })
      .catch((reason) => {
        if (!cancelled) {
          setState({ kind: "error", message: errorMessage(reason, t("goal.diff.error")) });
        }
      });

    return () => {
      cancelled = true;
    };
  }, [goalID, t]);

  if (state.kind === "ready" && !state.data.available && state.data.reason !== "timeout") {
    return null;
  }

  return (
    <section className="min-w-0 border-t border-line pt-5" data-testid="goal-diff" aria-labelledby="goal-diff-heading">
      <h2 id="goal-diff-heading" className="font-display text-lg font-semibold text-ink-950">
        {t("goal.diff.title")}
      </h2>

      {state.kind === "loading" && <div className="mt-4"><AreaLoading label={t("goal.diff.title")} /></div>}
      {state.kind === "error" && (
        <p className="mt-4 break-words text-base text-danger-700" role="alert">
          {state.message}
        </p>
      )}
      {state.kind === "ready" && !state.data.available && state.data.reason === "timeout" && (
        <p className="mt-4 text-base text-ink-700">{t("goal.diff.unknown")}</p>
      )}
      {state.kind === "ready" && state.data.available && (
        <>
          <div className="mt-4 flex flex-wrap gap-x-4 gap-y-1 text-base text-ink-700">
            <span>
              {t("goal.diff.filesChanged")}: <strong data-testid="goal-diff-files-changed">{state.data.files_changed}</strong>
            </span>
            <span>
              {t("goal.diff.insertions")}: <strong data-testid="goal-diff-insertions">+{state.data.insertions}</strong>
            </span>
            <span>
              {t("goal.diff.deletions")}: <strong data-testid="goal-diff-deletions">−{state.data.deletions}</strong>
            </span>
          </div>

          {state.data.files.length === 0 ? (
            <p className="mt-4 text-base text-ink-700">{t("goal.diff.empty")}</p>
          ) : (
            <ul className="mt-4 min-w-0 space-y-3">
              {state.data.files.map((file) => {
                const patchState = patchStates[file.path];

                return (
                  <li key={file.path} className="min-w-0">
                    <details
                      className="min-w-0"
                      onToggle={(event) => {
                        if (event.currentTarget.open) {
                          void loadPatch(file.path);
                        }
                      }}
                    >
                      <summary className="focus-ring cursor-pointer text-base font-medium text-accent-700 hover:text-accent-600">
                        <span className="break-words font-mono">{file.path}</span>
                        <span className="ml-3 whitespace-nowrap text-ink-700">
                          {file.binary
                            ? t("goal.diff.binary")
                            : `+${file.insertions} −${file.deletions}`}
                        </span>
                      </summary>

                      {patchState?.kind === "loading" && (
                        <div className="mt-4"><AreaLoading label={t("goal.diff.loading")} /></div>
                      )}
                      {patchState?.kind === "error" && (
                        <p className="mt-4 break-words text-base text-danger-700" role="alert">
                          {patchState.message}
                        </p>
                      )}
                      {patchState?.kind === "ready" && patchState.data.available && patchState.data.patch !== "" && (
                        <div className="mt-4 min-w-0 max-w-full overflow-x-auto">
                          <Suspense fallback={<AreaLoading label={t("goal.diff.loading")} />}>
                            <DiffView data={{ hunks: [patchState.data.patch] }} />
                          </Suspense>
                        </div>
                      )}
                      {patchState?.kind === "ready" && patchState.data.available && patchState.data.patch === "" &&
                        patchState.data.omitted_lines > 0 && (
                          <p className="mt-4 text-base text-ink-700">
                            {t("goal.diff.omitted", { count: patchState.data.omitted_lines })}
                          </p>
                        )}
                      {patchState?.kind === "ready" && !patchState.data.available && patchState.data.reason === "timeout" && (
                        <p className="mt-4 text-base text-ink-700">{t("goal.diff.unknown")}</p>
                      )}
                    </details>
                  </li>
                );
              })}
            </ul>
          )}
        </>
      )}
    </section>
  );
}
