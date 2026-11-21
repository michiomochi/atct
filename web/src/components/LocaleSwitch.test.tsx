import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LocaleSwitch } from "./LocaleSwitch";

const { updateUILocale, changeLanguage, reload } = vi.hoisted(() => ({
	updateUILocale: vi.fn(),
	changeLanguage: vi.fn(),
	reload: vi.fn(),
}));

vi.mock("../lib/api", () => ({ updateUILocale }));
vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    i18n: { language: "en", changeLanguage },
    t: (key: string) => key,
  }),
}));

describe("LocaleSwitch", () => {
	beforeEach(() => {
		cleanup();
		updateUILocale.mockReset();
		changeLanguage.mockReset();
		reload.mockReset();
		const testWindow = Object.create(window);
		Object.defineProperty(testWindow, "location", { value: { reload }, configurable: true });
		vi.stubGlobal("window", testWindow);
	});

	afterEach(() => {
		vi.unstubAllGlobals();
		vi.restoreAllMocks();
		cleanup();
	});

	it("disables both buttons while saving and reloads after success", async () => {
		let resolveUpdate!: (value: { locale: "ja" }) => void;
		updateUILocale.mockReturnValue(new Promise((resolve) => {
			resolveUpdate = resolve;
		}));

		render(<LocaleSwitch />);
		fireEvent.click(screen.getByRole("button", { name: "locale.ja" }));

		expect(screen.getAllByRole("button").every((button) => (button as HTMLButtonElement).disabled)).toBe(true);
		expect(changeLanguage).not.toHaveBeenCalled();

		resolveUpdate({ locale: "ja" });
		await waitFor(() => expect(reload).toHaveBeenCalledTimes(1));
		expect(updateUILocale).toHaveBeenCalledWith("ja");
		expect(changeLanguage).not.toHaveBeenCalled();
	});

	it("shows an accessible error and preserves the current locale after failure", async () => {
		updateUILocale.mockRejectedValue(new Error("offline"));

		render(<LocaleSwitch />);
		fireEvent.click(screen.getByRole("button", { name: "locale.ja" }));

		const alert = await screen.findByRole("alert");
		expect(alert.textContent).not.toBe("");
		expect(reload).not.toHaveBeenCalled();
		expect(changeLanguage).not.toHaveBeenCalled();
		expect(screen.getByRole("button", { name: "locale.en" }).getAttribute("aria-pressed")).toBe("true");
	});
});
