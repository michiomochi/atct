import { Button } from "@cloudflare/kumo/components/button";
import { Table } from "@cloudflare/kumo/components/table";
import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { archiveProject, fetchProjects, unarchiveProject, type Project } from "../lib/api";
import { AreaLoading, EmptyState, ErrorState } from "./StateMessage";

interface Props {
  onChanged?: () => void;
}

const columnScope = { scope: "col" } as const;

export function ProjectList({ onChanged }: Props = {}) {
  const { t } = useTranslation();
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [loadError, setLoadError] = useState<Error | null>(null);
  const [updateError, setUpdateError] = useState<Error | null>(null);
  const [pendingID, setPendingID] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoadError(null);
    try {
      setProjects(await fetchProjects());
    } catch (reason) {
      setLoadError(reason instanceof Error ? reason : new Error(t("project.error.load")));
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  const toggle = async (project: Project) => {
    const archived = Boolean(project.archived_at);
    const key = archived ? "project.confirm.unarchive" : "project.confirm.archive";
    if (!window.confirm(t(key, { name: project.name }))) return;
    setUpdateError(null);
    setPendingID(project.id);
    try {
      await (archived ? unarchiveProject(project.id) : archiveProject(project.id));
      await load();
      onChanged?.();
    } catch (reason) {
      setUpdateError(reason instanceof Error ? reason : new Error(t("project.error.update")));
    } finally {
      setPendingID(null);
    }
  };

  if (loadError) return <ErrorState message={loadError.message} onRetry={() => void load()} />;
  if (projects === null) return <AreaLoading label={t("dashboard.projects.title")} />;
  if (projects.length === 0) return <EmptyState>{t("project.empty")}</EmptyState>;

  return (
    <div className="space-y-3">
      {updateError && <ErrorState message={updateError.message} onRetry={() => setUpdateError(null)} />}
      <div className="table-scroll">
        <Table className="min-w-[32rem] w-full border-collapse text-left text-base">
          <caption className="sr-only">{t("project.caption.list")}</caption>
          <Table.Header className="border-b-2 border-ink-300 text-base text-ink-700">
            <Table.Row>
              <Table.Head {...columnScope} className="px-3 py-3 font-semibold">{t("project.column.name")}</Table.Head>
              <Table.Head {...columnScope} className="w-40 px-3 py-3 font-semibold">{t("project.column.status")}</Table.Head>
              <Table.Head {...columnScope} className="w-40 px-3 py-3 font-semibold">{t("project.column.action")}</Table.Head>
            </Table.Row>
          </Table.Header>
          <Table.Body>
            {projects.map((project) => {
              const archived = Boolean(project.archived_at);
              return (
                <Table.Row key={project.id} className="border-b border-line align-top last:border-b-0">
                  <Table.Cell className="px-3 py-4 font-medium text-ink-950">{project.name}</Table.Cell>
                  <Table.Cell className="px-3 py-4 text-ink-700">
                    {archived ? t("project.status.archived") : t("project.status.active")}
                  </Table.Cell>
                  <Table.Cell className="px-3 py-4">
                    <Button
                      type="button"
                      variant="outline"
                      className="focus-ring px-3 py-2 text-base disabled:cursor-wait disabled:opacity-60"
                      disabled={pendingID === project.id}
                      onClick={() => void toggle(project)}
                    >
                      {archived ? t("project.action.unarchive") : t("project.action.archive")}
                    </Button>
                  </Table.Cell>
                </Table.Row>
              );
            })}
          </Table.Body>
        </Table>
      </div>
    </div>
  );
}
