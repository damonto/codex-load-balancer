package plus

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestPreferOpenAIRecords(t *testing.T) {
	tests := []struct {
		name    string
		records []emailListRecord
		wantIDs []int
	}{
		{
			name: "keeps only openai related records",
			records: []emailListRecord{
				{EmailID: 1, SendEmail: "noreply@example.com", Subject: "Welcome"},
				{EmailID: 2, SendEmail: "noreply@tm.openai.com", Subject: "Your verification code"},
				{EmailID: 3, SendEmail: "service@another.com", Subject: "openai activity detected"},
			},
			wantIDs: []int{2, 3},
		},
		{
			name: "falls back to all records when no openai signals",
			records: []emailListRecord{
				{EmailID: 4, SendEmail: "noreply@example.com", Subject: "Welcome"},
				{EmailID: 5, SendEmail: "service@another.com", Subject: "Status update"},
			},
			wantIDs: []int{4, 5},
		},
		{
			name:    "handles empty input",
			records: nil,
			wantIDs: []int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := preferOpenAIRecords(tt.records)
			gotIDs := make([]int, 0, len(got))
			for _, rec := range got {
				gotIDs = append(gotIDs, rec.EmailID)
			}
			if !reflect.DeepEqual(gotIDs, tt.wantIDs) {
				t.Fatalf("preferOpenAIRecords() ids = %v, want %v", gotIDs, tt.wantIDs)
			}
		})
	}
}

func TestGenerateWithContext(t *testing.T) {
	tests := []struct {
		name     string
		canceled bool
		wantErr  bool
	}{
		{name: "active context", canceled: false, wantErr: false},
		{name: "canceled context", canceled: true, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.canceled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			_, err := GenerateWithContext(ctx)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GenerateWithContext() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLatestWithContext(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context
		address string
		wantErr bool
	}{
		{
			name:    "empty address",
			ctx:     context.Background(),
			address: "",
			wantErr: true,
		},
		{
			name: "canceled context",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			}(),
			address: "demo@example.invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LatestWithContext(tt.ctx, tt.address)
			if (err != nil) != tt.wantErr {
				t.Fatalf("LatestWithContext() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestIsRecordAfterCursor(t *testing.T) {
	tests := []struct {
		name   string
		record emailListRecord
		cursor emailCursor
		want   bool
	}{
		{
			name:   "zero cursor accepts first email",
			record: emailListRecord{EmailID: 10, CreateTime: "2026-03-11 19:00:00"},
			cursor: emailCursor{},
			want:   true,
		},
		{
			name:   "newer email id wins when time equal",
			record: emailListRecord{EmailID: 11, CreateTime: "2026-03-11 19:00:00"},
			cursor: emailCursor{EmailID: 10, CreatedAt: time.Date(2026, 3, 11, 19, 0, 0, 0, time.UTC), HasCreatedAt: true},
			want:   true,
		},
		{
			name:   "same email is not newer",
			record: emailListRecord{EmailID: 10, CreateTime: "2026-03-11 19:00:00"},
			cursor: emailCursor{EmailID: 10, CreatedAt: time.Date(2026, 3, 11, 19, 0, 0, 0, time.UTC), HasCreatedAt: true},
			want:   false,
		},
		{
			name:   "older email id is rejected",
			record: emailListRecord{EmailID: 9, CreateTime: "2026-03-11 19:00:00"},
			cursor: emailCursor{EmailID: 10},
			want:   false,
		},
		{
			name:   "newer timestamp wins when ids are odd",
			record: emailListRecord{EmailID: 8, CreateTime: "2026-03-11 19:00:01"},
			cursor: emailCursor{EmailID: 10, CreatedAt: time.Date(2026, 3, 11, 19, 0, 0, 0, time.UTC), HasCreatedAt: true},
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRecordAfterCursor(tt.record, tt.cursor); got != tt.want {
				t.Fatalf("isRecordAfterCursor() = %v, want %v", got, tt.want)
			}
		})
	}
}
