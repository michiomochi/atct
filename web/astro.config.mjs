import { defineConfig } from "astro/config";
import react from "@astrojs/react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  output: "static",
  integrations: [react()],
  vite: {
    plugins: [tailwindcss()],
    // Keep the sentinel file that lets go:embed compile on a fresh checkout.
    build: { emptyOutDir: false },
  },
});
