import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Button } from "@cloudflare/kumo/components/button";
import { Dialog } from "@cloudflare/kumo/components/dialog";
import { useTranslation } from "react-i18next";
import { ApiError, createGoal, fetchProjects, type Project } from "../lib/api";
import { AreaLoading, ErrorState } from "./StateMessage";

const DATA_OVERLOAD_LIMIT = 100;

interface Props {
  onCreated?: () => void;
  onDirtyChange?: (dirty: boolean) => void;
}

export function GoalCreateForm({ onCreated, onDirtyChange }: Props = {}) {
  const { t } = useTranslation();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectsLoading, setProjectsLoading] = useState(true);
  const [projectsError, setProjectsError] = useState<Error | null>(null);
  const [open, setOpen] = useState(false);
  const [projectID, setProjectID] = useState("");
  const [content, setContent] = useState("");
  const [validationError, setValidationError] = useState("");
  const [submitError, setSubmitError] = useState<Error | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const loadProjects = useCallback(async () => {
    setProjectsLoading(true);
    setProjectsError(null);
    try {
      const nextProjects = await fetchProjects();
      setProjects(nextProjects);
      setProjectID((current) =>
        nextProjects.some((project) => project.id === current) ? current : "",
      );
    } catch (reason) {
      setProjectsError(reason instanceof Error ? reason : new Error(t("form.goal.error.load")));
    } finally {
      setProjectsLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void loadProjects();
  }, [loadProjects]);

  useEffect(() => {
    const dirty = [projectID, content].some((value) => value.trim() !== "");
    onDirtyChange?.(open && dirty);
  }, [content, onDirtyChange, open, projectID]);

  useEffect(() => () => onDirtyChange?.(false), [onDirtyChange]);

  const closeDialog = () => {
    setOpen(false);
    onDirtyChange?.(false);
  };

  const handleDialogOpenChange = (nextOpen: boolean) => {
    setOpen(nextOpen);
    if (!nextOpen) {
      onDirtyChange?.(false);
    }
  };

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setValidationError("");
    setSubmitError(null);
    if (!projectID || !content.trim()) {
      setValidationError(t("form.goal.error.required"));
      return;
    }

    setSubmitting(true);
    try {
      await createGoal({ project_id: projectID, content, creator: "human" });
      setContent("");
      closeDialog();
      onCreated?.();
    } catch (reason) {
      setSubmitError(reason instanceof Error ? reason : new Error(t("form.goal.error.create")));
    } finally {
      setSubmitting(false);
    }
  };

  if (projectsLoading) {
    return <AreaLoading label={t("form.goal.project.label")} />;
  }

  if (projectsError) {
    return <ErrorState message={projectsError.message} onRetry={() => void loadProjects()} />;
  }

  if (projects.length === 0) {
    return (
      <p className="mt-4 border-t border-line pt-4 text-sm text-muted">
        {t("form.goal.noProject")}
      </p>
    );
  }

  const dataOverloaded = projects.length > DATA_OVERLOAD_LIMIT;

  return (
    <Dialog.Root open={open} onOpenChange={handleDialogOpenChange}>
      <Dialog.Trigger
        render={(triggerProps) => (
          <Button
            {...triggerProps}
            type="button"
            className="focus-ring border border-line bg-surface px-3 py-2 text-sm text-ink-950 hover:bg-accent-100"
          >
            {t("form.goal.action.new")}
          </Button>
        )}
      />
      <Dialog className="p-6">
        <Dialog.Title className="mb-4 font-display text-xl font-semibold text-ink-950">
          {t("form.goal.action.new")}
        </Dialog.Title>
        <form className="space-y-4" onSubmit={handleSubmit} aria-busy={submitting}>
          {dataOverloaded && (
            <p className="text-sm text-muted" role="status">
              {t("form.goal.overload.description", { count: projects.length })}
            </p>
          )}
          <div>
            <label className="mb-1 block text-sm font-medium text-ink-950" htmlFor="goal-project">
              {t("form.goal.project.label")}
            </label>
            <select
              id="goal-project"
              name="project_id"
              className="focus-ring w-full border border-line bg-surface px-3 py-2 text-sm text-ink-950"
              value={projectID}
              onChange={(event) => {
                setProjectID(event.target.value);
                setValidationError("");
              }}
              required
            >
              <option value="" disabled>
                {t("form.goal.project.placeholder")}
              </option>
              {projects.map((project) => (
                <option key={project.id} value={project.id}>
                  {project.name}
                </option>
              ))}
            </select>
          </div>

          <div>
            <label className="mb-1 block text-sm font-medium text-ink-950" htmlFor="goal-content">
              {t("form.goal.content.label")}
            </label>
            <textarea
              id="goal-content"
              name="content"
              className="focus-ring min-h-24 w-full border border-line bg-surface px-3 py-2 text-sm text-ink-950"
              value={content}
              onChange={(event) => {
                setContent(event.target.value);
                setValidationError("");
              }}
              rows={5}
              required
            />
          </div>

          {validationError && (
            <p className="text-sm text-danger-700" role="alert">
              {validationError}
            </p>
          )}
          {submitError && submitError instanceof ApiError && submitError.status === 409 ? (
            <ErrorState
              message={t("form.goal.error.conflict")}
              onRetry={() => {
                setSubmitError(null);
                void loadProjects();
              }}
            />
          ) : submitError ? (
            <ErrorState message={submitError.message} onRetry={() => setSubmitError(null)} />
          ) : null}

          <div className="flex gap-3">
            <Button
              type="submit"
              className="focus-ring bg-accent-700 px-3 py-2 text-sm text-white hover:bg-accent-600 disabled:cursor-wait disabled:opacity-60"
              disabled={submitting}
            >
              {submitting ? t("form.goal.action.creating") : t("form.goal.submit")}
            </Button>
            <Dialog.Close
              render={(closeProps) => (
                <Button
                  {...closeProps}
                  type="button"
                  className="focus-ring border border-line bg-surface px-3 py-2 text-sm text-ink-950 hover:bg-accent-100"
                >
                  {t("form.goal.cancel")}
                </Button>
              )}
            />
          </div>
        </form>
      </Dialog>
    </Dialog.Root>
  );
}
