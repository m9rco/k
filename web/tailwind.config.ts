import type { Config } from "tailwindcss";

// Design tokens for the "去 AI 化" sober aesthetic: 12px radius, 1.6 body line
// height, soft hairline borders, desaturated accent. Colors are driven by CSS
// variables (see src/index.css) so the whole system has a single source.
export default {
  darkMode: "class",
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        bg: "hsl(var(--bg) / <alpha-value>)",
        "bg-elev": "hsl(var(--bg-elev) / <alpha-value>)",
        "bg-elev-2": "hsl(var(--bg-elev-2) / <alpha-value>)",
        line: "hsl(var(--line) / <alpha-value>)",
        fg: "hsl(var(--fg) / <alpha-value>)",
        "fg-dim": "hsl(var(--fg-dim) / <alpha-value>)",
        "fg-mute": "hsl(var(--fg-mute) / <alpha-value>)",
        accent: "hsl(var(--accent) / <alpha-value>)",
        "accent-fg": "hsl(var(--accent-fg) / <alpha-value>)",
        ok: "hsl(var(--ok) / <alpha-value>)",
        danger: "hsl(var(--danger) / <alpha-value>)",
        warn: "hsl(var(--warn) / <alpha-value>)",
      },
      borderRadius: {
        DEFAULT: "12px",
        lg: "12px",
        md: "10px",
        sm: "8px",
      },
      lineHeight: {
        relaxed: "1.6",
      },
      fontFamily: {
        sans: [
          "-apple-system", "BlinkMacSystemFont", "Segoe UI", "PingFang SC",
          "Hiragino Sans GB", "Microsoft YaHei", "sans-serif",
        ],
        mono: ["ui-monospace", "SFMono-Regular", "Menlo", "monospace"],
      },
      keyframes: {
        "fade-in": { from: { opacity: "0" }, to: { opacity: "1" } },
      },
      animation: {
        "fade-in": "fade-in 0.2s ease-out",
      },
    },
  },
  plugins: [],
} satisfies Config;
