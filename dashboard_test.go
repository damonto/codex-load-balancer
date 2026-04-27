package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestHandleDashboardOverviewFiltersAccountsWithoutActiveTokens(t *testing.T) {
	tests := []struct {
		name            string
		storeTokens     []TokenState
		usageRecords    []UsageRecord
		wantAccountKeys []string
	}{
		{
			name: "returns only accounts with active token files",
			storeTokens: []TokenState{
				{
					ID:        "active-token.json",
					Path:      "data/active-token.json",
					AccountID: "acct-active",
				},
			},
			usageRecords: []UsageRecord{
				{
					AccountKey:      "acct-active",
					TokenID:         "active-token.json",
					Path:            "/v1/responses",
					StatusCode:      http.StatusOK,
					InputTokens:     10,
					CachedTokens:    2,
					OutputTokens:    5,
					ReasoningTokens: 1,
					CreatedAt:       time.Now().UTC(),
				},
				{
					AccountKey:      "acct-removed",
					TokenID:         "removed-token.json",
					Path:            "/v1/responses",
					StatusCode:      http.StatusOK,
					InputTokens:     7,
					CachedTokens:    1,
					OutputTokens:    3,
					ReasoningTokens: 0,
					CreatedAt:       time.Now().UTC(),
				},
			},
			wantAccountKeys: []string{"acct-active"},
		},
		{
			name:         "returns empty account list when all token files are removed",
			storeTokens:  nil,
			usageRecords: []UsageRecord{{AccountKey: "acct-removed", TokenID: "removed-token.json", Path: "/v1/responses", StatusCode: http.StatusOK, CreatedAt: time.Now().UTC()}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usageDB, err := openUsageDB(filepath.Join(t.TempDir(), "clb.db"))
			if err != nil {
				t.Fatalf("openUsageDB() error = %v", err)
			}
			defer usageDB.Close()

			for _, rec := range tt.usageRecords {
				if err := usageDB.InsertUsage(context.Background(), rec); err != nil {
					t.Fatalf("InsertUsage() error = %v", err)
				}
			}

			store := NewTokenStore()
			modTime := time.Now().UTC()
			for _, token := range tt.storeTokens {
				store.UpsertToken(token, modTime)
			}

			server := &Server{
				store:   store,
				usageDB: usageDB,
			}

			req := httptest.NewRequest(http.MethodGet, "/stats/overview", nil)
			rr := httptest.NewRecorder()
			server.handleDashboardOverview(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d, body = %s", rr.Code, http.StatusOK, rr.Body.String())
			}

			var resp dashboardOverviewResponse
			if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}

			gotAccountKeys := make([]string, 0, len(resp.Accounts))
			for _, account := range resp.Accounts {
				gotAccountKeys = append(gotAccountKeys, account.AccountKey)
				if len(account.TokenIDs) == 0 {
					t.Fatalf("account %q has empty token_ids", account.AccountKey)
				}
			}

			slices.Sort(gotAccountKeys)
			slices.Sort(tt.wantAccountKeys)
			if !slices.Equal(gotAccountKeys, tt.wantAccountKeys) {
				t.Fatalf("account keys = %v, want %v", gotAccountKeys, tt.wantAccountKeys)
			}
		})
	}
}

func TestDashboardPageDoesNotPrefetchAccountDetails(t *testing.T) {
	tests := []struct {
		name        string
		wantPresent []string
		wantAbsent  []string
	}{
		{
			name:        "dashboard page only fetches overview data on load",
			wantPresent: []string{"fetch('stats/overview')"},
			wantAbsent:  []string{"stats/account?account_key=", "stats/accounts/details", "detailsCache", "syncDetails("},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/stats", nil)
			rr := httptest.NewRecorder()
			newMux(&Server{}).ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
			}

			body := rr.Body.String()
			for _, want := range tt.wantPresent {
				if !strings.Contains(body, want) {
					t.Fatalf("dashboard page missing %q", want)
				}
			}
			for _, want := range tt.wantAbsent {
				if strings.Contains(body, want) {
					t.Fatalf("dashboard page unexpectedly contains %q", want)
				}
			}
		})
	}
}

func TestDashboardRoutesRejectNonGETMethods(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{
			name: "dashboard page rejects post",
			path: "/stats",
		},
		{
			name: "overview rejects post",
			path: "/stats/overview",
		},
		{
			name: "asset route rejects post",
			path: "/stats/assets/app.js",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, nil)
			rr := httptest.NewRecorder()

			newMux(&Server{}).ServeHTTP(rr, req)

			if rr.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}
