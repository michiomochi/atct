import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Button } from "@cloudflare/kumo/components/button";
import { useTranslation } from "react-i18next";
import { ApiError, createGoal, fetchProjects, type Project } from "../lib/api";
import { AreaLoading, ErrorState } from "./StateMessage";

interface GoalCreateFormProps {
  onCreated: () => void;
  onDirtyChange: (dirty: boolean) => void;
}

const DATA_OVERLOAD_LIMIT = 100;

export function GoalCreateForm({ onCreated, onDirtyChange }: GoalCreateFormProps) {
  const { t } = useTranslation();
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectsLoading, setProjectsLoading] = useState(true);
  const [projectsError, setProjectsError] = useState<Error | null>(null);
  const [open, setOpen] = useState(false);
  const [projectID, setProjectID] = useState("");
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
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
    const dirty = [projectID, title, description].some((value) => value.trim() !== "");
    onDirtyChange(dirty);
  }, [description, onDirtyChange, projectID, title]);

  useEffect(() => () => onDirtyChange(false), [onDirtyChange]);

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setValidationError("");
    setSubmitError(null);
    if (!projectID || !title.trim()) {
      setValidationError(t("form.goal.error.required"));
      return;
    }

    setSubmitting(true);
    try {
      await createGoal({ project_id: projectID, title, description });
      setTitle("");
      setDescription("");
      setOpen(false);
      onCreated();
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
    <div className="mt-4 border-t border-line pt-4">
      <Button
        type="button"
        className="focus-ring border border-line bg-surface px-3 py-2 text-sm text-ink hover:bg-surface-raised"
        aria-expanded={open}
        aria-controls="goal-create-form"
        onClick={() => setOpen((current) => !current)}
      >
        {open ? t("form.goal.cancel") : t("form.goal.action.new")}
      </Button>

      {open && (
        <form
          id="goal-create-form"
          className="mt-4 max-w-2xl space-y-4"
          onSubmit={handleSubmit}
          aria-busy={submitting}
        >
          {dataOverloaded && (
            <p className="text-sm text-muted" role="status">
              {t("form.goal.overload.description", { count: projects.length })}
            </p>
          )}
          <div>
            <label className="mb-1 block text-sm font-medium text-ink" htmlFor="goal-project">
              {t("form.goal.project.label")}
            </label>
            <select
              id="goal-project"
              name="project_id"
              className="focus-ring w-full border border-line bg-surface px-3 py-2 text-sm text-ink"
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
            <label className="mb-1 block text-sm font-medium text-ink" htmlFor="goal-title">
              {t("form.goal.title.label")}
            </label>
            <input
              id="goal-title"
              name="title"
              className="focus-ring w-full border border-line bg-surface px-3 py-2 text-sm text-ink"
              value={title}
              onChange={(event) => {
                setTitle(event.target.value);
                setValidationError("");
              }}
              required
            />
          </div>

          <div>
            <label className="mb-1 block text-sm font-medium text-ink" htmlFor="goal-description">
              {t("form.goal.description.label")}
            </label>
            <textarea
              id="goal-description"
              name="description"
              className="focus-ring min-h-24 w-full border border-line bg-surface px-3 py-2 text-sm text-ink"
              value={description}
              onChange={(event) => setDescription(event.target.value)}
              rows={4}
            />
          </div>

          {validationError && (
            <p className="text-sm text-danger" role="alert">
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

          <Button
            type="submit"
            className="focus-ring bg-accent-700 px-3 py-2 text-sm text-white hover:bg-accent-800 disabled:cursor-wait disabled:opacity-60"
            disabled={submitting}
          >
            {submitting ? t("form.goal.action.creating") : t("form.goal.submit")}
          </Button>
        </form>
      )}
    </div>
  );
}
