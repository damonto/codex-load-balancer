package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func newIPv4TestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp4: %v", err)
	}

	ts := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: handler},
	}
	ts.Start()
	t.Cleanup(ts.Close)
	return ts
}

func TestSyncOneTokenUpdatesPlanTypeFromUsage(t *testing.T) {
	tests := []struct {
		name             string
		initialAccountID string
		initialEmail     string
		initialPlanType  string
		usageUserID      string
		usageAccountID   string
		usageEmail       string
		usagePlanType    string
		wantAccountID    string
		wantEmail        string
		wantPlanType     string
	}{
		{
			name:             "usage metadata keeps token-derived account id",
			initialAccountID: "account-1",
			initialEmail:     "legacy@example.com",
			initialPlanType:  "Legacy",
			usageUserID:      "user-1",
			usageAccountID:   "account-2",
			usageEmail:       "fresh@example.com",
			usagePlanType:    "plus",
			wantAccountID:    "account-1",
			wantEmail:        "fresh@example.com",
			wantPlanType:     "plus",
		},
		{
			name:             "blank usage metadata keeps current values",
			initialAccountID: "account-1",
			initialEmail:     "legacy@example.com",
			initialPlanType:  "Legacy",
			usageAccountID:   "",
			usageEmail:       "",
			usagePlanType:    "",
			wantAccountID:    "account-1",
			wantEmail:        "legacy@example.com",
			wantPlanType:     "Legacy",
		},
		{
			name:             "usage account id fills blank token account id",
			initialAccountID: "",
			initialEmail:     "legacy@example.com",
			initialPlanType:  "Legacy",
			usageUserID:      "user-fallback",
			usageAccountID:   "account-filled",
			usageEmail:       "",
			usagePlanType:    "",
			wantAccountID:    "account-filled",
			wantEmail:        "legacy@example.com",
			wantPlanType:     "Legacy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewTokenStore()
			store.UpsertToken(TokenState{
				ID:        "active.json",
				Path:      filepath.Join(t.TempDir(), "active.json"),
				Token:     "access-token",
				AccountID: tt.initialAccountID,
				Email:     tt.initialEmail,
				PlanType:  tt.initialPlanType,
			}, time.Now().UTC())

			ts := newIPv4TestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
					t.Fatalf("Authorization = %q, want %q", got, "Bearer access-token")
				}
				if got := r.Header.Get("ChatGPT-Account-Id"); got != "account-1" {
					t.Fatalf("ChatGPT-Account-Id = %q, want %q", got, "account-1")
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(rateLimitStatusPayload{
					UserID:    tt.usageUserID,
					AccountID: tt.usageAccountID,
					Email:     tt.usageEmail,
					PlanType:  tt.usagePlanType,
					RateLimit: &rateLimitStatusDetails{
						PrimaryWindow: &rateLimitWindowSnapshot{
							UsedPercent:        10,
							LimitWindowSeconds: 18000,
						},
						SecondaryWindow: &rateLimitWindowSnapshot{
							UsedPercent:        20,
							LimitWindowSeconds: 604800,
						},
					},
				}); err != nil {
					t.Fatalf("encode response: %v", err)
				}
			}))

			removed := syncOneToken(context.Background(), store, ts.Client(), ts.URL, TokenRef{
				ID:        "active.json",
				Token:     "access-token",
				AccountID: "account-1",
			})
			if removed {
				t.Fatal("syncOneToken() should not remove token")
			}

			token, ok := store.TokenSnapshot("active.json")
			if !ok {
				t.Fatal("token should remain in store")
			}
			if token.PlanType != tt.wantPlanType {
				t.Fatalf("PlanType = %q, want %q", token.PlanType, tt.wantPlanType)
			}
			if token.AccountID != tt.wantAccountID {
				t.Fatalf("AccountID = %q, want %q", token.AccountID, tt.wantAccountID)
			}
			if token.Email != tt.wantEmail {
				t.Fatalf("Email = %q, want %q", token.Email, tt.wantEmail)
			}
			if !token.FiveHour.Known || token.FiveHour.UsedPercent != 10 {
				t.Fatalf("FiveHour = %+v, want known window with used percent 10", token.FiveHour)
			}
			if !token.Weekly.Known || token.Weekly.UsedPercent != 20 {
				t.Fatalf("Weekly = %+v, want known window with used percent 20", token.Weekly)
			}
		})
	}
}

func TestRemoveTokenAfterUsageRemovalUnauthorized(t *testing.T) {
	tests := []struct {
		name       string
		tokenID    string
		hasToken   bool
		createFile bool
		want       bool
	}{
		{
			name:       "remove token and file",
			tokenID:    "active.json",
			hasToken:   true,
			createFile: true,
			want:       true,
		},
		{
			name:       "remove token when file already missing",
			tokenID:    "missing-file.json",
			hasToken:   true,
			createFile: false,
			want:       true,
		},
		{
			name:       "ignore missing token",
			tokenID:    "not-found.json",
			hasToken:   false,
			createFile: false,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewTokenStore()
			now := time.Now().UTC()
			path := filepath.Join(t.TempDir(), tt.tokenID)

			if tt.createFile {
				if err := writeSessionFileForTest(path, "x", "account-1", "", "plus"); err != nil {
					t.Fatalf("write token file: %v", err)
				}
			}

			if tt.hasToken {
				store.UpsertToken(TokenState{
					ID:        tt.tokenID,
					Path:      path,
					Token:     "token-value",
					AccountID: "account-1",
				}, now)
				store.SetSession("session-1", tt.tokenID)
			}

			if got := removeTokenAndFile(store, tt.tokenID, "unauthorized"); got != tt.want {
				t.Fatalf("removeTokenAndFile() = %v, want %v", got, tt.want)
			}

			if _, ok := store.TokenSnapshot(tt.tokenID); ok {
				t.Fatalf("token %q should be removed from store", tt.tokenID)
			}

			if _, ok := store.SessionToken("session-1"); ok {
				t.Fatalf("session binding should be removed")
			}

			_, err := os.Stat(path)
			if tt.createFile {
				if !os.IsNotExist(err) {
					t.Fatalf("token file should be removed, stat err = %v", err)
				}
			}
		})
	}
}

