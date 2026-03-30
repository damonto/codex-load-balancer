package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestTopUpMissingAccountsNoop(t *testing.T) {
	store := NewTokenStore()
	if err := topUpMissingAccounts(context.Background(), store, t.TempDir(), 0, topUpOptions{}); err != nil {
		t.Fatalf("topUpMissingAccounts() error = %v", err)
	}
}

func TestTopUpAccountsDisabledNoop(t *testing.T) {
	store := NewTokenStore()
	if err := topUpAccounts(context.Background(), store, t.TempDir(), topUpOptions{
		Enabled:     false,
		TargetCount: 1,
	}); err != nil {
		t.Fatalf("topUpAccounts() error = %v", err)
	}
}

func TestTopUpAccountsRejectsInvalidOptions(t *testing.T) {
	tests := []struct {
		name    string
		opts    topUpOptions
		wantErr string
	}{
		{
			name: "reject zero workers",
			opts: topUpOptions{
				Enabled:         true,
				TargetCount:     1,
				RegisterTimeout: time.Minute,
			},
			wantErr: "register workers must be positive",
		},
		{
			name: "reject zero timeout",
			opts: topUpOptions{
				Enabled:         true,
				TargetCount:     1,
				RegisterWorkers: 1,
			},
			wantErr: "register timeout must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := topUpAccounts(context.Background(), NewTokenStore(), t.TempDir(), tt.opts)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("topUpAccounts() error = %v, want contains %q", err, tt.wantErr)
			}
		})
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
		if err := writeSessionFileForTest(filePath, "access-token", "account-1", "demo@example.com", "plus"); err != nil {
			return plus.RegisterResult{}, err
		}
		return plus.RegisterResult{
			Email:     "demo@example.com",
			AccountID: "account-1",
			Tokens: plus.AuthTokens{
				AccessToken: "access-token",
			},
			Session: plus.ChatGPTSession{
				AccessToken: "access-token",
				User: plus.ChatGPTSessionUser{
					Email: "demo@example.com",
				},
				Account: plus.ChatGPTSessionAccount{
					ID:       "account-1",
					PlanType: "plus",
				},
			},
			FilePath: filePath,
		}, nil
	}

	store := NewTokenStore()
	errCh := make(chan error, 2)
	go func() {
		errCh <- topUpAccounts(context.Background(), store, dataDir, topUpOptions{Enabled: true, TargetCount: 1, RegisterWorkers: 1, RegisterTimeout: time.Minute})
	}()
	<-started
	go func() {
		errCh <- topUpAccounts(context.Background(), store, dataDir, topUpOptions{Enabled: true, TargetCount: 1, RegisterWorkers: 1, RegisterTimeout: time.Minute})
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

func writeSessionFileForTest(path string, accessToken string, accountID string, email string, planType string) error {
	payload := map[string]any{
		"auth_mode":    "chatgpt",
		"last_refresh": time.Now().UTC().Format(time.RFC3339Nano),
		"created_at":   time.Now().UTC().Format(time.RFC3339Nano),
		"tokens": map[string]string{
			"access_token": accessToken,
			"account_id":   accountID,
		},
	}
	if email != "" || planType != "" {
		payload["session"] = map[string]any{
			"user": map[string]string{
				"email": email,
			},
			"account": map[string]string{
				"planType": planType,
			},
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
