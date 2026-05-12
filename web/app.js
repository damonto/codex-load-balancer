(function () {
  var THEME_QUERY = window.matchMedia
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
  var COLORS = themeColors();
  var TREND_LINE_WIDTH = 2;
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

  var state = {
    payload: null,
    accounts: [],
    trendDays: 30,
    sort: "total",
    search: "",
    trendChart: null,
    compositionChart: null,
  };

  function $(id) {
    return document.getElementById(id);
  }

  function isDarkMode() {
    return Boolean(THEME_QUERY && THEME_QUERY.matches);
  }

  function themeColors() {
    return isDarkMode() ? DARK_COLORS : LIGHT_COLORS;
  }

  function planSoft(plan) {
    return isDarkMode() && plan.darkSoft ? plan.darkSoft : plan.soft;
  }

  function planBorder(plan) {
    return isDarkMode() && plan.darkBorder ? plan.darkBorder : plan.border;
  }

  function planTrack(plan) {
    return isDarkMode() && plan.darkTrack ? plan.darkTrack : plan.track;
  }

  function esc(value) {
    return String(value == null ? "" : value)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }

  function num(value) {
    var n = Number(value);
    return Number.isFinite(n) ? n : 0;
  }

  function clamp(value, min, max) {
    return Math.min(max, Math.max(min, value));
  }

  function fmt(value) {
    var n = num(value);
    if (Math.abs(n) >= 1000000) {
      return (n / 1000000).toFixed(1).replace(/\.0$/, "") + "M";
    }
    return new Intl.NumberFormat().format(Math.round(n));
  }

  function fmtAxis(value) {
    var n = num(value);
    if (Math.abs(n) >= 1000000) return Math.round(n / 1000000) + "M";
    if (Math.abs(n) >= 1000) return Math.round(n / 1000) + "K";
    return String(Math.round(n));
  }

  function fmtPercent(value) {
    return num(value).toFixed(1).replace(/\.0$/, "") + "%";
  }

  function formatDateLabel(dateText) {
    var date = new Date(dateText + "T00:00:00Z");
    if (Number.isNaN(date.getTime())) return dateText;
    return date.toLocaleDateString(undefined, {
      month: "short",
      day: "numeric",
      timeZone: "UTC",
    });
  }

  function formatGeneratedAt(value) {
    var date = new Date(value);
    if (Number.isNaN(date.getTime())) return "Generated time unavailable";
    return "Generated at " + date.toLocaleString();
  }

  function displayAccountName(account) {
    var accountKey = String(account.account_key || "");
    return (
      account.email ||
      (accountKey.toLowerCase().endsWith(".json") ? "" : accountKey) ||
      account.account_id ||
      account.user_id ||
      "Unknown account"
    );
  }

  function accountInitial(account) {
    var source = displayAccountName(account).trim();
    return (source[0] || "?").toUpperCase();
  }

  function totalInput(account) {
    return num(account.input_tokens) + num(account.cached_tokens);
  }

  function compositionFor(
    inputTokens,
    cachedTokens,
    outputTokens,
    composition,
  ) {
    if (composition) return composition;
    var input = num(inputTokens);
    var cached = num(cachedTokens);
    var output = num(outputTokens);
    var total = input + cached + output;
    return {
      cached_input: {
        tokens: cached,
        percent: total ? (cached / total) * 100 : 0,
      },
      input: { tokens: input, percent: total ? (input / total) * 100 : 0 },
      output: { tokens: output, percent: total ? (output / total) * 100 : 0 },
    };
  }

  function part(comp, key) {
    return comp && comp[key] ? comp[key] : { tokens: 0, percent: 0 };
  }

  function periodCard(label, totals, emphasized) {
    totals = totals || {};
    var input = num(totals.input_tokens);
    var cached = num(totals.cached_tokens);
    var output = num(totals.output_tokens);
    var reasoning = num(totals.reasoning_tokens);
    var total = num(totals.total_tokens);
    var base = emphasized
      ? "bg-gradient-to-br from-[#fb527a] to-[#f0185b] text-white shadow-[0_16px_42px_rgba(240,24,91,0.28)]"
      : "surface-card border border-slate-200 bg-white text-[#251a2d] shadow-[0_18px_60px_rgba(15,23,42,0.06)]";
    var muted = emphasized ? "text-white/80" : "app-muted text-slate-500";
    var line = emphasized ? "border-white/15" : "subtle-border border-slate-100";
    return (
      "" +
      '<article class="min-h-33 rounded-2xl p-4 ' +
      base +
      '">' +
      '<div class="text-xs font-extrabold ' +
      muted +
      '">' +
      esc(label) +
      "</div>" +
      '<div class="mt-2 text-2xl font-black leading-none tracking-normal tabular-nums sm:text-3xl">' +
      esc(fmt(total)) +
      "</div>" +
      '<div class="mt-4 grid grid-cols-2 gap-3 border-t pt-3 ' +
      line +
      '">' +
      '<div><div class="text-xs font-semibold ' +
      muted +
      '">Input</div><div class="mt-1 text-lg font-black tabular-nums">' +
      esc(fmt(input)) +
      '</div><div class="mt-1 text-xs font-semibold tabular-nums ' +
      muted +
      '">Cached ' +
      esc(fmt(cached)) +
      "</div></div>" +
      '<div><div class="text-xs font-semibold ' +
      muted +
      '">Output</div><div class="mt-1 text-lg font-black tabular-nums">' +
      esc(fmt(output)) +
      '</div><div class="mt-1 text-xs font-semibold tabular-nums ' +
      muted +
      '">Reasoning ' +
      esc(fmt(reasoning)) +
      "</div></div>" +
      "</div>" +
      "</article>"
    );
  }

  function renderPeriodCards(payload) {
    $("periodCards").innerHTML = [
      periodCard("Today", payload.today, false),
      periodCard("Last 7 Days", payload.recent_7_days, false),
      periodCard("Last 30 Days", payload.recent_30_days, false),
      periodCard("Total", payload.total, true),
    ].join("");
  }

  function trendWindow(days) {
    var windows =
      state.payload && state.payload.trend
        ? state.payload.trend.windows || []
        : [];
    for (var i = 0; i < windows.length; i++) {
      if (num(windows[i].days) === days) return windows[i];
    }
    return { days: days, buckets: [] };
  }

  function renderTrendTabs() {
    var html = [7, 30, 90]
      .map(function (days) {
        var active = state.trendDays === days;
        var cls = active
          ? "trend-tab-active bg-rose-50 text-rose-600"
          : "trend-tab text-slate-500 hover:bg-slate-50 hover:text-slate-700";
        return (
          '<button type="button" data-trend-days="' +
          days +
          '" class="rounded-lg px-4 py-2 text-sm font-extrabold transition ' +
          cls +
          '">' +
          days +
          " Days</button>"
        );
      })
      .join("");
    $("trendTabs").innerHTML = html;
  }

  function trendFillGradient(ctx) {
    var gradient = ctx.createLinearGradient(0, 0, 0, 290);
    gradient.addColorStop(0, "rgba(225, 29, 72, 0.16)");
    gradient.addColorStop(1, "rgba(225, 29, 72, 0)");
    return gradient;
  }

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
      pointHoverRadius: 4,
      tension: 0.34,
      legendColor: cfg.legendColor || cfg.borderColor,
      legendDashed: Boolean(cfg.borderDash && cfg.borderDash.length),
    };
  }

  function renderTrendChart() {
    renderTrendTabs();
    if (!window.Chart) return;

    var canvas = $("trendChart");
    var ctx = canvas.getContext("2d");
    var buckets = trendWindow(state.trendDays).buckets || [];
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

    if (state.trendChart) state.trendChart.destroy();
    state.trendChart = new Chart(ctx, {
      type: "line",
      data: {
        labels: labels,
        datasets: [
          trendDataset({
            label: "Total",
            data: totalData,
            borderColor: COLORS.total,
            backgroundColor: trendFillGradient(ctx),
            fill: true,
            borderWidth: TREND_LINE_WIDTH,
          }),
          trendDataset({
            label: "Input",
            data: inputData,
            borderColor: COLORS.input,
            backgroundColor: COLORS.inputSoft,
            borderWidth: TREND_LINE_WIDTH,
          }),
          trendDataset({
            label: "Cached Input",
            data: cachedData,
            borderColor: COLORS.cached,
            backgroundColor: COLORS.cachedSoft,
            borderWidth: TREND_LINE_WIDTH,
          }),
          trendDataset({
            label: "Input (Non Cached)",
            data: nonCachedInputData,
            borderColor: COLORS.nonCachedInput,
            backgroundColor: COLORS.nonCachedInput,
            borderWidth: TREND_LINE_WIDTH,
          }),
          trendDataset({
            label: "Output",
            data: outputData,
            borderColor: COLORS.output,
            backgroundColor: COLORS.outputSoft,
            borderWidth: TREND_LINE_WIDTH,
          }),
          trendDataset({
            label: "Reasoning",
            data: reasoningData,
            borderColor: COLORS.reasoningLine,
            backgroundColor: COLORS.reasoning,
            borderWidth: TREND_LINE_WIDTH,
            borderDash: [2, 5],
            legendColor: COLORS.reasoning,
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
              boxWidth: 18,
              boxHeight: 8,
              color: COLORS.muted,
              font: { weight: 800 },
              padding: 14,
            },
          },
          tooltip: {
            backgroundColor: COLORS.tooltipBg,
            titleColor: COLORS.text,
            bodyColor: COLORS.text,
            borderColor: COLORS.tooltipBorder,
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
              color: COLORS.muted,
              maxTicksLimit: 7,
              font: { weight: 700 },
            },
          },
          y: {
            beginAtZero: true,
            border: { display: false },
            grid: { color: COLORS.grid },
            ticks: {
              color: COLORS.muted,
              callback: fmtAxis,
              font: { weight: 700 },
            },
          },
        },
      },
    });
  }

  function renderComposition(payload) {
    var comp = compositionFor(
      payload.total && payload.total.input_tokens,
      payload.total && payload.total.cached_tokens,
      payload.total && payload.total.output_tokens,
      payload.composition,
    );
    var cached = part(comp, "cached_input");
    var input = part(comp, "input");
    var output = part(comp, "output");
    var total = num(cached.tokens) + num(input.tokens) + num(output.tokens);

    $("compositionCenter").innerHTML =
      '<div><div class="text-base font-extrabold">Total</div><div class="mt-1 text-xl font-black tabular-nums">' +
      esc(fmt(total)) +
      "</div></div>";
    $("compositionLegend").innerHTML = [
      compositionLegendRow("Cached Input", cached, COLORS.cached),
      compositionLegendRow("Input (Non-cached)", input, COLORS.input),
      compositionLegendRow("Output", output, COLORS.output),
    ].join("");

    if (!window.Chart) return;
    var ctx = $("compositionChart").getContext("2d");
    if (state.compositionChart) state.compositionChart.destroy();
    state.compositionChart = new Chart(ctx, {
      type: "doughnut",
      data: {
        labels:
          total > 0
            ? ["Cached Input", "Input (Non-cached)", "Output"]
            : ["No usage"],
        datasets: [
          {
            data:
              total > 0 ? [cached.tokens, input.tokens, output.tokens] : [1],
            backgroundColor:
              total > 0
                ? [COLORS.cached, COLORS.input, COLORS.output]
                : [COLORS.doughnutEmpty],
            borderColor: COLORS.chartSurface,
            borderWidth: 2,
            hoverOffset: 4,
          },
        ],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        cutout: "56%",
        plugins: {
          legend: { display: false },
          tooltip: {
            backgroundColor: COLORS.tooltipBg,
            titleColor: COLORS.text,
            bodyColor: COLORS.text,
            borderColor: COLORS.tooltipBorder,
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

  function compositionLegendRow(label, data, color) {
    return (
      "" +
      '<div class="grid grid-cols-[14px_1fr] gap-3">' +
      '<span class="mt-1.5 size-3 rounded-full" style="background:' +
      esc(color) +
      '"></span>' +
      "<div>" +
      '<div class="app-text text-sm font-extrabold text-[#251a2d]">' +
      esc(label) +
      "</div>" +
      '<div class="app-text mt-1 text-base font-black tabular-nums text-[#251a2d]">' +
      esc(fmt(data.tokens)) +
      ' <span class="app-muted font-semibold text-slate-500">(' +
      esc(fmtPercent(data.percent)) +
      ")</span></div>" +
      "</div>" +
      "</div>"
    );
  }

  function quotaProgress(used, limit) {
    if (!limit || limit <= 0) return 0;
    return clamp((used / limit) * 100, 0, 100);
  }

  function formatResetAt(value) {
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

  function windowBar(label, hasQuota, used, limit, resetAt) {
    var pct = hasQuota ? quotaProgress(num(used), num(limit)) : 0;
    var barColor = pct >= 100 ? "#f43f5e" : "#fb527a";
    return (
      "" +
      '<div class="grid gap-1.5">' +
      '<div class="app-muted flex items-center justify-between gap-3 text-xs font-bold text-slate-500">' +
      "<span>" +
      esc(label) +
      '</span><span class="text-right">' +
      esc(formatResetAt(resetAt)) +
      "</span>" +
      "</div>" +
      '<div class="flex items-center gap-2">' +
      '<div class="progress-track h-1.5 flex-1 overflow-hidden rounded-full bg-rose-100">' +
      '<span class="block h-full rounded-full" style="width:' +
      pct.toFixed(1) +
      "%;background:" +
      barColor +
      '"></span>' +
      "</div>" +
      '<span class="app-muted w-9 text-right text-xs font-extrabold text-slate-500">' +
      (hasQuota ? Math.round(pct) + "%" : "-") +
      "</span>" +
      "</div>" +
      "</div>"
    );
  }

  function accountTokens(account) {
    return Array.isArray(account.token_ids) ? account.token_ids : [];
  }

  function accountSearchText(account) {
    return [
      account.account_key,
      account.user_id,
      account.account_id,
      account.email,
      account.plan_type,
    ]
      .concat(accountTokens(account))
      .join(" ")
      .toLowerCase();
  }

  function sortValue(account) {
    if (state.sort === "input") return totalInput(account);
    if (state.sort === "output") return num(account.output_tokens);
    if (state.sort === "cached") return num(account.cached_tokens);
    return num(account.total_tokens);
  }

  function filteredAccounts() {
    var search = state.search.trim().toLowerCase();
    var accounts = state.accounts.filter(function (account) {
      return !search || accountSearchText(account).indexOf(search) !== -1;
    });
    accounts.sort(function (a, b) {
      var diff = sortValue(b) - sortValue(a);
      if (diff !== 0) return diff;
      return displayAccountName(a).localeCompare(displayAccountName(b));
    });
    return accounts;
  }

  function renderAccounts() {
    var accounts = filteredAccounts();
    $("accountHeading").textContent = "Accounts (" + accounts.length + ")";
    $("accountsEmpty").classList.toggle("hidden", accounts.length !== 0);
    $("accountCards").innerHTML = accounts.map(accountCard).join("");
  }

  function normalizePlanType(value) {
    return String(value || "")
      .toLowerCase()
      .replace(/[\s_-]+/g, "");
  }

  function planFor(account, index) {
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

  function planIcon(account, plan) {
    if (plan.icon) {
      return (
        '<img class="size-9 shrink-0 object-contain sm:size-10" src="' +
        esc(plan.icon) +
        '" alt="' +
        esc(plan.label) +
        ' plan icon" loading="lazy">'
      );
    }
    return (
      '<div class="grid size-9 shrink-0 place-items-center rounded-lg text-base font-black sm:size-10" style="background:' +
      esc(planSoft(plan)) +
      ";color:" +
      esc(plan.color) +
      ";border:1px solid " +
      esc(planBorder(plan)) +
      '">' +
      esc(accountInitial(account)) +
      "</div>"
    );
  }

  function planBadge(plan) {
    return (
      '<span class="inline-flex h-5 shrink-0 items-center rounded-md px-1.5 text-[10px] font-black uppercase leading-none" style="background:' +
      esc(planSoft(plan)) +
      ";color:" +
      esc(plan.color) +
      ";border:1px solid " +
      esc(planBorder(plan)) +
      '">' +
      esc(String(plan.label || "unknown").toUpperCase()) +
      "</span>"
    );
  }

  function metricBlock(label, value, subtext, alignRight, color) {
    return (
      '<div class="' +
      (alignRight ? "text-right" : "") +
      '">' +
      '<div class="app-muted text-[11px] font-extrabold leading-tight text-slate-500">' +
      esc(label) +
      "</div>" +
      '<div class="mt-1 text-sm font-black leading-none tracking-normal tabular-nums" style="color:' +
      esc(color || COLORS.text) +
      '">' +
      esc(value) +
      "</div>" +
      (subtext
        ? '<div class="app-subtle mt-1 truncate text-[11px] font-bold leading-none text-slate-400 tabular-nums">' +
          esc(subtext) +
          "</div>"
        : "") +
      "</div>"
    );
  }

  function quotaCard(label, hasQuota, used, limit, resetAt, plan) {
    var pct = hasQuota ? quotaProgress(num(used), num(limit)) : 0;
    var width = pct > 0 && pct < 1 ? "0.8" : pct.toFixed(1);
    var pctText = hasQuota ? fmtPercent(pct) : "-";
    var detail = hasQuota ? fmt(used) + " / " + fmt(limit) : "No quota data";
    var reset = hasQuota ? "Reset " + formatResetAt(resetAt) : "";

    return (
      "" +
      '<div class="grid gap-1.5">' +
      '<div class="app-muted flex items-center justify-between gap-2 text-xs font-extrabold text-slate-500">' +
      "<span>" +
      esc(label) +
      '</span><span class="app-text text-[#251a2d] tabular-nums">' +
      esc(pctText) +
      "</span>" +
      "</div>" +
      '<div class="h-1.5 overflow-hidden rounded-full" style="background:' +
      esc(planTrack(plan)) +
      '">' +
      '<span class="block h-full rounded-full" style="width:' +
      width +
      "%;background:" +
      esc(plan.color) +
      '"></span>' +
      "</div>" +
      '<div class="app-muted flex items-center justify-between gap-2 text-[11px] font-bold leading-tight text-slate-500">' +
      '<span class="tabular-nums">' +
      esc(detail) +
      "</span><span>" +
      esc(reset) +
      "</span>" +
      "</div>" +
      "</div>"
    );
  }

  function compositionBreakdown(cached, input, output) {
    var cachedPct = clamp(num(cached.percent), 0, 100);
    var inputPct = clamp(num(input.percent), 0, 100);
    var outputPct = clamp(num(output.percent), 0, 100);
    var items = [
      ["Cached", cachedPct, COLORS.cached],
      ["Input", inputPct, COLORS.input],
      ["Output", outputPct, COLORS.output],
    ];
    return (
      '<div class="subtle-border grid grid-cols-3 gap-2 border-t border-slate-100 pt-3 text-center">' +
      items
        .map(function (item) {
          return (
            '<div><div class="text-sm font-black tabular-nums" style="color:' +
            esc(item[2]) +
            '">' +
            esc(fmtPercent(item[1])) +
            '</div><div class="app-muted mt-1 text-[11px] font-extrabold text-slate-500">' +
            esc(item[0]) +
            "</div></div>"
          );
        })
        .join("") +
      "</div>"
    );
  }

  function accountCard(account, index) {
    var comp = compositionFor(
      account.input_tokens,
      account.cached_tokens,
      account.output_tokens,
      account.composition,
    );
    var cached = part(comp, "cached_input");
    var input = part(comp, "input");
    var output = part(comp, "output");
    var total = num(account.total_tokens);
    var plan = planFor(account, index);

    return (
      "" +
      '<article class="surface-card rounded-xl border border-slate-200 bg-white p-3.5 text-[#251a2d] shadow-[0_10px_34px_rgba(15,23,42,0.045)]">' +
      '<div class="flex min-w-0 items-center gap-2.5 sm:gap-3">' +
      planIcon(account, plan) +
      '<div class="flex min-w-0 flex-1 items-center gap-1.5">' +
      '<div class="app-text min-w-0 truncate text-sm font-black tracking-normal" title="' +
      esc(displayAccountName(account)) +
      '">' +
      esc(displayAccountName(account)) +
      "</div>" +
      '<span class="app-muted shrink-0 text-sm font-black leading-none">&#183;</span>' +
      planBadge(plan) +
      "</div>" +
      "</div>" +
      '<div class="mt-4 grid grid-cols-3 gap-2 rounded-lg border p-2" style="background:' +
      esc(planSoft(plan)) +
      ";border-color:" +
      esc(planBorder(plan)) +
      '">' +
      metricBlock("Total Usage", fmt(total), "", false) +
      metricBlock(
        "Input",
        fmt(totalInput(account)),
        "Cached " + fmt(account.cached_tokens),
        false,
        plan.color,
      ) +
      metricBlock("Output", fmt(account.output_tokens), "", true) +
      "</div>" +
      '<div class="mt-4 grid gap-3">' +
      quotaCard(
        "5 Hour Limit",
        Boolean(account.has_5h_quota),
        account.used_5h_tokens,
        account.quota_5h_tokens,
        account.five_hour_reset_at,
        plan,
      ) +
      quotaCard(
        "Weekly Limit",
        Boolean(account.has_week_quota),
        account.used_week_tokens,
        account.quota_week_tokens,
        account.weekly_reset_at,
        plan,
      ) +
      "</div>" +
      '<div class="mt-3.5">' +
      compositionBreakdown(cached, input, output) +
      "</div>" +
      "</article>"
    );
  }

  function render(payload) {
    COLORS = themeColors();
    state.payload = payload || {};
    state.accounts = Array.isArray(state.payload.accounts)
      ? state.payload.accounts
      : [];
    renderPeriodCards(state.payload);
    renderTrendChart();
    renderComposition(state.payload);
    renderAccounts();
  }

  async function loadOverview() {
    var refreshBtn = $("refreshBtn");
    $("meta").textContent = "Loading...";
    refreshBtn.disabled = true;
    try {
      var resp = await fetch("stats/overview");
      if (!resp.ok)
        throw new Error((await resp.text()).trim() || resp.statusText);
      var payload = await resp.json();
      render(payload);
      $("meta").textContent = formatGeneratedAt(payload.generated_at);
    } catch (err) {
      $("meta").textContent = "Load failed: " + err.message;
    } finally {
      refreshBtn.disabled = false;
    }
  }

  function bindEvents() {
    $("refreshBtn").addEventListener("click", loadOverview);
    $("trendTabs").addEventListener("click", function (event) {
      var btn = event.target.closest("[data-trend-days]");
      if (!btn) return;
      state.trendDays = num(btn.getAttribute("data-trend-days"));
      renderTrendChart();
    });
    $("sortSelect").addEventListener("change", function (event) {
      state.sort = event.target.value;
      renderAccounts();
    });
    $("searchInput").addEventListener("input", function (event) {
      state.search = event.target.value;
      renderAccounts();
    });
    if (THEME_QUERY) {
      var onThemeChange = function () {
        COLORS = themeColors();
        if (state.payload) render(state.payload);
      };
      if (THEME_QUERY.addEventListener) {
        THEME_QUERY.addEventListener("change", onThemeChange);
      } else if (THEME_QUERY.addListener) {
        THEME_QUERY.addListener(onThemeChange);
      }
    }
  }

  bindEvents();
  loadOverview();
})();
