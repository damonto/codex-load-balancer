import { fmt, fmtAxis, formatDateLabel, num } from "./format.js";

var TREND_LINE_WIDTH = 1.75;

function trendDataset(cfg) {
  return {
    label: cfg.label,
    data: cfg.data,
    borderColor: cfg.borderColor,
    backgroundColor: cfg.backgroundColor || cfg.borderColor,
    fill: Boolean(cfg.fill),
    borderWidth: cfg.borderWidth,
    borderDash: cfg.borderDash || [],
    pointRadius: 0,
    pointHoverRadius: 3,
    tension: 0.34,
  };
}

function trendFillGradient(ctx) {
  var gradient = ctx.createLinearGradient(0, 0, 0, 290);
  gradient.addColorStop(0, "rgba(225, 29, 72, 0.16)");
  gradient.addColorStop(1, "rgba(225, 29, 72, 0)");
  return gradient;
}

export function createTrendChart(canvas, buckets, colors) {
  var ctx = canvas.getContext("2d");
  var labels = buckets.map(function (bucket) {
    return formatDateLabel(bucket.date);
  });
  var totalData = buckets.map(function (bucket) {
    return (
      num(bucket.input_tokens) +
      num(bucket.cached_tokens) +
      num(bucket.output_tokens)
    );
  });
  var inputData = buckets.map(function (bucket) {
    return num(bucket.input_tokens) + num(bucket.cached_tokens);
  });
  var cachedData = buckets.map(function (bucket) {
    return num(bucket.cached_tokens);
  });
  var nonCachedInputData = buckets.map(function (bucket) {
    return num(bucket.input_tokens);
  });
  var outputData = buckets.map(function (bucket) {
    return num(bucket.output_tokens);
  });
  var reasoningData = buckets.map(function (bucket) {
    return num(bucket.reasoning_tokens);
  });

  return new Chart(ctx, {
    type: "line",
    data: {
      labels: labels,
      datasets: [
        trendDataset({
          label: "Total",
          data: totalData,
          borderColor: colors.total,
          backgroundColor: trendFillGradient(ctx),
          fill: true,
          borderWidth: TREND_LINE_WIDTH,
        }),
        trendDataset({
          label: "Input",
          data: inputData,
          borderColor: colors.input,
          backgroundColor: colors.inputSoft,
          borderWidth: TREND_LINE_WIDTH,
        }),
        trendDataset({
          label: "Cached Input",
          data: cachedData,
          borderColor: colors.cached,
          backgroundColor: colors.cachedSoft,
          borderWidth: TREND_LINE_WIDTH,
        }),
        trendDataset({
          label: "Input (Non Cached)",
          data: nonCachedInputData,
          borderColor: colors.nonCachedInput,
          backgroundColor: colors.nonCachedInput,
          borderWidth: TREND_LINE_WIDTH,
        }),
        trendDataset({
          label: "Output",
          data: outputData,
          borderColor: colors.output,
          backgroundColor: colors.outputSoft,
          borderWidth: TREND_LINE_WIDTH,
        }),
        trendDataset({
          label: "Reasoning",
          data: reasoningData,
          borderColor: colors.reasoningLine,
          backgroundColor: colors.reasoning,
          borderWidth: TREND_LINE_WIDTH,
          borderDash: [2, 5],
        }),
      ],
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      interaction: { mode: "index", intersect: false },
      plugins: {
        legend: {
          display: true,
          position: "top",
          align: "end",
          labels: {
            boxWidth: 14,
            boxHeight: 7,
            color: colors.muted,
            font: { size: 12, weight: 800 },
            padding: 10,
          },
        },
        tooltip: {
          backgroundColor: colors.tooltipBg,
          titleColor: colors.tooltipText,
          bodyColor: colors.tooltipText,
          footerColor: colors.tooltipText,
          borderColor: colors.tooltipBorder,
          borderWidth: 1,
          callbacks: {
            label: function (item) {
              return item.dataset.label + ": " + fmt(item.raw);
            },
          },
        },
      },
      scales: {
        x: {
          border: { display: false },
          grid: { display: false },
          ticks: {
            color: colors.muted,
            maxTicksLimit: 7,
            font: { size: 12, weight: 700 },
          },
        },
        y: {
          beginAtZero: true,
          border: { display: false },
          grid: { color: colors.grid, lineWidth: 0.75 },
          ticks: {
            color: colors.muted,
            callback: fmtAxis,
            font: { size: 12, weight: 700 },
          },
        },
      },
    },
  });
}

export function createCompositionChart(canvas, parts, total, colors) {
  var ctx = canvas.getContext("2d");
  return new Chart(ctx, {
    type: "doughnut",
    data: {
      labels: total > 0 ? ["Input", "Cached Input", "Output"] : ["No usage"],
      datasets: [
        {
          data:
            total > 0
              ? [parts.input.tokens, parts.cached.tokens, parts.output.tokens]
              : [1],
          backgroundColor:
            total > 0
              ? [colors.input, colors.cached, colors.output]
              : [colors.doughnutEmpty],
          borderColor: colors.chartSurface,
          borderWidth: 1.5,
          hoverOffset: 2,
        },
      ],
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      cutout: "58%",
      plugins: {
        legend: { display: false },
        tooltip: {
          backgroundColor: colors.tooltipBg,
          titleColor: colors.tooltipText,
          bodyColor: colors.tooltipText,
          footerColor: colors.tooltipText,
          borderColor: colors.tooltipBorder,
          borderWidth: 1,
          callbacks: {
            label: function (item) {
              if (total <= 0) return "No usage";
              return item.label + ": " + fmt(item.raw);
            },
          },
        },
      },
    },
  });
}
