package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/damonto/codex-load-balancer/plus"
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

func TestTopUpAccountsSerializesConcurrentRuns(t *testing.T) {
	dataDir := t.TempDir()
	var registerCalls atomic.Int32
	started := make(chan struct{}, 1)
	release := make(chan struct{})

	originalRegister := registerCodexCredential
	t.Cleanup(func() { registerCodexCredential = originalRegister })
	registerCodexCredential = func(ctx context.Context, opts plus.RegisterOptions) (plus.RegisterResult, error) {
		registerCalls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		if err := os.MkdirAll(opts.DataDir, 0o755); err != nil {
			return plus.RegisterResult{}, err
		}
		filePath := filepath.Join(opts.DataDir, "demo@example.com.json")
		if err := writeCredentialFileForTest(filePath, "access-token", "refresh-token", "account-1"); err != nil {
			return plus.RegisterResult{}, err
		}
		return plus.RegisterResult{
			Email:     "demo@example.com",
			AccountID: "account-1",
			Tokens: plus.AuthTokens{
				AccessToken:  "access-token",
				RefreshToken: "refresh-token",
			},
			FilePath: filePath,
		}, nil
	}

	store := NewTokenStore()
	errCh := make(chan error, 2)
	go func() {
		errCh <- topUpAccounts(context.Background(), store, dataDir, topUpOptions{TargetCount: 1, RegisterWorkers: 1})
	}()
	<-started
	go func() {
		errCh <- topUpAccounts(context.Background(), store, dataDir, topUpOptions{TargetCount: 1, RegisterWorkers: 1})
	}()
	close(release)

	for range 2 {
		if err := <-errCh; err != nil {
			t.Fatalf("topUpAccounts() error = %v", err)
		}
	}
	if got := registerCalls.Load(); got != 1 {
		t.Fatalf("register calls = %d, want %d", got, 1)
	}
}

func writeCredentialFileForTest(path string, accessToken string, refreshToken string, accountID string) error {
	payload := map[string]any{
		"last_refresh": time.Now().UTC().Format(time.RFC3339Nano),
		"tokens": map[string]string{
			"access_token":  accessToken,
			"refresh_token": refreshToken,
			"account_id":    accountID,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
