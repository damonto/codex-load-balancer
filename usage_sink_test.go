package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestUsageSinkStopDrainsQueuedRecords(t *testing.T) {
	tests := []struct {
		name    string
		records []UsageRecord
	}{
		{
			name: "stop drains records queued before worker starts",
			records: []UsageRecord{
				{
					AccountKey: "acct-1",
					TokenID:    "token-1",
					Path:       "/responses",
					StatusCode: 200,
					CreatedAt:  time.Unix(10, 0).UTC(),
				},
				{
					AccountKey: "acct-1",
					TokenID:    "token-2",
					Path:       "/responses",
					StatusCode: 200,
					CreatedAt:  time.Unix(11, 0).UTC(),
				},
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

			sink := NewUsageSink(usageDB, 1)
			for _, rec := range tt.records {
				if err := sink.Record(rec); err != nil {
					t.Fatalf("Record() error = %v", err)
				}
			}

			sink.Run()
			sink.Stop()
			sink.Wait()

			var got int
			if err := usageDB.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM usage_events`).Scan(&got); err != nil {
				t.Fatalf("count usage events: %v", err)
			}
			if got != len(tt.records) {
				t.Fatalf("usage event count = %d, want %d", got, len(tt.records))
			}
		})
	}
}

func TestUsageSinkRecordRejectsStoppedSink(t *testing.T) {
	tests := []struct {
		name string
	}{
		{
			name: "stopped sink rejects new records",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usageDB, err := openUsageDB(filepath.Join(t.TempDir(), "clb.db"))
			if err != nil {
				t.Fatalf("openUsageDB() error = %v", err)
			}
			defer usageDB.Close()

			sink := NewUsageSink(usageDB, 1)
			sink.Stop()

			err = sink.Record(UsageRecord{AccountKey: "acct-2", TokenID: "token-2"})
			if !errors.Is(err, errUsageSinkStopped) {
				t.Fatalf("Record() error = %v, want %v", err, errUsageSinkStopped)
			}
		})
	}
}

func TestUsageSinkOverflowAggregatesRecords(t *testing.T) {
	tests := []struct {
		name    string
		records []UsageRecord
		wantDB  int
		wantSum int64
	}{
		{
			name: "same account and hour overflow records are coalesced",
			records: []UsageRecord{
				{
					AccountKey:      "acct-1",
					TokenID:         "token-1",
					Path:            "/responses",
					StatusCode:      200,
					InputTokens:     10,
					CachedTokens:    1,
					OutputTokens:    2,
					ReasoningTokens: 3,
					CreatedAt:       time.Date(2026, 4, 17, 10, 5, 0, 0, time.UTC),
				},
				{
					AccountKey:      "acct-1",
					TokenID:         "token-1",
					Path:            "/responses",
					StatusCode:      200,
					InputTokens:     20,
					CachedTokens:    2,
					OutputTokens:    3,
					ReasoningTokens: 4,
					CreatedAt:       time.Date(2026, 4, 17, 10, 25, 0, 0, time.UTC),
				},
				{
					AccountKey:      "acct-1",
					TokenID:         "token-1",
					Path:            "/responses",
					StatusCode:      200,
					InputTokens:     30,
					CachedTokens:    3,
					OutputTokens:    4,
					ReasoningTokens: 5,
					CreatedAt:       time.Date(2026, 4, 17, 10, 45, 0, 0, time.UTC),
				},
			},
			wantDB:  2,
			wantSum: 75,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usageDB, err := openUsageDB(filepath.Join(t.TempDir(), "clb.db"))
			if err != nil {
				t.Fatalf("openUsageDB() error = %v", err)
			}
			defer usageDB.Close()

			sink := NewUsageSink(usageDB, 1)
			for _, rec := range tt.records {
				if err := sink.Record(rec); err != nil {
					t.Fatalf("Record() error = %v", err)
				}
			}

			if got := len(sink.queued); got != 1 {
				t.Fatalf("len(queued) = %d, want %d", got, 1)
			}
			if got := len(sink.overflow); got != 1 {
				t.Fatalf("len(overflow) = %d, want %d", got, 1)
			}

			sink.Run()
			sink.Stop()
			sink.Wait()

			var gotCount int
			if err := usageDB.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM usage_events`).Scan(&gotCount); err != nil {
				t.Fatalf("count usage events: %v", err)
			}
			if gotCount != tt.wantDB {
				t.Fatalf("usage event count = %d, want %d", gotCount, tt.wantDB)
			}

			totals, err := usageDB.GlobalTotals(context.Background())
			if err != nil {
				t.Fatalf("GlobalTotals() error = %v", err)
			}
			if totals.TotalTokens() != tt.wantSum {
				t.Fatalf("GlobalTotals().TotalTokens() = %d, want %d", totals.TotalTokens(), tt.wantSum)
			}
		})
	}
}

