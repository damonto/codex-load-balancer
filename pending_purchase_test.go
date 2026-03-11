package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/damonto/codex-load-balancer/plus"
)

func TestCountPendingPurchases(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T, dataDir string)
		wantCount int
	}{
		{
			name:      "missing pending dir counts zero",
			setup:     func(t *testing.T, dataDir string) {},
			wantCount: 0,
		},
		{
			name: "counts only parseable pending credentials without active conflicts",
			setup: func(t *testing.T, dataDir string) {
				pendingDir := pendingPurchaseDir(dataDir)
				if err := os.MkdirAll(filepath.Join(pendingDir, "nested"), 0o755); err != nil {
					t.Fatalf("create pending dir: %v", err)
				}
				files := map[string]string{
					filepath.Join(pendingDir, "good.json"):        `{"tokens":{"access_token":"good-token","account_id":"account-good"}}`,
					filepath.Join(pendingDir, "bad.json"):         `{}`,
					filepath.Join(pendingDir, "duplicate.json"):   `{"tokens":{"access_token":"dup-token","account_id":"account-dup"}}`,
					filepath.Join(pendingDir, "note.txt"):         `x`,
					filepath.Join(pendingDir, "nested", "c.json"): `{"tokens":{"access_token":"nested-token"}}`,
				}
				for path, body := range files {
					if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
						t.Fatalf("write %s: %v", path, err)
					}
				}
				if err := os.WriteFile(filepath.Join(dataDir, "duplicate.json"), []byte(`{"tokens":{"access_token":"active-token","account_id":"account-dup"}}`), 0o644); err != nil {
					t.Fatalf("write active duplicate: %v", err)
				}
			},
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataDir := t.TempDir()
			tt.setup(t, dataDir)

			got, err := countPendingPurchases(dataDir)
			if err != nil {
				t.Fatalf("countPendingPurchases() error = %v", err)
			}
			if got != tt.wantCount {
				t.Fatalf("countPendingPurchases() = %d, want %d", got, tt.wantCount)
			}
		})
	}
}

func TestSyncPendingPurchasesRemovesConflictingPendingFile(t *testing.T) {
	dataDir := t.TempDir()
	pendingDir := pendingPurchaseDir(dataDir)
	if err := os.MkdirAll(pendingDir, 0o755); err != nil {
		t.Fatalf("create pending dir: %v", err)
	}

	activePath := filepath.Join(dataDir, "demo@example.com.json")
	if err := os.WriteFile(activePath, []byte(`{"tokens":{"access_token":"active-token","account_id":"account-1"}}`), 0o644); err != nil {
		t.Fatalf("write active credential: %v", err)
	}
	pendingPath := filepath.Join(pendingDir, "demo@example.com.json")
	if err := os.WriteFile(pendingPath, []byte(`{"tokens":{"access_token":"pending-token","account_id":"account-1","refresh_token":"refresh-token"}}`), 0o644); err != nil {
		t.Fatalf("write pending credential: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(rateLimitStatusPayload{
			UserID:    "user-1",
			AccountID: "account-1",
			Email:     "demo@example.com",
			PlanType:  "plus",
			RateLimit: &rateLimitStatusDetails{
				PrimaryWindow: &rateLimitWindowSnapshot{UsedPercent: 10, LimitWindowSeconds: 18000},
			},
		}); err != nil {
			t.Fatalf("encode usage response: %v", err)
		}
	}))
	defer server.Close()

	pendingStore := NewTokenStore()
	activeStore := NewTokenStore()
	syncPendingPurchasesOnce(context.Background(), pendingStore, activeStore, server.Client(), dataDir, server.URL, usageSyncOptions{Concurrency: 1})

	if _, err := os.Stat(activePath); err != nil {
		t.Fatalf("active credential should remain, stat err = %v", err)
	}
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Fatalf("pending credential should be removed, stat err = %v", err)
	}
}

