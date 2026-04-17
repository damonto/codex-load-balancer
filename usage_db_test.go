package main

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestGlobalPeriodTotalsUsesRollingWindows(t *testing.T) {
	tests := []struct {
		name       string
		records    []UsageRecord
		wantDay    int64
		want7Days  int64
		want30Days int64
		wantTotal  int64
	}{
		{
			name: "splits today recent windows and total",
			records: func() []UsageRecord {
				now := time.Now().UTC()
				dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
				return []UsageRecord{
					{
						AccountKey:      "acct_1",
						TokenID:         "tok_today",
						Path:            "/v1/responses",
						StatusCode:      200,
						InputTokens:     10,
						CachedTokens:    2,
						OutputTokens:    3,
						ReasoningTokens: 1,
						CreatedAt:       dayStart,
					},
					{
						AccountKey:      "acct_1",
						TokenID:         "tok_6d",
						Path:            "/v1/responses",
						StatusCode:      200,
						InputTokens:     20,
						CachedTokens:    1,
						OutputTokens:    4,
						ReasoningTokens: 2,
						CreatedAt:       now.AddDate(0, 0, -6),
					},
					{
						AccountKey:      "acct_1",
						TokenID:         "tok_20d",
						Path:            "/v1/responses",
						StatusCode:      200,
						InputTokens:     30,
						CachedTokens:    5,
						OutputTokens:    6,
						ReasoningTokens: 3,
						CreatedAt:       now.AddDate(0, 0, -20),
					},
					{
						AccountKey:      "acct_1",
						TokenID:         "tok_40d",
						Path:            "/v1/responses",
						StatusCode:      200,
						InputTokens:     40,
						CachedTokens:    6,
						OutputTokens:    7,
						ReasoningTokens: 4,
						CreatedAt:       now.AddDate(0, 0, -40),
					},
				}
			}(),
			wantDay:    15,
			want7Days:  40,
			want30Days: 81,
			wantTotal:  134,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usageDB, err := openUsageDB(filepath.Join(t.TempDir(), "clb.db"))
			if err != nil {
				t.Fatalf("openUsageDB() error = %v", err)
			}
			defer usageDB.Close()

			for _, rec := range tt.records {
				if err := usageDB.InsertUsage(context.Background(), rec); err != nil {
					t.Fatalf("InsertUsage() error = %v", err)
				}
			}

			got, err := usageDB.GlobalPeriodTotals(context.Background())
			if err != nil {
				t.Fatalf("GlobalPeriodTotals() error = %v", err)
			}

			if got.Daily.TotalTokens() != tt.wantDay {
				t.Fatalf("Daily.TotalTokens() = %d, want %d", got.Daily.TotalTokens(), tt.wantDay)
			}
			if got.Recent7Days.TotalTokens() != tt.want7Days {
				t.Fatalf("Recent7Days.TotalTokens() = %d, want %d", got.Recent7Days.TotalTokens(), tt.want7Days)
			}
			if got.Recent30Days.TotalTokens() != tt.want30Days {
				t.Fatalf("Recent30Days.TotalTokens() = %d, want %d", got.Recent30Days.TotalTokens(), tt.want30Days)
			}
			if got.Total.TotalTokens() != tt.wantTotal {
				t.Fatalf("Total.TotalTokens() = %d, want %d", got.Total.TotalTokens(), tt.wantTotal)
			}
		})
	}
}

