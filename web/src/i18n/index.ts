import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import { en, type TranslationKey } from "./en";
import { ja } from "./ja";

export type Locale = "en" | "ja";
export const STORAGE_KEY = "atct.locale";

i18n.use(initReactI18next).init({
  resources: { en: { translation: en }, ja: { translation: ja } },
  // Start at English so server-rendered markup and the first client render agree.
  // The stored choice is applied after hydration; see the provider in Task 2.
  lng: "en",
  fallbackLng: "en",
  // Keys are dotted flat strings, and UI copy contains ":". Without these two,
  // i18next reads "dashboard.title" as nesting and "Status: open" as a namespace.
  keySeparator: false,
  nsSeparator: false,
  // React escapes its own output; leaving this on double-escapes apostrophes.
  interpolation: { escapeValue: false },
  returnNull: false,
});

export default i18n;

export function t(key: TranslationKey, params?: Record<string, string | number>): string {
  return i18n.t(key, params) as string;
}

function isLocale(value: unknown): value is Locale {
  return value === "en" || value === "ja";
}

// Stored choice wins; then the browser's language; then English.
export function resolveLocale(stored: string | null, navigatorLanguage: string | null): Locale {
  if (isLocale(stored)) return stored;
  if (navigatorLanguage && navigatorLanguage.toLowerCase().startsWith("ja")) return "ja";
  return "en";
}

export function readStoredLocale(): string | null {
  try {
    return localStorage.getItem(STORAGE_KEY);
  } catch {
    return null; // private mode or a blocked storage partition
  }
}

export function storeLocale(locale: Locale): void {
  try {
    localStorage.setItem(STORAGE_KEY, locale);
  } catch {
    // Persisting is best-effort; the session still switches.
  }
}

export function formatDateTime(locale: Locale, iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  return new Intl.DateTimeFormat(locale === "ja" ? "ja-JP" : "en-US", {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date);
}

// Composed from translated units rather than hardcoded suffixes.
export function formatDuration(locale: Locale, seconds: number): string {
  const unit = (key: TranslationKey, value: number) =>
    i18n.getFixedT(locale)(key, { value }) as string;
  if (!Number.isFinite(seconds) || seconds <= 0) return i18n.getFixedT(locale)("duration.none") as string;
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (hours > 0) {
    const h = unit("duration.hours", hours);
    const m = unit("duration.minutes", minutes);
    return locale === "ja" ? `${h}${m}` : `${h} ${m}`;
  }
  if (minutes > 0) return unit("duration.minutes", minutes);
  return unit("duration.seconds", Math.floor(seconds));
}
