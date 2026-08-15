/** @type {import('tailwindcss').Config} */
export default {
  content: ["./src/**/*.{astro,html,js,jsx,md,mdx,ts,tsx}"],
  theme: {
    extend: {
      colors: {
        ink: {
          950: "#17232c",
          800: "#2b3a44",
          700: "#53636d",
          500: "#778891",
          300: "#b9c5ca",
        },
        paper: "#f7f8f6",
        surface: "#ffffff",
        line: "#d9e0e2",
        accent: {
          700: "#075985",
          600: "#0369a1",
          100: "#e0f2fe",
        },
        danger: {
          700: "#b42318",
          100: "#fef3f2",
        },
        notice: {
          800: "#854d0e",
          100: "#fefce8",
        },
      },
      fontFamily: {
        sans: ["Inter", "Noto Sans JP", "ui-sans-serif", "system-ui", "sans-serif"],
        display: ["Inter", "Noto Sans JP", "ui-sans-serif", "system-ui", "sans-serif"],
        mono: ["ui-monospace", "SFMono-Regular", "Menlo", "monospace"],
      },
      borderRadius: {
        surface: "0.625rem",
        control: "0.375rem",
      },
      boxShadow: {
        subtle: "0 1px 2px rgb(23 35 44 / 0.08)",
      },
    },
  },
  plugins: [],
};
