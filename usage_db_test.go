package main

import (
	"context"
	"database/sql"
	"path/filepath"
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
				rawDB, err := sql.Open("sqlite", usageDBDSN(dbPath))
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
