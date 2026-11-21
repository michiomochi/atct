import { useTranslation } from "react-i18next";
import type { Decision, StalledProject } from "../lib/api";
import { formatDateTime, formatDuration } from "../i18n";
import { encodePathSegment, stripHeadingMarker } from "../lib/ui";

interface Props {
  decisions: Decision[];
  projects: StalledProject[];
}

// StalledNotice shows answers that have waited unapplied too long and projects
// that have gone quiet. It only displays; it does not apply or answer anything.
export function StalledNotice({ decisions, projects }: Props) {
  const { t, i18n } = useTranslation();
  if (decisions.length === 0 && projects.length === 0) return null;
  const locale = i18n.language.startsWith("ja") ? "ja" : "en";

  return (
    <div className="border border-notice-800 bg-notice-100 px-4 py-4 text-base text-notice-800" role="alert">
      <h2 className="font-display text-lg font-semibold">{t("dashboard.stalled.title")}</h2>
      {decisions.length > 0 && (
        <ul className="mt-3 space-y-3">
          {decisions.map((decision) => {
            const answeredAt = Date.parse(decision.answered_at ?? "");
            const elapsed = Math.max(0, Math.floor((Date.now() - answeredAt) / 1000));
            return (
              <li key={decision.id}>
                <span className="break-all">{decision.project_name || "-"}</span>
                {" / "}
                <a
                  className="focus-ring font-medium text-accent-700 underline decoration-accent-100 underline-offset-4 hover:decoration-accent-700"
                  href={`/goals/${encodePathSegment(decision.goal_id)}`}
                >
                  {stripHeadingMarker(decision.goal_headline) || "-"}
                </a>
                <p className="mt-1">{t("dashboard.stalled.answerUnapplied", { duration: formatDuration(locale, elapsed) })}</p>
                <p className="mt-1 text-ink-700">
                  {decision.owner_last_seen_at
                    ? t("dashboard.stalled.lastHeartbeat", { time: formatDateTime(locale, decision.owner_last_seen_at) })
                    : t("dashboard.stalled.noHeartbeat")}
                </p>
              </li>
            );
          })}
        </ul>
      )}
      {projects.length > 0 && (
        <ul className="mt-3 space-y-3">
          {projects.map((project) => (
            <li key={project.project_id}>
              <p className="break-all">{project.project_name}</p>
              <p className="mt-1">{t("dashboard.stalled.projectSilent")}</p>
              <p className="mt-1 text-ink-700">
                {project.last_seen_at
                  ? t("dashboard.stalled.lastHeartbeat", { time: formatDateTime(locale, project.last_seen_at) })
                  : t("dashboard.stalled.noHeartbeat")}
              </p>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
