import { Button } from "@cloudflare/kumo/components/button";
import { useTranslation } from "react-i18next";
import { storeLocale, type Locale } from "../i18n";

const locales: Locale[] = ["en", "ja"];

export function LocaleSwitch() {
  const { i18n, t } = useTranslation();
  const active = i18n.language === "ja" ? "ja" : "en";

  function select(next: Locale) {
    storeLocale(next);
    void i18n.changeLanguage(next);
  }

  return (
    <div className="flex items-center gap-4">
      <a
        className="focus-ring text-sm font-medium text-ink-700 underline decoration-transparent underline-offset-4 transition hover:text-ink-950 hover:decoration-ink-300"
        href="/"
      >
        {t("nav.inbox")}
      </a>
      <div className="flex items-center gap-2" aria-label={t("locale.label")}>
        {locales.map((locale) => (
          <Button
            key={locale}
            type="button"
            aria-pressed={active === locale}
            className="focus-ring px-2 py-1 text-sm font-medium text-ink-700 hover:text-ink-950"
            onClick={() => select(locale)}
          >
            {t(`locale.${locale}`)}
          </Button>
        ))}
      </div>
    </div>
  );
}
