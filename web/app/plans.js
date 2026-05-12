import { isDarkMode } from "./theme.js";

var PLAN_META = {
  free: {
    label: "free",
    icon: "stats/assets/plan-icons/free.png",
    color: "#ff2f6d",
    soft: "#fff1f5",
    border: "#ffd5e2",
    track: "#f1f5f9",
    darkSoft: "#3b1320",
    darkBorder: "#6f2438",
    darkTrack: "#3b1320",
  },
  go: {
    label: "go",
    icon: "stats/assets/plan-icons/go.png",
    color: "#0891b2",
    soft: "#ecfeff",
    border: "#a5f3fc",
    track: "#f1f5f9",
    darkSoft: "#11313b",
    darkBorder: "#0891b2",
    darkTrack: "#1e293b",
  },
  plus: {
    label: "plus",
    icon: "stats/assets/plan-icons/plus.png",
    color: "#1476ff",
    soft: "#eff6ff",
    border: "#bfdbfe",
    track: "#f1f5f9",
    darkSoft: "#10233f",
    darkBorder: "#1d4ed8",
    darkTrack: "#1e293b",
  },
  prolite: {
    label: "prolite",
    icon: "stats/assets/plan-icons/prolite.png",
    color: "#19c979",
    soft: "#ecfdf5",
    border: "#bbf7d0",
    track: "#f1f5f9",
    darkSoft: "#0f3327",
    darkBorder: "#047857",
    darkTrack: "#1e293b",
  },
  pro: {
    label: "pro",
    icon: "stats/assets/plan-icons/pro.png",
    color: "#ff9f1a",
    soft: "#fff7ed",
    border: "#fed7aa",
    track: "#f1f5f9",
    darkSoft: "#3b2610",
    darkBorder: "#b45309",
    darkTrack: "#1e293b",
  },
  team: {
    label: "team",
    icon: "stats/assets/plan-icons/team.png",
    color: "#7c3aed",
    soft: "#f5f3ff",
    border: "#ddd6fe",
    track: "#f1f5f9",
    darkSoft: "#23183f",
    darkBorder: "#6d28d9",
    darkTrack: "#1e293b",
  },
};

function normalizePlanType(value) {
  return String(value || "")
    .toLowerCase()
    .replace(/[\s_-]+/g, "");
}

export function planFor(account, index) {
  var key = normalizePlanType(account.plan_type);
  if (PLAN_META[key]) return PLAN_META[key];
  var fallbackColors = [
    ["#f8fafc", "#475569", "#e2e8f0", "#1e293b", "#475569"],
    ["#f5f3ff", "#6d28d9", "#ddd6fe", "#241a3a", "#6d28d9"],
    ["#ecfeff", "#0891b2", "#a5f3fc", "#11313b", "#0891b2"],
  ];
  var colors = fallbackColors[index % fallbackColors.length];
  return {
    label: account.plan_type || "unknown",
    icon: "",
    color: colors[1],
    soft: colors[0],
    border: colors[2],
    track: "#f1f5f9",
    darkSoft: colors[3],
    darkBorder: colors[4],
    darkTrack: "#1e293b",
  };
}

export function planSoft(plan) {
  return isDarkMode() && plan.darkSoft ? plan.darkSoft : plan.soft;
}

export function planBorder(plan) {
  return isDarkMode() && plan.darkBorder ? plan.darkBorder : plan.border;
}

export function planTrack(plan) {
  return isDarkMode() && plan.darkTrack ? plan.darkTrack : plan.track;
}
