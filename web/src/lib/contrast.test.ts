/// <reference types="astro/client" />

import { describe, expect, it } from "vitest";
// The project does not load Node's module declarations during tsc, but Vitest
// runs these tests in Node and needs the actual stylesheet source.
// @ts-expect-error Node's fs module is not declared in this project's tsc setup.
import { readFileSync } from "node:fs";
import {
  contrastRatio,
  mixOklchWithWhite,
  oklchToSrgb,
  parseBrandFromCss,
} from "./contrast";

const globalCss = readFileSync("src/styles/global.css", "utf8");

function emphasisContrast(cssText: string): number {
  const { l, c, h } = parseBrandFromCss(cssText);
  const [backgroundL, backgroundC, backgroundH] = mixOklchWithWhite(l, c, h, 30);
  return contrastRatio(oklchToSrgb(backgroundL, backgroundC, backgroundH), [255, 255, 255]);
}

function expectBrandToMeetAa(cssText: string): void {
  expect(emphasisContrast(cssText)).toBeGreaterThanOrEqual(4.5);
}

describe("Kumo brand contrast", () => {
  it("N1: returns WCAG ratios for black and white", () => {
    expect(contrastRatio([0, 0, 0], [255, 255, 255])).toBeCloseTo(21, 10);
    expect(contrastRatio([255, 255, 255], [255, 255, 255])).toBeCloseTo(1, 10);
  });

  it("N2: converts Kumo's original brand to the measured sRGB anchor", () => {
    const actual = oklchToSrgb(0.5772, 0.2324, 260);
    const expected = [5, 109, 255];

    actual.forEach((channel, index) => {
      expect(Math.abs(channel - expected[index])).toBeLessThanOrEqual(1);
    });
  });

  it("N3: keeps the primary emphasis background at AA against white", () => {
    expectBrandToMeetAa(globalCss);
  });

  it("N4: parses the first light-dark brand argument", () => {
    const css = `
      :root {
        --color-kumo-brand: light-dark(oklch(36% .2324 260), oklch(33% .2324 260));
      }
    `;

    expect(parseBrandFromCss(css)).toEqual({ l: 0.36, c: 0.2324, h: 260 });
  });

  it("B1: rejects the previous brand lightness for the AA guard", () => {
    const previousCss = globalCss.replace(
      "oklch(36% .2324 260)",
      "oklch(57.72% .2324 260)",
    );

    expect(emphasisContrast(previousCss)).toBeLessThan(4.5);
    expect(() => expectBrandToMeetAa(previousCss)).toThrow();
  });

  it("B3: preserves the existing global CSS declarations", () => {
    for (const declaration of [
      '@config "../../tailwind.config.mjs";',
      '@source "../../node_modules/@cloudflare/kumo/dist/**/*.{js,jsx,ts,tsx}";',
      '@import "@cloudflare/kumo/styles/tailwind";',
      '@import "tailwindcss";',
      ".table-scroll {",
      ".text-clamp-2 {",
      ".focus-ring:focus-visible {",
    ]) {
      expect(globalCss).toContain(declaration);
    }
  });

  it("B4: keeps Kumo's brand hue and chroma unchanged", () => {
    const brand = parseBrandFromCss(globalCss);

    expect(brand.c).toBeCloseTo(0.2324, 4);
    expect(brand.h).toBe(260);
  });

  it("N5: declares Kumo's brand outside a cascade layer", () => {
    const declarationIndex = globalCss.indexOf("--color-kumo-brand:");
    expect(declarationIndex).toBeGreaterThanOrEqual(0);

    let braceDepth = 0;
    for (const character of globalCss.slice(0, declarationIndex)) {
      if (character === "{") {
        braceDepth++;
      } else if (character === "}") {
        braceDepth--;
      }
    }

    expect(braceDepth).toBe(1);
  });
});
