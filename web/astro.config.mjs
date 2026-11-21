import { defineConfig } from "astro/config";
import react from "@astrojs/react";
import tailwindcss from "@tailwindcss/vite";

const locale = process.env.PUBLIC_ATCT_LOCALE;
if (locale !== "en" && locale !== "ja") {
  throw new Error("PUBLIC_ATCT_LOCALE must be en or ja");
}

export default defineConfig({
  output: "static",
  base: `/${locale}/`,
  outDir: `./dist/${locale}/`,
  integrations: [react()],
  vite: {
    plugins: [tailwindcss()],
    // Keep the sentinel file that lets go:embed compile on a fresh checkout.
    build: { emptyOutDir: false },
  },
});
