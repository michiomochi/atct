import { Button } from "@cloudflare/kumo/components/button";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { readStoredLocale, resolveLocale, storeLocale, type Locale } from "../i18n";

const locales: Locale[] = ["en", "ja"];

export function LocaleSwitch() {
  const { i18n, t } = useTranslation();
  const active = i18n.language === "ja" ? "ja" : "en";

  useEffect(() => {
    let timer: number | undefined;
    const apply = () => {
      const nav = typeof navigator === "undefined" ? null : navigator.language;
      const next = resolveLocale(readStoredLocale(), nav);
      if (i18n.language !== next) void i18n.changeLanguage(next);
    };

    if (document.readyState === "complete") {
      timer = window.setTimeout(apply, 0);
    } else {
      const onLoad = () => {
        timer = window.setTimeout(apply, 0);
      };
      window.addEventListener("load", onLoad, { once: true });
      return () => {
        window.removeEventListener("load", onLoad);
        if (timer !== undefined) window.clearTimeout(timer);
      };
    }

    return () => {
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [i18n]);

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
        {t("nav.dashboard")}
      </a>
      <div className="flex items-center gap-2" aria-label={t("locale.label")}>
        {locales.map((locale) => (
          <Button
            key={locale}
            type="button"
            aria-pressed={active === locale}
            className="focus-ring px-2 py-1 text-sm font-medium !text-ink-700 hover:!text-ink-950"
            onClick={() => select(locale)}
          >
            {t(`locale.${locale}`)}
          </Button>
        ))}
      </div>
    </div>
  );
}
