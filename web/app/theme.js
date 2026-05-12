export var THEME_QUERY = window.matchMedia
  ? window.matchMedia("(prefers-color-scheme: dark)")
  : null;

var LIGHT_COLORS = {
  total: "#e11d48",
  cached: "#fb7185",
  input: "#60a5fa",
  nonCachedInput: "#14b8a6",
  output: "#7c5cff",
  reasoning: "#f59e0b",
  cachedSoft: "rgba(246, 95, 134, 0.14)",
  inputSoft: "rgba(96, 165, 250, 0.14)",
  text: "#251a2d",
  muted: "#64748b",
  grid: "#e9edf3",
  nonCachedInputLine: "rgba(20, 184, 166, 0.55)",
  outputLine: "rgba(124, 92, 255, 0.55)",
  reasoningLine: "rgba(245, 158, 11, 0.6)",
  outputSoft: "rgba(124, 92, 255, 0.14)",
  chartSurface: "#ffffff",
  tooltipBg: "rgba(37, 26, 45, 0.92)",
  tooltipBorder: "rgba(226, 232, 240, 0.6)",
  doughnutEmpty: "#e2e8f0",
};

var DARK_COLORS = {
  total: "#fb7185",
  cached: "#fda4af",
  input: "#93c5fd",
  nonCachedInput: "#2dd4bf",
  output: "#a78bfa",
  reasoning: "#fbbf24",
  cachedSoft: "rgba(253, 164, 175, 0.18)",
  inputSoft: "rgba(147, 197, 253, 0.16)",
  text: "#f8fafc",
  muted: "#9aa7b8",
  grid: "#273142",
  nonCachedInputLine: "rgba(45, 212, 191, 0.62)",
  outputLine: "rgba(167, 139, 250, 0.62)",
  reasoningLine: "rgba(251, 191, 36, 0.68)",
  outputSoft: "rgba(167, 139, 250, 0.16)",
  chartSurface: "#171c24",
  tooltipBg: "rgba(15, 18, 24, 0.95)",
  tooltipBorder: "rgba(42, 51, 66, 0.95)",
  doughnutEmpty: "#334155",
};

export function isDarkMode() {
  return Boolean(THEME_QUERY && THEME_QUERY.matches);
}

export function themeColors() {
  return isDarkMode() ? DARK_COLORS : LIGHT_COLORS;
}
