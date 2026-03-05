package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestValidAccountCount(t *testing.T) {
	store := NewTokenStore()
	now := time.Now()

	pathA1 := filepath.Join("data", "a1.json")
	pathA2 := filepath.Join("data", "a2.json")
	pathB := filepath.Join("data", "b.json")

	store.UpsertToken(TokenState{
		ID:        "a1.json",
		Path:      pathA1,
		Token:     "token-a1",
		AccountID: "account-a",
	}, now)
	store.UpsertToken(TokenState{
		ID:        "a2.json",
		Path:      pathA2,
		Token:     "token-a2",
		AccountID: "account-a",
	}, now)
	store.UpsertToken(TokenState{
		ID:        "b.json",
		Path:      pathB,
		Token:     "token-b",
		AccountID: "account-b",
	}, now)
	store.MarkInvalid("b.json")

	if got, want := store.ValidAccountCount(), 1; got != want {
		t.Fatalf("ValidAccountCount() = %d, want %d", got, want)
	}
}

func TestResolveRegisterWorkers(t *testing.T) {
	tests := []struct {
		name    string
		workers int
		want    int
	}{
		{
			name:    "default when zero",
			workers: 0,
			want:    defaultRegisterWorkers,
		},
		{
			name:    "default when negative",
			workers: -3,
			want:    defaultRegisterWorkers,
		},
		{
			name:    "keep positive value",
			workers: 9,
			want:    9,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveRegisterWorkers(tt.workers); got != tt.want {
				t.Fatalf("resolveRegisterWorkers() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestTopUpMissingAccountsNoop(t *testing.T) {
	store := NewTokenStore()
	if err := topUpMissingAccounts(context.Background(), store, t.TempDir(), 0, topUpOptions{}); err != nil {
		t.Fatalf("topUpMissingAccounts() error = %v", err)
	}
}