func TestOpenUsageDBReasoningTokensMigration(t *testing.T) {
	tests := []struct {
		name              string
		precreateOldTable bool
	}{
		{
			name:              "fresh database",
			precreateOldTable: false,
		},
		{
			name:              "old schema without reasoning_tokens",
			precreateOldTable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "clb.db")

			if tt.precreateOldTable {
				dsn := fmt.Sprintf("file:%s?mode=rwc&_journal_mode=WAL&_synchronous=NORMAL", dbPath)
				rawDB, err := sql.Open("sqlite", dsn)
				if err != nil {
					t.Fatalf("open raw sqlite db: %v", err)
				}
				defer rawDB.Close()

				if _, err := rawDB.Exec(`
CREATE TABLE usage_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	account_key TEXT NOT NULL,
	token_id TEXT NOT NULL,
	request_path TEXT NOT NULL,
	status_code INTEGER NOT NULL,
	is_stream INTEGER NOT NULL,
	input_tokens INTEGER NOT NULL,
	cached_tokens INTEGER NOT NULL,
	output_tokens INTEGER NOT NULL,
	created_at_unix INTEGER NOT NULL
);
`); err != nil {
					t.Fatalf("create old usage_events: %v", err)
				}
			}

			usageDB, err := openUsageDB(dbPath)
			if err != nil {
				t.Fatalf("openUsageDB() error = %v", err)
			}
			defer usageDB.Close()

			rec := UsageRecord{
				AccountKey:      "acct_1",
				TokenID:         "tok_1",
				Path:            "/v1/responses",
				StatusCode:      200,
				IsStream:        false,
				InputTokens:     10,
				CachedTokens:    2,
				OutputTokens:    8,
				ReasoningTokens: 7,
				CreatedAt:       time.Unix(1700000000, 0).UTC(),
			}
			if err := usageDB.InsertUsage(context.Background(), rec); err != nil {
				t.Fatalf("InsertUsage() error = %v", err)
			}

			var got int64
			if err := usageDB.db.QueryRowContext(context.Background(), `
SELECT COALESCE(SUM(reasoning_tokens), 0) FROM usage_events
`).Scan(&got); err != nil {
				t.Fatalf("query reasoning_tokens sum: %v", err)
			}
			if got != rec.ReasoningTokens {
				t.Fatalf("reasoning_tokens sum = %d, want %d", got, rec.ReasoningTokens)
			}
		})
	}
}

