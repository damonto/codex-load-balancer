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
	"strings"
	"time"
)

//go:embed web/*
var dashboardFiles embed.FS

var dashboardWebFS = mustDashboardWebFS()
var dashboardAssetHandler = http.StripPrefix("/stats/assets/", http.FileServer(http.FS(dashboardWebFS)))
var indexHTML = mustReadDashboardFile("index.html")

type dashboardOverviewResponse struct {
	GeneratedAt  time.Time          `json:"generated_at"`
	Today        dashboardTotals    `json:"today"`
	Recent7Days  dashboardTotals    `json:"recent_7_days"`
	Recent30Days dashboardTotals    `json:"recent_30_days"`
	Total        dashboardTotals    `json:"total"`
	Accounts     []dashboardAccount `json:"accounts"`
}

type dashboardTotals struct {
	InputTokens     int64 `json:"input_tokens"`
	CachedTokens    int64 `json:"cached_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
	TotalTokens     int64 `json:"total_tokens"`
}

type dashboardAccount struct {
	AccountKey      string     `json:"account_key"`
	Email           string     `json:"email"`
	PlanType        string     `json:"plan_type"`
	TokenIDs        []string   `json:"token_ids"`
	InputTokens     int64      `json:"input_tokens"`
	CachedTokens    int64      `json:"cached_tokens"`
	OutputTokens    int64      `json:"output_tokens"`
	ReasoningTokens int64      `json:"reasoning_tokens"`
	TotalTokens     int64      `json:"total_tokens"`
	Used5hTokens    int64      `json:"used_5h_tokens"`
	Quota5hTokens   int64      `json:"quota_5h_tokens"`
	Has5hQuota      bool       `json:"has_5h_quota"`
	FiveHourResetAt *time.Time `json:"five_hour_reset_at"`
	UsedWeekTokens  int64      `json:"used_week_tokens"`
	QuotaWeekTokens int64      `json:"quota_week_tokens"`
	HasWeekQuota    bool       `json:"has_week_quota"`
	WeeklyResetAt   *time.Time `json:"weekly_reset_at"`
}

type dashboardAccountResponse struct {
	AccountKey      string       `json:"account_key"`
	Quota5hTokens   int64        `json:"quota_5h_tokens"`
	QuotaWeekTokens int64        `json:"quota_week_tokens"`
	Has5hQuota      bool         `json:"has_5h_quota"`
	FiveHourResetAt *time.Time   `json:"five_hour_reset_at"`
	HasWeekQuota    bool         `json:"has_week_quota"`
	WeeklyResetAt   *time.Time   `json:"weekly_reset_at"`
	Daily           []UsagePoint `json:"daily"`
	Weekly          []UsagePoint `json:"weekly"`
	Monthly         []UsagePoint `json:"monthly"`
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
			Email:           info.Email,
			PlanType:        info.PlanType,
			TokenIDs:        summary.ActiveTokenIDs,
			InputTokens:     summary.InputTokens,
			CachedTokens:    summary.CachedTokens,
			OutputTokens:    summary.OutputTokens,
			ReasoningTokens: summary.ReasoningTokens,
			TotalTokens:     summary.TotalTokens(),
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
		Total:        totals(periods.Total),
		Accounts:     accounts,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	if err := enc.Encode(resp); err != nil {
		slog.Warn("encode dashboard overview", "err", err)
	}
}

func (s *Server) handleDashboardAccount(w http.ResponseWriter, r *http.Request) {
	if s.usageDB == nil {
		http.Error(w, "usage database is not configured", http.StatusServiceUnavailable)
		return
	}

	accountKey := strings.TrimSpace(r.URL.Query().Get("account_key"))
	if accountKey == "" {
		http.Error(w, "missing account_key", http.StatusBadRequest)
		return
	}

	daily, weekly, monthly, err := s.usageDB.AccountTrends(r.Context(), accountKey)
	if err != nil {
		slog.Warn("query account trends", "account", accountKey, "err", err)
		http.Error(w, "query account trends", http.StatusInternalServerError)
		return
	}

	quota := s.store.AccountQuotaSnapshots()[accountKey]
	quota5h := int64(0)
	quotaWeek := int64(0)
	if quota.HasFiveHour {
		quota5h = int64(math.Round(quota.FiveHourMax))
	}
	if quota.HasWeekly {
		quotaWeek = int64(math.Round(quota.WeeklyMax))
	}

	resp := dashboardAccountResponse{
		AccountKey:      accountKey,
		Has5hQuota:      quota.HasFiveHour,
		FiveHourResetAt: optionalTime(quota.FiveHourResetAt),
		HasWeekQuota:    quota.HasWeekly,
		WeeklyResetAt:   optionalTime(quota.WeeklyResetAt),
		Quota5hTokens:   quota5h,
		QuotaWeekTokens: quotaWeek,
		Daily:           daily,
		Weekly:          weekly,
		Monthly:         monthly,
	}
	if !resp.Has5hQuota && resp.HasWeekQuota {
		resp.Has5hQuota = true
		resp.Quota5hTokens = resp.QuotaWeekTokens
		resp.FiveHourResetAt = resp.WeeklyResetAt
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	if err := enc.Encode(resp); err != nil {
		slog.Warn("encode account dashboard", "account", accountKey, "err", err)
	}
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	t := value
	return &t
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
