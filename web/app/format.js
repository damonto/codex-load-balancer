export function num(value) {
  var n = Number(value);
  return Number.isFinite(n) ? n : 0;
}

export function clamp(value, min, max) {
  return Math.min(max, Math.max(min, value));
}

export function fmt(value) {
  var n = num(value);
  if (Math.abs(n) >= 1000000) {
    return (n / 1000000).toFixed(1).replace(/\.0$/, "") + "M";
  }
  return new Intl.NumberFormat().format(Math.round(n));
}

export function fmtAxis(value) {
  var n = num(value);
  if (Math.abs(n) >= 1000000) return Math.round(n / 1000000) + "M";
  if (Math.abs(n) >= 1000) return Math.round(n / 1000) + "K";
  return String(Math.round(n));
}

export function fmtPercent(value) {
  return num(value).toFixed(1).replace(/\.0$/, "") + "%";
}

export function formatDateLabel(dateText) {
  var date = new Date(dateText + "T00:00:00Z");
  if (Number.isNaN(date.getTime())) return dateText;
  return date.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    timeZone: "UTC",
  });
}

export function formatGeneratedAt(value) {
  var date = new Date(value);
  if (Number.isNaN(date.getTime())) return "Generated time unavailable";
  return "Generated at " + date.toLocaleString();
}

export function formatResetAt(value) {
  if (!value) return "-";
  var date = new Date(value);
  if (Number.isNaN(date.getTime())) return "-";
  return date.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}
