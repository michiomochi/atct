import { Button } from "@cloudflare/kumo/components/button";
import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import type { Decision } from "../lib/api";
import { answerDecision, ApiError } from "../lib/api";
import { formatDuration } from "../i18n";
import { decisionAutoSettlementSeconds, decisionRecommendationLabel, validateAnswer, type AnswerErrors } from "../lib/ui";

interface Props {
  decision: Decision;
  onUpdated: () => void;
  onInputStateChange?: (hasInput: boolean, decisionID: string) => void;
}

export function DecisionAnswerForm({ decision, onUpdated, onInputStateChange }: Props) {
  const { t, i18n } = useTranslation();
  const locale = i18n.language.startsWith("ja") ? "ja" : "en";
  const [answerLabel, setAnswerLabel] = useState("");
  const [answerText, setAnswerText] = useState("");
  const [errors, setErrors] = useState<AnswerErrors>({});
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [conflict, setConflict] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    onInputStateChange?.(
      answerLabel.trim().length > 0 || answerText.trim().length > 0,
      decision.id,
    );
  }, [answerLabel, answerText, decision.id, onInputStateChange]);

  const labelId = `answer-label-${decision.id}`;
  const textId = `answer-text-${decision.id}`;
  const options = decision.options ?? [];
  const autoSettlementSeconds = decisionAutoSettlementSeconds(decision.default_after_ms);
  const autoSettlementDuration = autoSettlementSeconds === undefined
    ? undefined
    : formatDuration(locale, autoSettlementSeconds);

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSubmitError(null);
    setConflict(false);

    const nextErrors = validateAnswer({
      answer_label: answerLabel,
      answer_text: answerText,
    });
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;

    setSubmitting(true);
    try {
      await answerDecision(decision.id, {
        answer_label: answerLabel.trim(),
        answer_text: answerText.trim(),
      });
      onUpdated();
    } catch (error) {
      if (error instanceof ApiError && error.status === 409) {
        setConflict(true);
      } else {
        setSubmitError(error instanceof Error ? error.message : t("form.answer.error.submit"));
      }
    } finally {
      setSubmitting(false);
    }
  }

  if (conflict) {
    return (
      <div className="mt-3 border border-notice-800 bg-notice-100 px-3 py-3 text-sm text-notice-800" role="alert">
        <p>{t("form.answer.conflict")}</p>
        <Button
          type="button"
          className="focus-ring mt-3 border border-notice-800 bg-surface px-3 py-2 text-sm font-medium text-notice-800 hover:bg-notice-100"
          onClick={onUpdated}
        >
          {t("form.answer.fetchLatest")}
        </Button>
      </div>
    );
  }

  return (
    <form className="mt-3 border-l-2 border-accent-600 pl-3" onSubmit={handleSubmit} noValidate>
      <p className="mb-3 whitespace-pre-wrap break-words text-sm leading-6 text-ink-800">{decision.question}</p>
      {autoSettlementDuration && (
        <p className="mb-3 text-xs text-ink-600">
          {t("decision.autoSettlesIn", { duration: autoSettlementDuration })}
        </p>
      )}
      {options.length > 0 ? (
        <label className="mb-3 block text-sm text-ink-800" htmlFor={labelId}>
          {t("form.answer.label")} <span className="text-ink-500">{t("form.optional")}</span>
          <select
            className="focus-ring mt-1 block w-full border border-line bg-surface px-3 py-2 text-sm text-ink-950"
            id={labelId}
            value={answerLabel}
            onChange={(event) => setAnswerLabel(event.target.value)}
            aria-invalid={Boolean(errors.answer_label)}
            aria-describedby={errors.answer_label ? `${labelId}-error` : undefined}
          >
            <option value="">{t("form.answer.noLabel")}</option>
            {options.map((option) => {
              const recommendation = decisionRecommendationLabel(locale, decision.default_option, option.label);
              return (
                <option key={option.label} value={option.label}>
                  {option.label}{recommendation ? ` — ${recommendation}` : ""}
                </option>
              );
            })}
          </select>
          {errors.answer_label && <span className="mt-1 block text-xs text-danger-700" id={`${labelId}-error`}>{t("form.answer.error.labelOrText")}</span>}
        </label>
      ) : (
        <label className="mb-3 block text-sm text-ink-800" htmlFor={labelId}>
          {t("form.answer.label")} <span className="text-ink-500">{t("form.optional")}</span>
          <input
            className="focus-ring mt-1 block w-full border border-line bg-surface px-3 py-2 text-sm text-ink-950"
            id={labelId}
            value={answerLabel}
            onChange={(event) => setAnswerLabel(event.target.value)}
            aria-invalid={Boolean(errors.answer_label)}
            aria-describedby={errors.answer_label ? `${labelId}-error` : undefined}
          />
          {errors.answer_label && <span className="mt-1 block text-xs text-danger-700" id={`${labelId}-error`}>{t("form.answer.error.labelOrText")}</span>}
        </label>
      )}
      <label className="mb-3 block text-sm text-ink-800" htmlFor={textId}>
        {t("form.answer.text")} <span className="text-ink-500">{t("form.optional")}</span>
        <textarea
          className="focus-ring mt-1 block min-h-24 w-full resize-y border border-line bg-surface px-3 py-2 text-sm leading-6 text-ink-950"
          id={textId}
          value={answerText}
          onChange={(event) => setAnswerText(event.target.value)}
          aria-invalid={Boolean(errors.answer_text)}
          aria-describedby={errors.answer_text ? `${textId}-error` : undefined}
        />
        {errors.answer_text && <span className="mt-1 block text-xs text-danger-700" id={`${textId}-error`}>{t("form.answer.error.labelOrText")}</span>}
      </label>
      {submitError && <p className="mb-3 text-sm text-danger-700" role="alert">{submitError}</p>}
      <Button
        type="submit"
        disabled={submitting}
        className="focus-ring border border-accent-700 bg-accent-700 px-3 py-2 text-sm font-medium text-white transition hover:bg-accent-600 disabled:cursor-wait disabled:opacity-60"
      >
        {submitting ? t("form.answer.submitting") : t("form.answer.submit")}
      </Button>
    </form>
  );
}