func TestTopUpAccountsCountsPendingPurchases(t *testing.T) {
	tests := []struct {
		name        string
		targetCount int
		wantCalls   int32
	}{
		{
			name:        "skip when pending already meets target",
			targetCount: 1,
			wantCalls:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataDir := t.TempDir()
			pendingDir := pendingPurchaseDir(dataDir)
			if err := os.MkdirAll(pendingDir, 0o755); err != nil {
				t.Fatalf("create pending dir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(pendingDir, "pending.json"), []byte(`{"tokens":{"access_token":"x"}}`), 0o644); err != nil {
				t.Fatalf("write pending file: %v", err)
			}

			var registerCalls atomic.Int32
			originalRegister := registerCodexCredential
			t.Cleanup(func() { registerCodexCredential = originalRegister })
			registerCodexCredential = func(ctx context.Context, opts plus.RegisterOptions) (plus.RegisterResult, error) {
				registerCalls.Add(1)
				return plus.RegisterResult{}, nil
			}

			store := NewTokenStore()
			if err := topUpAccounts(context.Background(), store, dataDir, topUpOptions{TargetCount: tt.targetCount}); err != nil {
				t.Fatalf("topUpAccounts() error = %v", err)
			}
			if got := registerCalls.Load(); got != tt.wantCalls {
				t.Fatalf("register calls = %d, want %d", got, tt.wantCalls)
			}
		})
	}
}

func TestSyncPendingPurchasesOnce(t *testing.T) {
	tests := []struct {
		name             string
		planType         string
		wantActiveFile   bool
		wantPendingFile  bool
		wantActiveLoaded bool
	}{
		{
			name:             "free account stays pending",
			planType:         "free",
			wantActiveFile:   false,
			wantPendingFile:  true,
			wantActiveLoaded: false,
		},
		{
			name:             "plus account promotes to active",
			planType:         "plus",
			wantActiveFile:   true,
			wantPendingFile:  false,
			wantActiveLoaded: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataDir := t.TempDir()
			pendingDir := pendingPurchaseDir(dataDir)
			if err := os.MkdirAll(pendingDir, 0o755); err != nil {
				t.Fatalf("create pending dir: %v", err)
			}

			pendingPath := filepath.Join(pendingDir, "demo@example.com.json")
			if err := os.WriteFile(pendingPath, []byte(`{"last_refresh":"`+time.Now().UTC().Format(time.RFC3339Nano)+`","tokens":{"access_token":"access-token","refresh_token":"refresh-token","account_id":"account-1"}}`), 0o644); err != nil {
				t.Fatalf("write pending credential: %v", err)
			}

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(rateLimitStatusPayload{
					UserID:    "user-1",
					AccountID: "account-1",
					Email:     "demo@example.com",
					PlanType:  tt.planType,
					RateLimit: &rateLimitStatusDetails{
						PrimaryWindow:   &rateLimitWindowSnapshot{UsedPercent: 10, LimitWindowSeconds: 18000},
						SecondaryWindow: &rateLimitWindowSnapshot{UsedPercent: 20, LimitWindowSeconds: 604800},
					},
				}); err != nil {
					t.Fatalf("encode usage response: %v", err)
				}
			}))
			defer server.Close()

			pendingStore := NewTokenStore()
			activeStore := NewTokenStore()
			syncPendingPurchasesOnce(context.Background(), pendingStore, activeStore, server.Client(), dataDir, server.URL, usageSyncOptions{Concurrency: 1})

			activePath := filepath.Join(dataDir, "demo@example.com.json")
			if _, err := os.Stat(activePath); tt.wantActiveFile != (err == nil) {
				t.Fatalf("active file exists = %v, want %v (err=%v)", err == nil, tt.wantActiveFile, err)
			}
			if _, err := os.Stat(pendingPath); tt.wantPendingFile != (err == nil) {
				t.Fatalf("pending file exists = %v, want %v (err=%v)", err == nil, tt.wantPendingFile, err)
			}

			_, ok := activeStore.TokenSnapshot("demo@example.com.json")
			if ok != tt.wantActiveLoaded {
				t.Fatalf("active store loaded = %v, want %v", ok, tt.wantActiveLoaded)
			}
			if tt.wantActiveLoaded {
				token, _ := activeStore.TokenSnapshot("demo@example.com.json")
				if token.PlanType != "plus" {
					t.Fatalf("PlanType = %q, want %q", token.PlanType, "plus")
				}
				if token.Email != "demo@example.com" {
					t.Fatalf("Email = %q, want %q", token.Email, "demo@example.com")
				}
			}
		})
	}
}
