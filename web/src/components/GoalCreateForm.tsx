import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Button } from "@cloudflare/kumo/components/button";
import { ApiError, createGoal, fetchProjects, type Project } from "../lib/api";
import { AreaLoading, ErrorState } from "./StateMessage";

interface GoalCreateFormProps {
  onCreated: () => void;
}

const DATA_OVERLOAD_LIMIT = 100;

export function GoalCreateForm({ onCreated }: GoalCreateFormProps) {
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
      setProjectsError(reason instanceof Error ? reason : new Error("Unable to load projects."));
    } finally {
      setProjectsLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadProjects();
  }, [loadProjects]);

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setValidationError("");
    setSubmitError(null);
    if (!projectID || !title.trim()) {
      setValidationError("Select a project and enter a title.");
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
      setSubmitError(reason instanceof Error ? reason : new Error("Unable to create goal."));
    } finally {
      setSubmitting(false);
    }
  };

  if (projectsLoading) {
    return <AreaLoading label="Projects" />;
  }

  if (projectsError) {
    return <ErrorState message={projectsError.message} onRetry={() => void loadProjects()} />;
  }

  if (projects.length === 0) {
    return (
      <p className="mt-4 border-t border-line pt-4 text-sm text-muted">
        リポジトリで <code>atct project add</code> を実行して、最初のプロジェクトを登録してください。
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
        {open ? "Cancel" : "New goal"}
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
              Showing all {projects.length} registered projects. Use the selector to choose one.
            </p>
          )}
          <div>
            <label className="mb-1 block text-sm font-medium text-ink" htmlFor="goal-project">
              Project
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
                Select a project
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
              Title
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
              Description
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
              message="The project changed while this goal was being created. Reload projects and try again."
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
            {submitting ? "Creating..." : "Create goal"}
          </Button>
        </form>
      )}
    </div>
  );
}
