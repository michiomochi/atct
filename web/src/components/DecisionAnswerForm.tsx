import { Button } from "@cloudflare/kumo/components/button";
import { useEffect, useState, type FormEvent } from "react";
import type { Decision } from "../lib/api";
import { answerDecision, ApiError } from "../lib/api";
import { validateAnswer, type AnswerErrors } from "../lib/ui";

interface Props {
  decision: Decision;
  onUpdated: () => void;
}

const ANSWERED_BY_KEY = "atct.answered_by";

export function DecisionAnswerForm({ decision, onUpdated }: Props) {
  const [answerLabel, setAnswerLabel] = useState("");
  const [answerText, setAnswerText] = useState("");
  const [answeredBy, setAnsweredBy] = useState("");
  const [errors, setErrors] = useState<AnswerErrors>({});
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [conflict, setConflict] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    const saved = window.localStorage.getItem(ANSWERED_BY_KEY);
    if (saved) setAnsweredBy(saved);
  }, []);

  const labelId = `answer-label-${decision.id}`;
  const textId = `answer-text-${decision.id}`;
  const answeredById = `answered-by-${decision.id}`;
  const options = decision.options ?? [];

  function updateAnsweredBy(value: string) {
    setAnsweredBy(value);
    window.localStorage.setItem(ANSWERED_BY_KEY, value);
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSubmitError(null);
    setConflict(false);

    const nextErrors = validateAnswer({
      answer_label: answerLabel,
      answer_text: answerText,
      answered_by: answeredBy,
    });
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;

    setSubmitting(true);
    try {
      await answerDecision(decision.id, {
        answer_label: answerLabel.trim(),
        answer_text: answerText.trim(),
        answered_by: answeredBy.trim(),
      });
      onUpdated();
    } catch (error) {
      if (error instanceof ApiError && error.status === 409) {
        setConflict(true);
      } else {
        setSubmitError(error instanceof Error ? error.message : "Could not submit the answer.");
      }
    } finally {
      setSubmitting(false);
    }
  }

  if (conflict) {
    return (
      <div className="mt-3 border border-notice-800 bg-notice-100 px-3 py-3 text-sm text-notice-800" role="alert">
        <p>This decision has already been answered in another tab or by another agent.</p>
        <Button
          type="button"
          className="focus-ring mt-3 border border-notice-800 bg-surface px-3 py-2 text-sm font-medium text-notice-800 hover:bg-notice-100"
          onClick={onUpdated}
        >
          Fetch the latest decision
        </Button>
      </div>
    );
  }

  return (
    <form className="mt-3 border-l-2 border-accent-600 pl-3" onSubmit={handleSubmit} noValidate>
      <p className="mb-3 whitespace-pre-wrap break-words text-sm leading-6 text-ink-800">{decision.question}</p>
      {options.length > 0 ? (
        <label className="mb-3 block text-sm text-ink-800" htmlFor={labelId}>
          Label <span className="text-ink-500">(optional)</span>
          <select
            className="focus-ring mt-1 block w-full border border-line bg-surface px-3 py-2 text-sm text-ink-950"
            id={labelId}
            value={answerLabel}
            onChange={(event) => setAnswerLabel(event.target.value)}
            aria-invalid={Boolean(errors.answer_label)}
            aria-describedby={errors.answer_label ? `${labelId}-error` : undefined}
          >
            <option value="">No label selected</option>
            {options.map((option) => (
              <option key={option.label} value={option.label}>{option.label}</option>
            ))}
          </select>
          {errors.answer_label && <span className="mt-1 block text-xs text-danger-700" id={`${labelId}-error`}>{errors.answer_label}</span>}
        </label>
      ) : (
        <label className="mb-3 block text-sm text-ink-800" htmlFor={labelId}>
          Label <span className="text-ink-500">(optional)</span>
          <input
            className="focus-ring mt-1 block w-full border border-line bg-surface px-3 py-2 text-sm text-ink-950"
            id={labelId}
            value={answerLabel}
            onChange={(event) => setAnswerLabel(event.target.value)}
            aria-invalid={Boolean(errors.answer_label)}
            aria-describedby={errors.answer_label ? `${labelId}-error` : undefined}
          />
          {errors.answer_label && <span className="mt-1 block text-xs text-danger-700" id={`${labelId}-error`}>{errors.answer_label}</span>}
        </label>
      )}
      <label className="mb-3 block text-sm text-ink-800" htmlFor={textId}>
        Answer text <span className="text-ink-500">(optional)</span>
        <textarea
          className="focus-ring mt-1 block min-h-24 w-full resize-y border border-line bg-surface px-3 py-2 text-sm leading-6 text-ink-950"
          id={textId}
          value={answerText}
          onChange={(event) => setAnswerText(event.target.value)}
          aria-invalid={Boolean(errors.answer_text)}
          aria-describedby={errors.answer_text ? `${textId}-error` : undefined}
        />
        {errors.answer_text && <span className="mt-1 block text-xs text-danger-700" id={`${textId}-error`}>{errors.answer_text}</span>}
      </label>
      <label className="mb-3 block text-sm text-ink-800" htmlFor={answeredById}>
        Answered by <span className="text-danger-700">(required)</span>
        <input
          className="focus-ring mt-1 block w-full border border-line bg-surface px-3 py-2 text-sm text-ink-950"
          id={answeredById}
          value={answeredBy}
          onChange={(event) => updateAnsweredBy(event.target.value)}
          aria-invalid={Boolean(errors.answered_by)}
          aria-describedby={errors.answered_by ? `${answeredById}-error` : undefined}
          required
        />
        {errors.answered_by && <span className="mt-1 block text-xs text-danger-700" id={`${answeredById}-error`}>{errors.answered_by}</span>}
      </label>
      {submitError && <p className="mb-3 text-sm text-danger-700" role="alert">{submitError}</p>}
      <Button
        type="submit"
        disabled={submitting}
        className="focus-ring border border-accent-700 bg-accent-700 px-3 py-2 text-sm font-medium text-white transition hover:bg-accent-600 disabled:cursor-wait disabled:opacity-60"
      >
        {submitting ? "Submitting..." : "Submit answer"}
      </Button>
    </form>
  );
}
