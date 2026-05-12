import {
  clamp,
  fmt,
  fmtPercent,
  formatGeneratedAt,
  formatResetAt,
  num,
} from "./format.js";
import { planBorder, planFor, planSoft, planTrack } from "./plans.js";
import { THEME_QUERY, themeColors } from "./theme.js";
import {
  accountInitial,
  accountTokens,
  compositionFor,
  displayAccountName,
  part,
  quotaProgress,
  totalInput,
} from "./usage.js";
import { createCompositionChart, createTrendChart } from "./charts.js";

export function dashboard() {
  return {
    payload: null,
    accounts: [],
    trendDays: 30,
    trendOptions: [7, 30, 90],
    compositionWindow: "total",
    sort: "total",
    search: "",
    trendChart: null,
    compositionChart: null,
    colors: themeColors(),
    meta: "Loading...",
    loading: false,

    init: function () {
      this.$watch("trendDays", this.renderTrendChart.bind(this));
      this.$watch("compositionWindow", this.renderCompositionChart.bind(this));
      this.loadOverview();
      this.bindThemeChanges();
    },

    fmt: fmt,
    fmtPercent: fmtPercent,
    formatResetAt: formatResetAt,
    displayAccountName: displayAccountName,
    totalInput: totalInput,
    planFor: planFor,
    accountInitial: accountInitial,

    loadOverview: async function () {
      this.meta = "Loading...";
      this.loading = true;
      try {
        var resp = await fetch("stats/overview");
        if (!resp.ok) {
          throw new Error((await resp.text()).trim() || resp.statusText);
        }
        this.payload = await resp.json();
        this.accounts = Array.isArray(this.payload.accounts)
          ? this.payload.accounts
          : [];
        this.colors = themeColors();
        this.meta = formatGeneratedAt(this.payload.generated_at);
        this.renderCharts();
      } catch (err) {
        this.meta = "Load failed: " + err.message;
      } finally {
        this.loading = false;
      }
    },

    bindThemeChanges: function () {
      if (!THEME_QUERY) return;
      var component = this;
      var onThemeChange = function () {
        component.colors = themeColors();
        component.renderCharts();
      };
      if (THEME_QUERY.addEventListener) {
        THEME_QUERY.addEventListener("change", onThemeChange);
        return;
      }
      if (THEME_QUERY.addListener) {
        THEME_QUERY.addListener(onThemeChange);
      }
    },

    periodCards: function () {
      var payload = this.payload || {};
      return [
        { label: "Today", totals: payload.today, emphasized: false },
        {
          label: "Last 7 Days",
          totals: payload.recent_7_days,
          emphasized: false,
        },
        {
          label: "Last 30 Days",
          totals: payload.recent_30_days,
          emphasized: false,
        },
        { label: "Total", totals: payload.total, emphasized: true },
      ].map(function (card) {
        var totals = card.totals || {};
        return {
          label: card.label,
          emphasized: card.emphasized,
          input: num(totals.input_tokens),
          cached: num(totals.cached_tokens),
          output: num(totals.output_tokens),
          reasoning: num(totals.reasoning_tokens),
          total: num(totals.total_tokens),
        };
      });
    },

    periodCardClass: function (card) {
      if (card.emphasized) {
        return "bg-gradient-to-br from-[#fb527a] to-[#f0185b] text-white shadow-[0_16px_42px_rgba(240,24,91,0.28)]";
      }
      return "surface-card border border-slate-200 bg-white text-[#251a2d] shadow-[0_18px_60px_rgba(15,23,42,0.06)]";
    },

    periodMutedClass: function (card) {
      return card.emphasized ? "text-white/80" : "app-muted text-slate-500";
    },

    periodLineClass: function (card) {
      return card.emphasized
        ? "border-white/15"
        : "subtle-border border-slate-100";
    },

    trendTabClass: function (days) {
      if (this.trendDays === days) {
        return "trend-tab-active bg-rose-50 text-rose-600";
      }
      return "trend-tab text-slate-500 hover:bg-slate-50 hover:text-slate-700";
    },

    setTrendDays: function (days) {
      this.trendDays = num(days);
    },

    trendWindow: function (days) {
      var windows =
        this.payload && this.payload.trend ? this.payload.trend.windows || [] : [];
      for (var i = 0; i < windows.length; i++) {
        if (num(windows[i].days) === days) return windows[i];
      }
      return { days: days, buckets: [] };
    },

    compositionWindows: function () {
      return [
        {
          key: "total",
          label: "Total",
          totals: this.payload && this.payload.total,
        },
        {
          key: "30d",
          label: "Last 30 days",
          totals: this.payload && this.payload.recent_30_days,
        },
        {
          key: "7d",
          label: "Last 7 days",
          totals: this.payload && this.payload.recent_7_days,
        },
        {
          key: "today",
          label: "Today",
          totals: this.payload && this.payload.today,
        },
      ];
    },

    selectedCompositionWindow: function () {
      var windows = this.compositionWindows();
      for (var i = 0; i < windows.length; i++) {
        if (windows[i].key === this.compositionWindow) return windows[i];
      }
      return windows[0];
    },

    compositionParts: function () {
      var selected = this.selectedCompositionWindow();
      var totals = selected.totals || {};
      var comp = compositionFor(
        totals.input_tokens,
        totals.cached_tokens,
        totals.output_tokens,
        this.compositionWindow === "total" && this.payload
          ? this.payload.composition
          : null,
      );
      return {
        cached: part(comp, "cached_input"),
        input: part(comp, "input"),
        output: part(comp, "output"),
      };
    },

    compositionTotal: function () {
      var parts = this.compositionParts();
      return (
        num(parts.cached.tokens) +
        num(parts.input.tokens) +
        num(parts.output.tokens)
      );
    },

    compositionRows: function () {
      var parts = this.compositionParts();
      return [
        { label: "Input", data: parts.input, color: this.colors.input },
        {
          label: "Cached Input",
          data: parts.cached,
          color: this.colors.cached,
        },
        { label: "Output", data: parts.output, color: this.colors.output },
      ];
    },

    compositionBreakdownRows: function (account) {
      var comp = compositionFor(
        account.input_tokens,
        account.cached_tokens,
        account.output_tokens,
        account.composition,
      );
      return [
        {
          label: "Cached",
          percent: clamp(num(part(comp, "cached_input").percent), 0, 100),
          color: this.colors.cached,
        },
        {
          label: "Input",
          percent: clamp(num(part(comp, "input").percent), 0, 100),
          color: this.colors.input,
        },
        {
          label: "Output",
          percent: clamp(num(part(comp, "output").percent), 0, 100),
          color: this.colors.output,
        },
      ];
    },

    filteredAccounts: function () {
      var component = this;
      var search = this.search.trim().toLowerCase();
      var accounts = this.accounts.filter(function (account) {
        return (
          !search ||
          [
            account.account_key,
            account.user_id,
            account.account_id,
            account.email,
            account.plan_type,
          ]
            .concat(accountTokens(account))
            .join(" ")
            .toLowerCase()
            .indexOf(search) !== -1
        );
      });
      accounts.sort(function (a, b) {
        var diff = component.sortValue(b) - component.sortValue(a);
        if (diff !== 0) return diff;
        return displayAccountName(a).localeCompare(displayAccountName(b));
      });
      return accounts;
    },

    sortValue: function (account) {
      if (this.sort === "input") return totalInput(account);
      if (this.sort === "output") return num(account.output_tokens);
      if (this.sort === "cached") return num(account.cached_tokens);
      return num(account.total_tokens);
    },

    planIconStyle: function (account, index) {
      var plan = planFor(account, index);
      return (
        "background:" +
        planSoft(plan) +
        ";color:" +
        plan.color +
        ";border:1px solid " +
        planBorder(plan)
      );
    },

    planBadgeStyle: function (account, index) {
      var plan = planFor(account, index);
      return (
        "background:" +
        planSoft(plan) +
        ";color:" +
        plan.color +
        ";border:1px solid " +
        planBorder(plan)
      );
    },

    accountMetricStyle: function (account, index, color) {
      var plan = planFor(account, index);
      return "color:" + (color || plan.color || this.colors.text);
    },

    accountSummaryStyle: function (account, index) {
      var plan = planFor(account, index);
      return (
        "background:" + planSoft(plan) + ";border-color:" + planBorder(plan)
      );
    },

    quota: function (account, windowName) {
      if (windowName === "weekly") {
        return {
          label: "Weekly Limit",
          hasQuota: Boolean(account.has_week_quota),
          used: num(account.used_week_tokens),
          limit: num(account.quota_week_tokens),
          resetAt: account.weekly_reset_at,
        };
      }
      return {
        label: "5 Hour Limit",
        hasQuota: Boolean(account.has_5h_quota),
        used: num(account.used_5h_tokens),
        limit: num(account.quota_5h_tokens),
        resetAt: account.five_hour_reset_at,
      };
    },

    quotaPercent: function (quota) {
      return quota.hasQuota ? quotaProgress(quota.used, quota.limit) : 0;
    },

    quotaPercentText: function (quota) {
      return quota.hasQuota ? fmtPercent(this.quotaPercent(quota)) : "-";
    },

    quotaDetail: function (quota) {
      return quota.hasQuota
        ? fmt(quota.used) + " / " + fmt(quota.limit)
        : "No quota data";
    },

    quotaResetText: function (quota) {
      return quota.hasQuota ? "Reset " + formatResetAt(quota.resetAt) : "";
    },

    quotaTrackStyle: function (account, index) {
      return "background:" + planTrack(planFor(account, index));
    },

    quotaFillStyle: function (account, index, quota) {
      var pct = this.quotaPercent(quota);
      var width = pct > 0 && pct < 1 ? "0.8" : pct.toFixed(1);
      return "width:" + width + "%;background:" + planFor(account, index).color;
    },

    renderCharts: function () {
      this.renderTrendChart();
      this.renderCompositionChart();
    },

    renderTrendChart: function () {
      if (!window.Chart || !this.payload) return;
      var canvas = document.getElementById("trendChart");
      if (!canvas) return;
      if (this.trendChart) this.trendChart.destroy();
      this.trendChart = createTrendChart(
        canvas,
        this.trendWindow(this.trendDays).buckets || [],
        this.colors,
      );
    },

    renderCompositionChart: function () {
      if (!window.Chart || !this.payload) return;
      var canvas = document.getElementById("compositionChart");
      if (!canvas) return;
      var parts = this.compositionParts();
      if (this.compositionChart) this.compositionChart.destroy();
      this.compositionChart = createCompositionChart(
        canvas,
        parts,
        this.compositionTotal(),
        this.colors,
      );
    },
  };
}
