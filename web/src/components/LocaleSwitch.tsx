import { Button } from "@cloudflare/kumo/components/button";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { updateUILocale } from "../lib/api";
import type { Locale } from "../i18n";

const locales: Locale[] = ["en", "ja"];

export function LocaleSwitch() {
  const { i18n, t } = useTranslation();
  const active = i18n.language === "ja" ? "ja" : "en";

  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function select(next: Locale) {
    setError(null);
    setPending(true);
    try {
      await updateUILocale(next);
    } catch {
      setError(t("locale.error.update"));
      setPending(false);
      return;
    }
    window.location.reload();
  }

  return (
    <div className="flex items-center gap-4">
      <div className="flex items-center gap-2" aria-label={t("locale.label")}>
        {locales.map((locale) => (
          <Button
            key={locale}
            type="button"
            aria-pressed={active === locale}
            disabled={pending}
            variant="ghost"
            className="focus-ring px-2 py-1 text-base font-medium"
            onClick={() => select(locale)}
          >
            {t(`locale.${locale}`)}
          </Button>
        ))}
      </div>
      {error && <p className="text-base text-danger-700" role="alert">{error}</p>}
    </div>
  );
}
