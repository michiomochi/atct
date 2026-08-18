import { describe, expect, it } from "vitest";
import { en } from "./en";
import { ja } from "./ja";
import { formatDateTime, formatDuration, resolveLocale, t } from "./index";

describe("resource parity", () => {
  it("has the same keys in both languages", () => {
    expect(Object.keys(ja).sort()).toEqual(Object.keys(en).sort());
  });

  it("uses the same language names in both resources", () => {
    expect(en["locale.en"]).toBe(ja["locale.en"]);
    expect(en["locale.ja"]).toBe(ja["locale.ja"]);
  });

  it("has no empty strings", () => {
    for (const [key, value] of Object.entries({ ...en, ...ja })) {
      expect(value, `empty value for ${key}`).not.toBe("");
    }
  });

  it("translates recommendation and automatic settlement messages", () => {
    expect(en["decision.recommended"]).toBe("AI recommendation");
    expect(ja["decision.recommended"]).toBe("AI の推奨");
    expect(en["decision.autoSettlesIn"]).toBe("Auto-settles in {{duration}}");
    expect(ja["decision.autoSettlesIn"]).toBe("{{duration}}後に自動確定");
  });

  it("keeps dotted keys flat rather than nested", () => {
    for (const key of Object.keys(en)) {
      expect(typeof en[key as keyof typeof en]).toBe("string");
    }
  });
});

describe("resolveLocale", () => {
  it("prefers the stored locale", () => {
    expect(resolveLocale("ja", "en-US")).toBe("ja");
    expect(resolveLocale("en", "ja-JP")).toBe("en");
  });

  it("falls back to the browser language", () => {
    expect(resolveLocale(null, "ja-JP")).toBe("ja");
    expect(resolveLocale(null, "ja")).toBe("ja");
    expect(resolveLocale(null, "en-GB")).toBe("en");
  });

  it("ignores an unsupported stored value", () => {
    expect(resolveLocale("fr", "ja-JP")).toBe("ja");
    expect(resolveLocale("", null)).toBe("en");
  });

  it("defaults to English", () => {
    expect(resolveLocale(null, null)).toBe("en");
  });
});

describe("translation", () => {
  it("returns the string for the active language", async () => {
    const { default: i18n } = await import("./index");
    await i18n.changeLanguage("en");
    expect(t("dashboard.title")).toBe(en["dashboard.title"]);
    await i18n.changeLanguage("ja");
    expect(t("dashboard.title")).toBe(ja["dashboard.title"]);
    await i18n.changeLanguage("en");
  });

  it("interpolates named params", () => {
    expect(t("state.loadingLabel", { label: "Now" })).toContain("Now");
  });

  it("does not treat a colon in copy as a namespace separator", () => {
    expect(t("definitely:not:a:key")).toBe("definitely:not:a:key");
  });
});

describe("formatDateTime", () => {
  it("formats per locale", () => {
    const iso = "2026-08-16T09:48:32Z";
    expect(formatDateTime("en", iso)).not.toBe(formatDateTime("ja", iso));
    expect(formatDateTime("ja", iso)).toContain("2026");
  });

  it("returns the input when it is not a date", () => {
    expect(formatDateTime("en", "not-a-date")).toBe("not-a-date");
  });
});

describe("formatDuration", () => {
  it("uses translated units", () => {
    expect(formatDuration("en", 42)).toBe("42s");
    expect(formatDuration("en", 8115)).toBe("2h 15m");
    expect(formatDuration("ja", 42)).toBe("42\u79d2");
    expect(formatDuration("ja", 8115)).toBe("2\u6642\u959315\u5206");
  });

  it("renders zero as a dash", () => {
    expect(formatDuration("en", 0)).toBe("-");
  });
});
