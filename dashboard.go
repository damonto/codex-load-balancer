package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"time"
)

//go:embed web/app.js web/app/*.js web/index.html web/tailwind.css web/icon.png web/plan-icons/*.png
var dashboardFiles embed.FS

var dashboardWebFS = mustDashboardWebFS()
var dashboardAssetHandler = http.StripPrefix("/stats/assets/", http.FileServer(http.FS(dashboardWebFS)))
var indexHTML = mustReadDashboardFile("index.html")

type dashboardOverviewResponse struct {
	GeneratedAt  time.Time            `json:"generated_at"`
	Today        dashboardTotals      `json:"today"`
	Recent7Days  dashboardTotals      `json:"recent_7_days"`
	Recent30Days dashboardTotals      `json:"recent_30_days"`
	Recent90Days dashboardTotals      `json:"recent_90_days"`
	Total        dashboardTotals      `json:"total"`
	Composition  dashboardComposition `json:"composition"`
	Trend        dashboardTrend       `json:"trend"`
	Accounts     []dashboardAccount   `json:"accounts"`
}

type dashboardTotals struct {
	InputTokens     int64 `json:"input_tokens"`
	CachedTokens    int64 `json:"cached_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
	TotalTokens     int64 `json:"total_tokens"`
}

type dashboardComposition struct {
	CachedInput dashboardCompositionPart `json:"cached_input"`
	Input       dashboardCompositionPart `json:"input"`
	Output      dashboardCompositionPart `json:"output"`
}

type dashboardCompositionPart struct {
	Tokens  int64   `json:"tokens"`
	Percent float64 `json:"percent"`
}

type dashboardTrend struct {
	Windows []dashboardTrendWindow `json:"windows"`
}

type dashboardTrendWindow struct {
	Days    int                    `json:"days"`
	Buckets []dashboardTrendBucket `json:"buckets"`
}

type dashboardTrendBucket struct {
	Date            string `json:"date"`
	InputTokens     int64  `json:"input_tokens"`
	CachedTokens    int64  `json:"cached_tokens"`
	OutputTokens    int64  `json:"output_tokens"`
	ReasoningTokens int64  `json:"reasoning_tokens"`
	TotalTokens     int64  `json:"total_tokens"`
}

type dashboardAccount struct {
	AccountKey      string               `json:"account_key"`
	UserID          string               `json:"user_id"`
	AccountID       string               `json:"account_id"`
	Email           string               `json:"email"`
	PlanType        string               `json:"plan_type"`
	TokenIDs        []string             `json:"token_ids"`
	InputTokens     int64                `json:"input_tokens"`
	CachedTokens    int64                `json:"cached_tokens"`
	OutputTokens    int64                `json:"output_tokens"`
	ReasoningTokens int64                `json:"reasoning_tokens"`
	TotalTokens     int64                `json:"total_tokens"`
	Composition     dashboardComposition `json:"composition"`
	Used5hTokens    int64                `json:"used_5h_tokens"`
	Quota5hTokens   int64                `json:"quota_5h_tokens"`
	Has5hQuota      bool                 `json:"has_5h_quota"`
	FiveHourResetAt *time.Time           `json:"five_hour_reset_at"`
	UsedWeekTokens  int64                `json:"used_week_tokens"`
	QuotaWeekTokens int64                `json:"quota_week_tokens"`
	HasWeekQuota    bool                 `json:"has_week_quota"`
	WeeklyResetAt   *time.Time           `json:"weekly_reset_at"`
}

func handleDashboardAsset(w http.ResponseWriter, r *http.Request) {
	dashboardAssetHandler.ServeHTTP(w, r)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(indexHTML); err != nil {
		slog.Warn("write dashboard page", "err", err)
	}
}

func mustDashboardWebFS() fs.FS {
	webFS, err := fs.Sub(dashboardFiles, "web")
	if err != nil {
		panic(fmt.Sprintf("sub dashboard web fs: %v", err))
	}
	return webFS
}

func mustReadDashboardFile(name string) []byte {
	content, err := fs.ReadFile(dashboardWebFS, name)
	if err != nil {
		panic(fmt.Sprintf("read dashboard file %q: %v", name, err))
	}
	return content
}

func (s *Server) handleDashboardOverview(w http.ResponseWriter, r *http.Request) {
	if s.usageDB == nil {
		http.Error(w, "usage database is not configured", http.StatusServiceUnavailable)
		return
	}

	periods, err := s.usageDB.GlobalPeriodTotals(r.Context())
	if err != nil {
		slog.Warn("query global period totals", "err", err)
		http.Error(w, "query global period totals", http.StatusInternalServerError)
		return
	}
	dailyBuckets, err := s.usageDB.DailyUsageBuckets(r.Context(), 90)
	if err != nil {
		slog.Warn("query daily usage buckets", "err", err)
		http.Error(w, "query daily usage buckets", http.StatusInternalServerError)
		return
	}

	activeAccounts := s.activeAccountTokens()
	accountQuotaSnapshots := s.store.AccountQuotaSnapshots()
	accountInfos := s.store.AccountInfos()
	summaries, err := s.usageDB.ListAccountSummaries(r.Context(), activeAccounts, 0, 0)
	if err != nil {
		slog.Warn("query account summaries", "err", err)
		http.Error(w, "query account summaries", http.StatusInternalServerError)
		return
	}

	accounts := make([]dashboardAccount, 0, len(summaries))
	for _, summary := range summaries {
		// Skip stale usage-only accounts whose token files are gone.
		if len(summary.ActiveTokenIDs) == 0 {
			continue
		}

		info := accountInfos[summary.AccountKey]
		account := dashboardAccount{
			AccountKey:      summary.AccountKey,
			UserID:          info.UserID,
			AccountID:       info.AccountID,
			Email:           info.Email,
			PlanType:        info.PlanType,
			TokenIDs:        summary.ActiveTokenIDs,
			InputTokens:     summary.InputTokens,
			CachedTokens:    summary.CachedTokens,
			OutputTokens:    summary.OutputTokens,
			ReasoningTokens: summary.ReasoningTokens,
			TotalTokens:     summary.TotalTokens(),
			Composition:     compositionFromTotals(summary.InputTokens, summary.CachedTokens, summary.OutputTokens),
		}
		if quota, ok := accountQuotaSnapshots[summary.AccountKey]; ok {
			if quota.HasFiveHour {
				account.Has5hQuota = true
				account.Used5hTokens = int64(math.Round(quota.FiveHourUsed))
				account.Quota5hTokens = int64(math.Round(quota.FiveHourMax))
				account.FiveHourResetAt = optionalTime(quota.FiveHourResetAt)
			}
			if quota.HasWeekly {
				account.HasWeekQuota = true
				account.UsedWeekTokens = int64(math.Round(quota.WeeklyUsed))
				account.QuotaWeekTokens = int64(math.Round(quota.WeeklyMax))
				account.WeeklyResetAt = optionalTime(quota.WeeklyResetAt)
			}
			if !account.Has5hQuota && account.HasWeekQuota {
				account.Has5hQuota = true
				account.Used5hTokens = account.UsedWeekTokens
				account.Quota5hTokens = account.QuotaWeekTokens
				account.FiveHourResetAt = account.WeeklyResetAt
			}
		}
		accounts = append(accounts, account)
	}

	totals := func(u UsageTotals) dashboardTotals {
		return dashboardTotals{
			InputTokens:     u.InputTokens,
			CachedTokens:    u.CachedTokens,
			OutputTokens:    u.OutputTokens,
			ReasoningTokens: u.ReasoningTokens,
			TotalTokens:     u.TotalTokens(),
		}
	}

	resp := dashboardOverviewResponse{
		GeneratedAt:  time.Now().UTC(),
		Today:        totals(periods.Daily),
		Recent7Days:  totals(periods.Recent7Days),
		Recent30Days: totals(periods.Recent30Days),
		Recent90Days: totals(periods.Recent90Days),
		Total:        totals(periods.Total),
		Composition:  compositionFromTotals(periods.Total.InputTokens, periods.Total.CachedTokens, periods.Total.OutputTokens),
		Trend:        trendFromDailyBuckets(dailyBuckets, []int{7, 30, 90}),
		Accounts:     accounts,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	if err := enc.Encode(resp); err != nil {
		slog.Warn("encode dashboard overview", "err", err)
	}
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	t := value
	return &t
}

func compositionFromTotals(inputTokens int64, cachedTokens int64, outputTokens int64) dashboardComposition {
	total := inputTokens + cachedTokens + outputTokens
	return dashboardComposition{
		CachedInput: dashboardCompositionPart{
			Tokens:  cachedTokens,
			Percent: percentOf(cachedTokens, total),
		},
		Input: dashboardCompositionPart{
			Tokens:  inputTokens,
			Percent: percentOf(inputTokens, total),
		},
		Output: dashboardCompositionPart{
			Tokens:  outputTokens,
			Percent: percentOf(outputTokens, total),
		},
	}
}

func percentOf(part int64, total int64) float64 {
	if total <= 0 || part <= 0 {
		return 0
	}
	return math.Round((float64(part)/float64(total))*1000) / 10
}

func trendFromDailyBuckets(dailyBuckets []UsageDailyBucket, windows []int) dashboardTrend {
	trend := dashboardTrend{
		Windows: make([]dashboardTrendWindow, 0, len(windows)),
	}
	for _, days := range windows {
		if days <= 0 {
			continue
		}
		start := max(len(dailyBuckets)-days, 0)
		source := dailyBuckets[start:]
		window := dashboardTrendWindow{
			Days:    days,
			Buckets: make([]dashboardTrendBucket, 0, len(source)),
		}
		for _, bucket := range source {
			window.Buckets = append(window.Buckets, dashboardTrendBucket{
				Date:            bucket.Date.Format(time.DateOnly),
				InputTokens:     bucket.Totals.InputTokens,
				CachedTokens:    bucket.Totals.CachedTokens,
				OutputTokens:    bucket.Totals.OutputTokens,
				ReasoningTokens: bucket.Totals.ReasoningTokens,
				TotalTokens:     bucket.Totals.TotalTokens(),
			})
		}
		trend.Windows = append(trend.Windows, window)
	}
	return trend
}

func (s *Server) activeAccountTokens() map[string][]string {
	refs := s.store.TokenRefs()
	active := make(map[string][]string)
	for _, ref := range refs {
		key := accountKeyFromRef(ref)
		if key == "" {
			continue
		}
		active[key] = append(active[key], ref.ID)
	}
	for key, tokenIDs := range active {
		slices.Sort(tokenIDs)
		active[key] = slices.Compact(tokenIDs)
	}
	return active
}
