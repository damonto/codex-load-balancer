package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestMaybeRefreshTokenDebounce(t *testing.T) {
	tests := []struct {
		name          string
		lastRefresh   time.Time
		debounce      time.Duration
		wantRefreshed bool
	}{
		{
			name:          "recent refresh skips forced refresh",
			lastRefresh:   time.Now().UTC(),
			debounce:      time.Minute,
			wantRefreshed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewTokenStore()
			store.UpsertToken(TokenState{
				ID:           "active.json",
				Path:         filepath.Join(t.TempDir(), "active.json"),
				Token:        "access-token",
				RefreshToken: "refresh-token",
				LastRefresh:  tt.lastRefresh,
			}, time.Now().UTC())

			refreshed, err := maybeRefreshToken(context.Background(), store, "active.json", refreshConfig{Debounce: tt.debounce})
			if err != nil {
				t.Fatalf("maybeRefreshToken() error = %v", err)
			}
			if refreshed != tt.wantRefreshed {
				t.Fatalf("maybeRefreshToken() refreshed = %v, want %v", refreshed, tt.wantRefreshed)
			}

			token, ok := store.TokenSnapshot("active.json")
			if !ok {
				t.Fatal("token should remain in store")
			}
			if !token.LastRefresh.Equal(tt.lastRefresh) {
				t.Fatalf("LastRefresh = %v, want %v", token.LastRefresh, tt.lastRefresh)
			}
		})
	}
}

func TestMaybeRefreshTokenDebounceAfterConcurrentRefresh(t *testing.T) {
	tests := []struct {
		name string
	}{
		{
			name: "returns true when another goroutine refreshed while waiting on the lock",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewTokenStore()
			store.UpsertToken(TokenState{
				ID:           "active.json",
				Path:         filepath.Join(t.TempDir(), "active.json"),
				Token:        "old-access-token",
				RefreshToken: "refresh-token",
				LastRefresh:  time.Now().UTC().Add(-time.Hour),
			}, time.Now().UTC())

			lock := store.RefreshLock("active.json")
			lock.Lock()

			type refreshResult struct {
				refreshed bool
				err       error
			}
			resultCh := make(chan refreshResult, 1)
			go func() {
				refreshed, err := maybeRefreshToken(context.Background(), store, "active.json", refreshConfig{Debounce: time.Minute})
				resultCh <- refreshResult{refreshed: refreshed, err: err}
			}()

			time.Sleep(10 * time.Millisecond)
			store.UpdateCredentials("active.json", "new-access-token", "refresh-token")
			lock.Unlock()

			result := <-resultCh
			if result.err != nil {
				t.Fatalf("maybeRefreshToken() error = %v", result.err)
			}
			if !result.refreshed {
				t.Fatal("maybeRefreshToken() refreshed = false, want true")
			}
		})
	}
}
