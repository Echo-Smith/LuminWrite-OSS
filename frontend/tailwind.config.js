/** @type {import('tailwindcss').Config} */
export default {
  darkMode: ["class"],
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        border: "hsl(var(--border))",
        input: "hsl(var(--input))",
        ring: "hsl(var(--ring))",
        background: "hsl(var(--background))",
        foreground: "hsl(var(--foreground))",
        primary: {
          DEFAULT: "hsl(var(--primary))",
          foreground: "hsl(var(--primary-foreground))",
        },
        secondary: {
          DEFAULT: "hsl(var(--secondary))",
          foreground: "hsl(var(--secondary-foreground))",
        },
        destructive: {
          DEFAULT: "hsl(var(--destructive))",
          foreground: "hsl(var(--destructive-foreground))",
        },
        muted: {
          DEFAULT: "hsl(var(--muted))",
          foreground: "hsl(var(--muted-foreground))",
        },
        accent: {
          DEFAULT: "hsl(var(--accent))",
          foreground: "hsl(var(--accent-foreground))",
        },
        popover: {
          DEFAULT: "hsl(var(--popover))",
          foreground: "hsl(var(--popover-foreground))",
        },
        card: {
          DEFAULT: "hsl(var(--card))",
          foreground: "hsl(var(--card-foreground))",
        },
        // ── Codex 风格扩展色 ──
        surface: {
          DEFAULT: "hsl(var(--surface))",
          raised: "hsl(var(--surface-raised))",
          overlay: "hsl(var(--surface-overlay))",
        },
      },
      borderRadius: {
        lg: "var(--radius)",
        md: "calc(var(--radius) - 2px)",
        sm: "calc(var(--radius) - 4px)",
        xl: "calc(var(--radius) + 4px)",
        "2xl": "calc(var(--radius) + 8px)",
        composer: "var(--composer-radius)",
      },
      fontFamily: {
        sans: [
          "-apple-system",
          "BlinkMacSystemFont",
          "Segoe UI",
          "Roboto",
          "Helvetica Neue",
          "Arial",
          "PingFang SC",
          "Microsoft YaHei",
          "sans-serif",
        ],
        mono: [
          "SF Mono",
          "JetBrains Mono",
          "Fira Code",
          "Menlo",
          "Monaco",
          "Consolas",
          "monospace",
        ],
      },
      // ── 动画系统扩展 ──
      animation: {
        "fade-in": "anim-fade-in var(--anim-duration-normal) var(--anim-ease-out) both",
        "fade-up": "anim-fade-up var(--anim-duration-normal) var(--anim-ease-precise) both",
        "fade-down": "anim-fade-down var(--anim-duration-normal) var(--anim-ease-precise) both",
        "fade-scale": "anim-fade-scale var(--anim-duration-normal) var(--anim-ease-spring) both",
        "fade-blur": "anim-fade-blur var(--anim-duration-slow) var(--anim-ease-precise) both",
        "slide-right": "anim-slide-right var(--anim-duration-normal) var(--anim-ease-precise) both",
        "slide-left": "anim-slide-left var(--anim-duration-normal) var(--anim-ease-precise) both",
        "pop-in": "anim-pop-in var(--anim-duration-slow) var(--anim-ease-spring) both",
        "spin-smooth": "anim-spin 0.8s var(--anim-ease-linear) infinite",
        "pulse-soft": "anim-pulse 2s var(--anim-ease-in-out) infinite",
        "pulse-ring": "anim-pulse-ring 2s var(--anim-ease-out) infinite",
        "shimmer": "anim-shimmer 2s var(--anim-ease-linear) infinite",
        "cursor-blink": "anim-cursor-blink 1s steps(2) infinite",
        "gradient-flow": "anim-gradient-flow 8s var(--anim-ease-smooth) infinite",
        "progress-indeterminate": "anim-progress-indeterminate 1.5s var(--anim-ease-in-out) infinite",
        "shake": "anim-shake 0.4s var(--anim-ease-in-out)",
        "bounce-dot": "anim-bounce-dot 1.2s var(--anim-ease-in-out) infinite",
      },
      keyframes: {
        "anim-fade-in": {
          from: { opacity: "0" },
          to: { opacity: "1" },
        },
        "anim-fade-up": {
          from: { opacity: "0", transform: "translateY(8px)" },
          to: { opacity: "1", transform: "translateY(0)" },
        },
        "anim-fade-down": {
          from: { opacity: "0", transform: "translateY(-8px)" },
          to: { opacity: "1", transform: "translateY(0)" },
        },
        "anim-fade-scale": {
          from: { opacity: "0", transform: "scale(0.96)" },
          to: { opacity: "1", transform: "scale(1)" },
        },
        "anim-fade-blur": {
          from: { opacity: "0", filter: "blur(4px)", transform: "translateY(4px)" },
          to: { opacity: "1", filter: "blur(0)", transform: "translateY(0)" },
        },
        "anim-slide-right": {
          from: { opacity: "0", transform: "translateX(-12px)" },
          to: { opacity: "1", transform: "translateX(0)" },
        },
        "anim-slide-left": {
          from: { opacity: "0", transform: "translateX(12px)" },
          to: { opacity: "1", transform: "translateX(0)" },
        },
        "anim-pop-in": {
          "0%": { opacity: "0", transform: "scale(0.8)" },
          "60%": { opacity: "1", transform: "scale(1.03)" },
          "100%": { transform: "scale(1)" },
        },
        "anim-spin": {
          to: { transform: "rotate(360deg)" },
        },
        "anim-pulse": {
          "0%, 100%": { opacity: "1", transform: "scale(1)" },
          "50%": { opacity: "0.6", transform: "scale(0.92)" },
        },
        "anim-pulse-ring": {
          "0%": { boxShadow: "0 0 0 0 var(--anim-ring-color, rgba(16, 163, 127, 0.4))" },
          "70%": { boxShadow: "0 0 0 6px transparent" },
          "100%": { boxShadow: "0 0 0 0 transparent" },
        },
        "anim-bounce-dot": {
          "0%, 80%, 100%": { transform: "translateY(0)", opacity: "0.4" },
          "40%": { transform: "translateY(-6px)", opacity: "1" },
        },
        "anim-cursor-blink": {
          "0%, 49%": { opacity: "1" },
          "50%, 100%": { opacity: "0" },
        },
        "anim-shimmer": {
          "0%": { backgroundPosition: "-200% 0" },
          "100%": { backgroundPosition: "200% 0" },
        },
        "anim-gradient-flow": {
          "0%, 100%": { backgroundPosition: "0% 50%" },
          "50%": { backgroundPosition: "100% 50%" },
        },
        "anim-shake": {
          "0%, 100%": { transform: "translateX(0)" },
          "20%": { transform: "translateX(-4px)" },
          "40%": { transform: "translateX(4px)" },
          "60%": { transform: "translateX(-3px)" },
          "80%": { transform: "translateX(3px)" },
        },
        "anim-progress-indeterminate": {
          "0%": { transform: "translateX(-100%)" },
          "100%": { transform: "translateX(400%)" },
        },
      },
      transitionTimingFunction: {
        smooth: "cubic-bezier(0.45, 0, 0.15, 1)",
        precise: "cubic-bezier(0.16, 1, 0.3, 1)",
        spring: "cubic-bezier(0.34, 1.56, 0.64, 1)",
      },
      transitionDuration: {
        instant: "80ms",
      },
    },
  },
  plugins: [
    require("@tailwindcss/typography"),
  ],
};
