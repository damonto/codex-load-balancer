package main

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

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
			dbPath := filepath.Join(t.TempDir(), "usage.db")

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
