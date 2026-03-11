package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/damonto/codex-load-balancer/plus"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
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
			name:             "usage metadata overwrites token-derived values",
			initialAccountID: "account-1",
			initialEmail:     "legacy@example.com",
			initialPlanType:  "Legacy",
			usageUserID:      "user-1",
			usageAccountID:   "account-2",
			usageEmail:       "fresh@example.com",
			usagePlanType:    "plus",
			wantAccountID:    "account-2",
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
			name:             "user id does not overwrite account id",
			initialAccountID: "",
			initialEmail:     "legacy@example.com",
			initialPlanType:  "Legacy",
			usageUserID:      "user-fallback",
			usageAccountID:   "",
			usageEmail:       "",
			usagePlanType:    "",
			wantAccountID:    "",
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

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			defer ts.Close()

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
				if err := os.WriteFile(path, []byte(`{"tokens":{"access_token":"x"}}`), 0o644); err != nil {
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

			if got := removeTokenAfterUsageRemoval(store, tt.tokenID, "unauthorized"); got != tt.want {
				t.Fatalf("removeTokenAfterUsageRemoval() = %v, want %v", got, tt.want)
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

func TestSyncOneTokenRemovesFreePlan(t *testing.T) {
	store := NewTokenStore()
	path := filepath.Join(t.TempDir(), "active.json")
	if err := os.WriteFile(path, []byte(`{"tokens":{"access_token":"x"}}`), 0o644); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	store.UpsertToken(TokenState{
		ID:        "active.json",
		Path:      path,
		Token:     "access-token",
		AccountID: "account-1",
	}, time.Now().UTC())

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	defer ts.Close()

	removed := syncOneToken(context.Background(), store, ts.Client(), ts.URL, TokenRef{
		ID:        "active.json",
		Token:     "access-token",
		AccountID: "account-1",
	})
	if !removed {
		t.Fatal("syncOneToken() should remove free-plan token")
	}
	if _, ok := store.TokenSnapshot("active.json"); ok {
		t.Fatal("token should be removed from store")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("token file should be removed, stat err = %v", err)
	}
}

func TestSyncOneTokenRefreshesUnauthorizedBeforeRemoval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active.json")
	if err := os.WriteFile(path, []byte(`{"last_refresh":"2026-01-01T00:00:00Z","tokens":{"access_token":"old-access","refresh_token":"refresh-token","account_id":"account-1"}}`), 0o644); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	store := NewTokenStore()
	store.UpsertToken(TokenState{
		ID:           "active.json",
		Path:         path,
		Token:        "old-access",
		AccountID:    "account-1",
		RefreshToken: "refresh-token",
	}, time.Now().UTC())

	originalClient := refreshHTTPClient
	t.Cleanup(func() { refreshHTTPClient = originalClient })
	refreshHTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != refreshTokenURL {
			t.Fatalf("refresh URL = %q, want %q", req.URL.String(), refreshTokenURL)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"access_token":"new-access","refresh_token":"new-refresh"}`)),
		}, nil
	})}

	requestCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		switch r.Header.Get("Authorization") {
		case "Bearer old-access":
			w.WriteHeader(http.StatusUnauthorized)
		case "Bearer new-access":
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(rateLimitStatusPayload{
				PlanType: "plus",
				RateLimit: &rateLimitStatusDetails{
					PrimaryWindow: &rateLimitWindowSnapshot{UsedPercent: 10, LimitWindowSeconds: 18000},
				},
			}); err != nil {
				t.Fatalf("encode usage response: %v", err)
			}
		default:
			t.Fatalf("unexpected Authorization header %q", r.Header.Get("Authorization"))
		}
	}))
	defer ts.Close()

	removed := syncOneToken(context.Background(), store, ts.Client(), ts.URL, TokenRef{
		ID:        "active.json",
		Token:     "old-access",
		AccountID: "account-1",
	})
	if removed {
		t.Fatal("syncOneToken() should keep token after successful refresh")
	}
	if requestCount != 2 {
		t.Fatalf("usage requests = %d, want %d", requestCount, 2)
	}

	token, ok := store.TokenSnapshot("active.json")
	if !ok {
		t.Fatal("token should remain in store")
	}
	if token.Token != "new-access" {
		t.Fatalf("Token = %q, want %q", token.Token, "new-access")
	}
	if token.RefreshToken != "new-refresh" {
		t.Fatalf("RefreshToken = %q, want %q", token.RefreshToken, "new-refresh")
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read updated token file: %v", err)
	}
	if !strings.Contains(string(updated), "new-access") {
		t.Fatalf("updated token file = %q, want new access token", string(updated))
	}
}

func TestSyncUsageOnceTopsUpRemovedFreePlanToken(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "active.json")
	if err := os.WriteFile(path, []byte(`{"tokens":{"access_token":"x"}}`), 0o644); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	store := NewTokenStore()
	store.UpsertToken(TokenState{
		ID:        "active.json",
		Path:      path,
		Token:     "access-token",
		AccountID: "account-1",
	}, time.Now().UTC())

	var registerCalls atomic.Int32
	originalRegister := registerCodexCredential
	t.Cleanup(func() {
		registerCodexCredential = originalRegister
	})

	registerCodexCredential = func(ctx context.Context, opts plus.RegisterOptions) (plus.RegisterResult, error) {
		registerCalls.Add(1)
		return plus.RegisterResult{
			Email:     "new@example.com",
			AccountID: "account-new",
			FilePath:  filepath.Join(opts.DataDir, "new.json"),
		}, nil
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	defer ts.Close()

	syncUsageOnce(context.Background(), store, ts.Client(), dataDir, ts.URL, usageSyncOptions{Concurrency: 1}, topUpOptions{RegisterWorkers: 1})

	if got := registerCalls.Load(); got != 1 {
		t.Fatalf("register calls = %d, want %d", got, 1)
	}
	if _, ok := store.TokenSnapshot("active.json"); ok {
		t.Fatal("free-plan token should be removed from store")
	}
}