func TestUsageSinkOverflowPreservesRequestDimensions(t *testing.T) {
	usageDB, err := openUsageDB(filepath.Join(t.TempDir(), "clb.db"))
	if err != nil {
		t.Fatalf("openUsageDB() error = %v", err)
	}
	defer usageDB.Close()

	sink := NewUsageSink(usageDB, 2)
	records := []UsageRecord{
		{
			AccountKey: "acct-1",
			TokenID:    "queued-1",
			Path:       "/responses",
			StatusCode: 200,
			CreatedAt:  time.Date(2026, 4, 17, 10, 0, 0, 0, time.UTC),
		},
		{
			AccountKey: "acct-1",
			TokenID:    "queued-2",
			Path:       "/responses",
			StatusCode: 200,
			CreatedAt:  time.Date(2026, 4, 17, 10, 1, 0, 0, time.UTC),
		},
		{
			AccountKey:  "acct-1",
			TokenID:     "token-1",
			Path:        "/responses",
			StatusCode:  200,
			InputTokens: 10,
			CreatedAt:   time.Date(2026, 4, 17, 10, 5, 0, 0, time.UTC),
		},
		{
			AccountKey:  "acct-1",
			TokenID:     "token-1",
			Path:        "/models",
			StatusCode:  429,
			InputTokens: 20,
			CreatedAt:   time.Date(2026, 4, 17, 10, 6, 0, 0, time.UTC),
		},
	}
	for i, rec := range records {
		if err := sink.Record(rec); err != nil {
			t.Fatalf("Record(%d) error = %v", i, err)
		}
	}

	if got := len(sink.overflow); got != 2 {
		t.Fatalf("len(overflow) = %d, want 2", got)
	}

	sink.Run()
	sink.Stop()
	sink.Wait()

	var gotCount int
	if err := usageDB.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM usage_events`).Scan(&gotCount); err != nil {
		t.Fatalf("count usage events: %v", err)
	}
	if gotCount != len(records) {
		t.Fatalf("usage event count = %d, want %d", gotCount, len(records))
	}
}

func TestUsageSinkRecordReturnsFullWhenQueueAndOverflowSaturated(t *testing.T) {
	tests := []struct {
		name    string
		records []UsageRecord
		wantErr error
	}{
		{
			name: "distinct overflow buckets exhaust bounded memory",
			records: []UsageRecord{
				{
					AccountKey: "acct-1",
					TokenID:    "token-1",
					CreatedAt:  time.Date(2026, 4, 17, 10, 5, 0, 0, time.UTC),
				},
				{
					AccountKey: "acct-1",
					TokenID:    "token-1",
					CreatedAt:  time.Date(2026, 4, 17, 10, 15, 0, 0, time.UTC),
				},
				{
					AccountKey: "acct-2",
					TokenID:    "token-2",
					CreatedAt:  time.Date(2026, 4, 17, 11, 5, 0, 0, time.UTC),
				},
			},
			wantErr: errUsageSinkFull,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usageDB, err := openUsageDB(filepath.Join(t.TempDir(), "clb.db"))
			if err != nil {
				t.Fatalf("openUsageDB() error = %v", err)
			}
			defer usageDB.Close()

			sink := NewUsageSink(usageDB, 1)
			for i, rec := range tt.records[:len(tt.records)-1] {
				if err := sink.Record(rec); err != nil {
					t.Fatalf("Record(%d) error = %v", i, err)
				}
			}

			err = sink.Record(tt.records[len(tt.records)-1])
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Record() error = %v, want %v", err, tt.wantErr)
			}
			if got := len(sink.queued); got != 1 {
				t.Fatalf("len(queued) = %d, want %d", got, 1)
			}
			if got := len(sink.overflow); got != 1 {
				t.Fatalf("len(overflow) = %d, want %d", got, 1)
			}
		})
	}
}
