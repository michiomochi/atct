import { describe, expect, it } from "vitest";
import tailwindConfig from "../../tailwind.config.mjs";

const sourceModules = import.meta.glob("../**/*.{ts,tsx,astro}", {
  eager: true,
  import: "default",
  query: "?raw",
}) as Record<string, string>;

// The colours Tailwind will actually generate, as "name" or "name-shade".
function definedTokens(): Set<string> {
  const colors = (tailwindConfig as { theme: { extend: { colors: Record<string, unknown> } } })
    .theme.extend.colors;
  const tokens = new Set<string>();
  for (const [name, value] of Object.entries(colors)) {
    if (typeof value === "string") {
      tokens.add(name);
      continue;
    }
    for (const shade of Object.keys(value as Record<string, string>)) {
      tokens.add(shade === "DEFAULT" ? name : `${name}-${shade}`);
    }
  }
  return tokens;
}

// Every place a project colour is used, including behind a variant such as
// hover: or focus:. Tailwind's own palette is not our problem, so only the
// families defined in the config are checked.
function usedTokens(families: string[]): Map<string, string[]> {
  const pattern = new RegExp(
    `\\b(?:[a-z-]+:)*(?:text|bg|border|decoration|ring|outline|fill|stroke|divide|placeholder|from|via|to)-(${families.join("|")})(?:-[a-z0-9]+)?\\b`,
    "g",
  );
  const found = new Map<string, string[]>();
  for (const [path, source] of Object.entries(sourceModules)) {
    if (path.endsWith("/colors.test.ts")) continue;
    for (const match of source.matchAll(pattern)) {
      const token = match[0].slice(match[0].lastIndexOf(":") + 1).replace(/^[a-z]+-/, "");
      const where = found.get(token) ?? [];
      where.push(path);
      found.set(token, where);
    }
  }
  return found;
}

describe("colour tokens", () => {
  it("uses only shades the Tailwind config defines", () => {
    const defined = definedTokens();
    const families = [...defined].map((token) => token.split("-")[0]);
    const used = usedTokens([...new Set(families)]);

    const undefinedTokens = [...used.entries()]
      .filter(([token]) => !defined.has(token))
      .map(([token, paths]) => `${token} (${[...new Set(paths)].join(", ")})`);

    // A class Tailwind does not generate is silently dropped, so the element
    // keeps whatever it inherited and nothing anywhere reports a problem.
    // tsc cannot see it and neither can a render test that only reads text.
    expect(undefinedTokens).toEqual([]);
  });

  it("finds the tokens that are defined", () => {
    // Guards the check itself: if the regex stopped matching, the test above
    // would pass by looking at nothing.
    const used = usedTokens(["ink", "accent", "danger", "line", "surface", "notice"]);
    expect(used.size).toBeGreaterThan(5);
  });
});