func TestSyncOneTokenKeepsFreePlan(t *testing.T) {
	store := NewTokenStore()
	path := filepath.Join(t.TempDir(), "active.json")
	if err := writeSessionFileForTest(path, "x", "account-1", "", "plus"); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	store.UpsertToken(TokenState{
		ID:        "active.json",
		Path:      path,
		Token:     "access-token",
		AccountID: "account-1",
	}, time.Now().UTC())

	ts := newIPv4TestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(rateLimitStatusPayload{
			PlanType: "free",
			RateLimit: &rateLimitStatusDetails{
				PrimaryWindow: &rateLimitWindowSnapshot{UsedPercent: 1, LimitWindowSeconds: 18000},
			},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))

	removed := syncOneToken(context.Background(), store, ts.Client(), ts.URL, TokenRef{
		ID:        "active.json",
		Token:     "access-token",
		AccountID: "account-1",
	})
	if removed {
		t.Fatal("syncOneToken() should keep free-plan token")
	}
	if _, ok := store.TokenSnapshot("active.json"); !ok {
		t.Fatal("token should remain in store")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("token file should remain, stat err = %v", err)
	}
}

func TestSyncUsageOnceKeepsFreePlan(t *testing.T) {
	store := NewTokenStore()
	path := filepath.Join(t.TempDir(), "active.json")
	if err := writeSessionFileForTest(path, "x", "account-1", "", "free"); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	store.UpsertToken(TokenState{
		ID:        "active.json",
		Path:      path,
		Token:     "access-token",
		AccountID: "account-1",
	}, time.Now().UTC())

	ts := newIPv4TestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(rateLimitStatusPayload{
			PlanType: "free",
			RateLimit: &rateLimitStatusDetails{
				PrimaryWindow: &rateLimitWindowSnapshot{UsedPercent: 1, LimitWindowSeconds: 18000},
			},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))

	syncUsageOnce(context.Background(), store, ts.Client(), ts.URL, usageSyncOptions{Concurrency: 1})
	if _, ok := store.TokenSnapshot("active.json"); !ok {
		t.Fatal("free-plan token should remain in store")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("token file should remain, stat err = %v", err)
	}
}

func TestSyncUsageNowRunsOnce(t *testing.T) {
	tests := []struct {
		name         string
		usagePlan    string
		wantRequests int32
	}{
		{
			name:         "syncs loaded token before serving",
			usagePlan:    "plus",
			wantRequests: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewTokenStore()
			store.UpsertToken(TokenState{
				ID:        "active.json",
				Token:     "access-token",
				AccountID: "account-1",
			}, time.Now().UTC())

			var requests atomic.Int32
			ts := newIPv4TestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(rateLimitStatusPayload{
					PlanType: tt.usagePlan,
					RateLimit: &rateLimitStatusDetails{
						PrimaryWindow: &rateLimitWindowSnapshot{UsedPercent: 1, LimitWindowSeconds: 18000},
					},
				}); err != nil {
					t.Fatalf("encode response: %v", err)
				}
			}))

			syncUsageNow(context.Background(), store, ts.URL, usageSyncOptions{Concurrency: 1})

			if got := requests.Load(); got != tt.wantRequests {
				t.Fatalf("requests = %d, want %d", got, tt.wantRequests)
			}
			token, ok := store.TokenSnapshot("active.json")
			if !ok {
				t.Fatal("token should remain in store")
			}
			if token.PlanType != tt.usagePlan {
				t.Fatalf("PlanType = %q, want %q", token.PlanType, tt.usagePlan)
			}
		})
	}
}

func TestSyncOneTokenRemovesUnauthorizedToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active.json")
	if err := writeSessionFileForTest(path, "old-access", "account-1", "demo@example.com", "plus"); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	store := NewTokenStore()
	store.UpsertToken(TokenState{
		ID:        "active.json",
		Path:      path,
		Token:     "old-access",
		AccountID: "account-1",
	}, time.Now().UTC())
	ts := newIPv4TestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))

	removed := syncOneToken(context.Background(), store, ts.Client(), ts.URL, TokenRef{
		ID:        "active.json",
		Token:     "old-access",
		AccountID: "account-1",
	})
	if !removed {
		t.Fatal("syncOneToken() should remove unauthorized token")
	}
	if _, ok := store.TokenSnapshot("active.json"); ok {
		t.Fatal("token should be removed from store")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("token file should be removed, stat err = %v", err)
	}
}
