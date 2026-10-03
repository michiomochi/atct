// @vitest-environment jsdom
import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Markdown } from "./Markdown";

afterEach(cleanup);

function html(md: string) {
  return render(<Markdown>{md}</Markdown>).container;
}

describe("Markdown", () => {
  it("renders headings keeping semantic level", () => {
    const c = html("# One\n\n## Two\n\n### Three");
    expect(c.querySelector("h1")?.textContent).toBe("One");
    expect(c.querySelector("h2")?.textContent).toBe("Two");
    expect(c.querySelector("h3")?.textContent).toBe("Three");
    expect(c.querySelector("h1")?.className).toContain("text-lg");
  });

  it("renders lists, emphasis and inline code", () => {
    const c = html("- a\n- b\n\n1. x\n\n**bold** *it* `code`");
    expect(c.querySelectorAll("ul > li")).toHaveLength(2);
    expect(c.querySelectorAll("ol > li")).toHaveLength(1);
    expect(c.querySelector("strong")?.textContent).toBe("bold");
    expect(c.querySelector("em")?.textContent).toBe("it");
    const code = c.querySelector("p code");
    expect(code?.textContent).toBe("code");
    expect(code?.className).toContain("bg-paper");
  });

  it("wraps tables in table-scroll", () => {
    const c = html("| a | b |\n|---|---|\n| 1 | 2 |");
    const table = c.querySelector("table");
    expect(table?.parentElement?.className).toContain("table-scroll");
    expect(c.querySelectorAll("td")).toHaveLength(2);
  });

  it("renders fenced code in a scrollable pre", () => {
    const c = html("```go\nfmt.Println(1)\n```");
    const pre = c.querySelector("pre");
    expect(pre?.className).toContain("overflow-x-auto");
    expect(pre?.textContent).toContain("fmt.Println(1)");
  });

  it("opens links in a new tab safely", () => {
    const a = html("[x](https://example.com)").querySelector("a");
    expect(a?.getAttribute("href")).toBe("https://example.com");
    expect(a?.getAttribute("target")).toBe("_blank");
    expect(a?.getAttribute("rel")).toBe("noopener noreferrer");
  });

  it("does not interpret raw HTML", () => {
    const c = html("<script>alert(1)</script>\n\n<img src=x onerror=alert(1)>");
    expect(c.querySelector("script")).toBeNull();
    expect(c.querySelector("img")).toBeNull();
  });

  it("drops javascript: hrefs", () => {
    const a = html("[x](javascript:alert(1))").querySelector("a");
    expect(a?.getAttribute("href") ?? "").not.toContain("javascript");
  });

  it("turns image syntax into a text link", () => {
    const c = html("![logo](https://example.com/a.png)");
    expect(c.querySelector("img")).toBeNull();
    const a = c.querySelector("a");
    expect(a?.getAttribute("href")).toBe("https://example.com/a.png");
    expect(a?.textContent).toContain("logo");
    expect(a?.textContent).toContain("https://example.com/a.png");
  });
});