func TestInsertUsageBatch(t *testing.T) {
	tests := []struct {
		name    string
		records []UsageRecord
		want    int
	}{
		{
			name: "batch inserts multiple usage events",
			records: []UsageRecord{
				{
					AccountKey:      "acct_1",
					TokenID:         "tok_1",
					Path:            "/v1/responses",
					StatusCode:      200,
					InputTokens:     10,
					CachedTokens:    2,
					OutputTokens:    8,
					ReasoningTokens: 3,
					CreatedAt:       time.Unix(1700000000, 0).UTC(),
				},
				{
					AccountKey:      "acct_2",
					TokenID:         "tok_2",
					Path:            "/v1/responses",
					StatusCode:      201,
					InputTokens:     5,
					CachedTokens:    1,
					OutputTokens:    4,
					ReasoningTokens: 2,
					CreatedAt:       time.Unix(1700000001, 0).UTC(),
				},
			},
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usageDB, err := openUsageDB(filepath.Join(t.TempDir(), "clb.db"))
			if err != nil {
				t.Fatalf("openUsageDB() error = %v", err)
			}
			defer usageDB.Close()

			if err := usageDB.InsertUsageBatch(context.Background(), tt.records); err != nil {
				t.Fatalf("InsertUsageBatch() error = %v", err)
			}

			var got int
			if err := usageDB.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM usage_events`).Scan(&got); err != nil {
				t.Fatalf("count usage events: %v", err)
			}
			if got != tt.want {
				t.Fatalf("usage event count = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestAccountTrendsBatch(t *testing.T) {
	tests := []struct {
		name        string
		accountKeys []string
		records     []UsageRecord
		assert      func(t *testing.T, got map[string]AccountUsageTrends)
	}{
		{
			name:        "groups overlapping accounts and preserves empty accounts",
			accountKeys: []string{"acct-a", "acct-b", "acct-empty"},
			records: []UsageRecord{
				{
					AccountKey:   "acct-a",
					TokenID:      "tok-a-1",
					Path:         "/v1/responses",
					StatusCode:   200,
					InputTokens:  10,
					CachedTokens: 1,
					OutputTokens: 3,
					CreatedAt:    time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC),
				},
				{
					AccountKey:   "acct-a",
					TokenID:      "tok-a-2",
					Path:         "/v1/responses",
					StatusCode:   200,
					InputTokens:  4,
					OutputTokens: 2,
					CreatedAt:    time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC),
				},
				{
					AccountKey:   "acct-a",
					TokenID:      "tok-a-3",
					Path:         "/v1/responses",
					StatusCode:   200,
					InputTokens:  7,
					OutputTokens: 1,
					CreatedAt:    time.Date(2026, 2, 8, 9, 0, 0, 0, time.UTC),
				},
				{
					AccountKey:   "acct-b",
					TokenID:      "tok-b-1",
					Path:         "/v1/responses",
					StatusCode:   200,
					InputTokens:  5,
					CachedTokens: 2,
					OutputTokens: 1,
					CreatedAt:    time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC),
				},
				{
					AccountKey:   "acct-b",
					TokenID:      "tok-b-2",
					Path:         "/v1/responses",
					StatusCode:   200,
					InputTokens:  8,
					CachedTokens: 1,
					OutputTokens: 4,
					CreatedAt:    time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC),
				},
			},
			assert: func(t *testing.T, got map[string]AccountUsageTrends) {
				t.Helper()

				if len(got["acct-empty"].Daily) != 0 || len(got["acct-empty"].Weekly) != 0 || len(got["acct-empty"].Monthly) != 0 {
					t.Fatalf("acct-empty trends = %+v, want empty slices", got["acct-empty"])
				}
				if got["acct-empty"].Daily == nil || got["acct-empty"].Weekly == nil || got["acct-empty"].Monthly == nil {
					t.Fatalf("acct-empty trends should use empty slices, got %+v", got["acct-empty"])
				}

				wantDailyA := []UsagePoint{
					{Bucket: "2026-02-01", InputTokens: 14, CachedTokens: 1, OutputTokens: 5},
					{Bucket: "2026-02-08", InputTokens: 7, CachedTokens: 0, OutputTokens: 1},
				}
				if !slices.Equal(got["acct-a"].Daily, wantDailyA) {
					t.Fatalf("acct-a daily = %+v, want %+v", got["acct-a"].Daily, wantDailyA)
				}

				wantMonthlyA := []UsagePoint{
					{Bucket: "2026-02", InputTokens: 21, CachedTokens: 1, OutputTokens: 6},
				}
				if !slices.Equal(got["acct-a"].Monthly, wantMonthlyA) {
					t.Fatalf("acct-a monthly = %+v, want %+v", got["acct-a"].Monthly, wantMonthlyA)
				}

				wantMonthlyB := []UsagePoint{
					{Bucket: "2026-02", InputTokens: 5, CachedTokens: 2, OutputTokens: 1},
					{Bucket: "2026-03", InputTokens: 8, CachedTokens: 1, OutputTokens: 4},
				}
				if !slices.Equal(got["acct-b"].Monthly, wantMonthlyB) {
					t.Fatalf("acct-b monthly = %+v, want %+v", got["acct-b"].Monthly, wantMonthlyB)
				}

				if len(got["acct-a"].Weekly) != 2 || len(got["acct-b"].Weekly) != 2 {
					t.Fatalf("weekly bucket counts = acct-a:%d acct-b:%d, want 2 and 2", len(got["acct-a"].Weekly), len(got["acct-b"].Weekly))
				}
				if got["acct-a"].Weekly[0].Bucket > got["acct-a"].Weekly[1].Bucket {
					t.Fatalf("acct-a weekly buckets not chronological: %+v", got["acct-a"].Weekly)
				}
				if got["acct-b"].Weekly[0].Bucket > got["acct-b"].Weekly[1].Bucket {
					t.Fatalf("acct-b weekly buckets not chronological: %+v", got["acct-b"].Weekly)
				}
			},
		},
		{
			name:        "limits daily buckets independently per account",
			accountKeys: []string{"acct-a", "acct-b"},
			records: func() []UsageRecord {
				records := make([]UsageRecord, 0, 37)
				start := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
				for i := range 35 {
					records = append(records, UsageRecord{
						AccountKey:   "acct-a",
						TokenID:      fmt.Sprintf("tok-a-%02d", i),
						Path:         "/v1/responses",
						StatusCode:   200,
						InputTokens:  1,
						OutputTokens: 1,
						CreatedAt:    start.AddDate(0, 0, i),
					})
				}
				records = append(records,
					UsageRecord{
						AccountKey:  "acct-b",
						TokenID:     "tok-b-1",
						Path:        "/v1/responses",
						StatusCode:  200,
						InputTokens: 1,
						CreatedAt:   start,
					},
					UsageRecord{
						AccountKey:  "acct-b",
						TokenID:     "tok-b-2",
						Path:        "/v1/responses",
						StatusCode:  200,
						InputTokens: 1,
						CreatedAt:   start.AddDate(0, 0, 1),
					},
				)
				return records
			}(),
			assert: func(t *testing.T, got map[string]AccountUsageTrends) {
				t.Helper()

				if len(got["acct-a"].Daily) != 30 {
					t.Fatalf("acct-a daily len = %d, want 30", len(got["acct-a"].Daily))
				}
				if got["acct-a"].Daily[0].Bucket != "2026-01-06" || got["acct-a"].Daily[29].Bucket != "2026-02-04" {
					t.Fatalf("acct-a daily buckets = %q..%q, want 2026-01-06..2026-02-04", got["acct-a"].Daily[0].Bucket, got["acct-a"].Daily[29].Bucket)
				}
				if len(got["acct-b"].Daily) != 2 {
					t.Fatalf("acct-b daily len = %d, want 2", len(got["acct-b"].Daily))
				}
			},
		},
		{
			name:        "limits weekly and monthly buckets independently per account",
			accountKeys: []string{"acct-a", "acct-b"},
			records: func() []UsageRecord {
				records := make([]UsageRecord, 0, 34)
				weekStart := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
				for i := range 18 {
					records = append(records, UsageRecord{
						AccountKey:  "acct-a",
						TokenID:     fmt.Sprintf("tok-a-week-%02d", i),
						Path:        "/v1/responses",
						StatusCode:  200,
						InputTokens: 1,
						CreatedAt:   weekStart.AddDate(0, 0, i*7),
					})
				}

				monthStart := time.Date(2024, 1, 1, 9, 0, 0, 0, time.UTC)
				for i := range 14 {
					records = append(records, UsageRecord{
						AccountKey:   "acct-a",
						TokenID:      fmt.Sprintf("tok-a-month-%02d", i),
						Path:         "/v1/responses",
						StatusCode:   200,
						CachedTokens: 1,
						CreatedAt:    monthStart.AddDate(0, i, 0),
					})
				}

				records = append(records,
					UsageRecord{
						AccountKey:  "acct-b",
						TokenID:     "tok-b-week-1",
						Path:        "/v1/responses",
						StatusCode:  200,
						InputTokens: 1,
						CreatedAt:   weekStart,
					},
					UsageRecord{
						AccountKey:   "acct-b",
						TokenID:      "tok-b-month-1",
						Path:         "/v1/responses",
						StatusCode:   200,
						CachedTokens: 1,
						CreatedAt:    monthStart,
					},
				)
				return records
			}(),
			assert: func(t *testing.T, got map[string]AccountUsageTrends) {
				t.Helper()

				if len(got["acct-a"].Weekly) != 16 {
					t.Fatalf("acct-a weekly len = %d, want 16", len(got["acct-a"].Weekly))
				}
				if len(got["acct-a"].Monthly) != 12 {
					t.Fatalf("acct-a monthly len = %d, want 12", len(got["acct-a"].Monthly))
				}
				if len(got["acct-b"].Weekly) != 2 {
					t.Fatalf("acct-b weekly len = %d, want 2", len(got["acct-b"].Weekly))
				}
				if len(got["acct-b"].Monthly) != 2 {
					t.Fatalf("acct-b monthly len = %d, want 2", len(got["acct-b"].Monthly))
				}
				if got["acct-a"].Monthly[0].Bucket != "2024-06" || got["acct-a"].Monthly[11].Bucket != "2025-05" {
					t.Fatalf("acct-a monthly buckets = %q..%q, want 2024-06..2025-05", got["acct-a"].Monthly[0].Bucket, got["acct-a"].Monthly[11].Bucket)
				}
				if got["acct-a"].Weekly[0].Bucket > got["acct-a"].Weekly[len(got["acct-a"].Weekly)-1].Bucket {
					t.Fatalf("acct-a weekly buckets not chronological: %+v", got["acct-a"].Weekly)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usageDB, err := openUsageDB(filepath.Join(t.TempDir(), "clb.db"))
			if err != nil {
				t.Fatalf("openUsageDB() error = %v", err)
			}
			defer usageDB.Close()

			for _, rec := range tt.records {
				if err := usageDB.InsertUsage(context.Background(), rec); err != nil {
					t.Fatalf("InsertUsage() error = %v", err)
				}
			}

			got, err := usageDB.AccountTrendsBatch(context.Background(), tt.accountKeys)
			if err != nil {
				t.Fatalf("AccountTrendsBatch() error = %v", err)
			}

			tt.assert(t, got)
		})
	}
}
