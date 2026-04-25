package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoadTokensFromDirPrunesMissingFiles(t *testing.T) {
	tests := []struct {
		name        string
		initial     []string
		remove      []string
		wantPresent []string
	}{
		{
			name:        "remove one file keeps other tokens",
			initial:     []string{"a.json", "b.json"},
			remove:      []string{"b.json"},
			wantPresent: []string{"a.json"},
		},
		{
			name:        "remove all token files",
			initial:     []string{"a.json", "b.json"},
			remove:      []string{"a.json", "b.json"},
			wantPresent: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewTokenStore()
			dir := t.TempDir()

			for _, name := range tt.initial {
				path := filepath.Join(dir, name)
				writeTokenSessionFileForTest(t, path, "token-"+name)
			}

			if err := loadTokensFromDir(store, dir); err != nil {
				t.Fatalf("loadTokensFromDir() initial error = %v", err)
			}

			for _, name := range tt.initial {
				store.SetSession("session-"+name, name)
			}

			for _, name := range tt.remove {
				if err := os.Remove(filepath.Join(dir, name)); err != nil {
					t.Fatalf("remove token file %q: %v", name, err)
				}
			}

			if err := loadTokensFromDir(store, dir); err != nil {
				t.Fatalf("loadTokensFromDir() second error = %v", err)
			}

			for _, name := range tt.wantPresent {
				if _, ok := store.TokenSnapshot(name); !ok {
					t.Fatalf("token %q should remain in store", name)
				}
			}

			for _, name := range tt.remove {
				if _, ok := store.TokenSnapshot(name); ok {
					t.Fatalf("token %q should be pruned from store", name)
				}
				if _, ok := store.SessionToken("session-" + name); ok {
					t.Fatalf("session for removed token %q should be cleared", name)
				}
			}
		})
	}
}

func TestLoadTokensFromDirIgnoresSessionMetadata(t *testing.T) {
	tests := []struct {
		name     string
		email    string
		planType string
	}{
		{
			name:     "session metadata present",
			email:    "demo@example.com",
			planType: "free",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewTokenStore()
			dir := t.TempDir()
			path := filepath.Join(dir, "active.json")
			if err := writeSessionFileForTest(path, "token-active", "account-1", tt.email, tt.planType); err != nil {
				t.Fatalf("write session file: %v", err)
			}

			if err := loadTokensFromDir(store, dir); err != nil {
				t.Fatalf("loadTokensFromDir() error = %v", err)
			}

			token, ok := store.TokenSnapshot("active.json")
			if !ok {
				t.Fatal("token should be loaded")
			}
			if token.Email != "" {
				t.Fatalf("Email = %q, want empty", token.Email)
			}
			if token.PlanType != "" {
				t.Fatalf("PlanType = %q, want empty", token.PlanType)
			}
		})
	}
}

func TestReloadTokensAndSyncUsageSyncsAddedOrUpdatedTokens(t *testing.T) {
	tests := []struct {
		name      string
		seedToken string
		nextToken string
	}{
		{
			name:      "added token",
			nextToken: "new-access",
		},
		{
			name:      "updated token",
			seedToken: "old-access",
			nextToken: "new-access",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewTokenStore()
			dir := t.TempDir()
			path := filepath.Join(dir, "active.json")

			if tt.seedToken != "" {
				if err := writeSessionFileForTest(path, tt.seedToken, "account-1", "", ""); err != nil {
					t.Fatalf("write seed session file: %v", err)
				}
				if err := loadTokensFromDir(store, dir); err != nil {
					t.Fatalf("loadTokensFromDir() seed error = %v", err)
				}
			}
			if err := writeSessionFileForTest(path, tt.nextToken, "account-1", "", ""); err != nil {
				t.Fatalf("write next session file: %v", err)
			}
			nextModTime := time.Now().UTC().Add(2 * time.Second)
			if err := os.Chtimes(path, nextModTime, nextModTime); err != nil {
				t.Fatalf("Chtimes() error = %v", err)
			}

			var requests atomic.Int32
			ts := newIPv4TestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if got := r.Header.Get("Authorization"); got != "Bearer "+tt.nextToken {
					t.Fatalf("Authorization = %q, want %q", got, "Bearer "+tt.nextToken)
				}
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

			if err := reloadTokensAndSyncUsage(context.Background(), store, dir, ts.URL, usageSyncOptions{Concurrency: 1}); err != nil {
				t.Fatalf("reloadTokensAndSyncUsage() error = %v", err)
			}

			if got := requests.Load(); got != 1 {
				t.Fatalf("requests = %d, want 1", got)
			}
			token, ok := store.TokenSnapshot("active.json")
			if !ok {
				t.Fatal("token should be loaded")
			}
			if token.Token != tt.nextToken {
				t.Fatalf("Token = %q, want %q", token.Token, tt.nextToken)
			}
			if token.PlanType != "free" {
				t.Fatalf("PlanType = %q, want %q", token.PlanType, "free")
			}
		})
	}
}

func TestTokenStorePruneMissingTokensScoping(t *testing.T) {
	tests := []struct {
		name           string
		existingInA    []string
		wantRemoved    []string
		wantRemainInA  []string
		wantRemainInB  []string
		sessionRemoved []string
	}{
		{
			name:           "only prune target directory",
			existingInA:    []string{"a-keep.json"},
			wantRemoved:    []string{"a-drop.json"},
			wantRemainInA:  []string{"a-keep.json"},
			wantRemainInB:  []string{"b-keep.json"},
			sessionRemoved: []string{"a-drop.json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewTokenStore()
			dirA := filepath.Join(t.TempDir(), "a")
			dirB := filepath.Join(t.TempDir(), "b")
			if err := os.MkdirAll(dirA, 0o755); err != nil {
				t.Fatalf("create dirA: %v", err)
			}
			if err := os.MkdirAll(dirB, 0o755); err != nil {
				t.Fatalf("create dirB: %v", err)
			}

			now := time.Now().UTC()
			tokenAKeep := TokenState{ID: "a-keep.json", Path: filepath.Join(dirA, "a-keep.json"), Token: "a-keep"}
			tokenADrop := TokenState{ID: "a-drop.json", Path: filepath.Join(dirA, "a-drop.json"), Token: "a-drop"}
			tokenBKeep := TokenState{ID: "b-keep.json", Path: filepath.Join(dirB, "b-keep.json"), Token: "b-keep"}
			store.UpsertToken(tokenAKeep, now)
			store.UpsertToken(tokenADrop, now)
			store.UpsertToken(tokenBKeep, now)
			store.SetSession("session-a-drop.json", "a-drop.json")

			existing := make(map[string]struct{}, len(tt.existingInA))
			for _, name := range tt.existingInA {
				existing[filepath.Clean(filepath.Join(dirA, name))] = struct{}{}
			}

			removed := store.PruneMissingTokens(dirA, existing)
			gotRemoved := make([]string, 0, len(removed))
			for _, token := range removed {
				gotRemoved = append(gotRemoved, token.ID)
			}
			slices.Sort(gotRemoved)
			wantRemoved := append([]string(nil), tt.wantRemoved...)
			slices.Sort(wantRemoved)
			if !slices.Equal(gotRemoved, wantRemoved) {
				t.Fatalf("PruneMissingTokens() removed = %v, want %v", gotRemoved, wantRemoved)
			}

			for _, id := range tt.wantRemainInA {
				if _, ok := store.TokenSnapshot(id); !ok {
					t.Fatalf("token %q should remain in dirA", id)
				}
			}
			for _, id := range tt.wantRemainInB {
				if _, ok := store.TokenSnapshot(id); !ok {
					t.Fatalf("token %q should remain in dirB", id)
				}
			}
			for _, id := range tt.wantRemoved {
				if _, ok := store.TokenSnapshot(id); ok {
					t.Fatalf("token %q should be removed", id)
				}
			}
			for _, id := range tt.sessionRemoved {
				if _, ok := store.SessionToken("session-" + id); ok {
					t.Fatalf("session for %q should be removed", id)
				}
			}
		})
	}
}

func TestTokenStoreUpsertTokenPreservesUsageMetadataOnReload(t *testing.T) {
	tests := []struct {
		name          string
		reloadToken   TokenState
		wantAccountID string
		wantEmail     string
		wantPlanType  string
	}{
		{
			name: "blank reload metadata keeps usage values",
			reloadToken: TokenState{
				ID:    "active.json",
				Path:  "/tmp/active.json",
				Token: "new-token",
			},
			wantAccountID: "account-usage",
			wantEmail:     "usage@example.com",
			wantPlanType:  "plus",
		},
		{
			name: "non blank reload account id still updates",
			reloadToken: TokenState{
				ID:        "active.json",
				Path:      "/tmp/active.json",
				Token:     "new-token",
				AccountID: "account-file",
			},
			wantAccountID: "account-file",
			wantEmail:     "usage@example.com",
			wantPlanType:  "plus",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewTokenStore()
			store.UpsertToken(TokenState{
				ID:        "active.json",
				Path:      "/tmp/active.json",
				Token:     "old-token",
				AccountID: "account-usage",
				Email:     "usage@example.com",
				PlanType:  "plus",
			}, time.Now().UTC())

			store.UpsertToken(tt.reloadToken, time.Now().UTC())

			token, ok := store.TokenSnapshot("active.json")
			if !ok {
				t.Fatal("token should remain in store")
			}
			if token.AccountID != tt.wantAccountID {
				t.Fatalf("AccountID = %q, want %q", token.AccountID, tt.wantAccountID)
			}
			if token.Email != tt.wantEmail {
				t.Fatalf("Email = %q, want %q", token.Email, tt.wantEmail)
			}
			if token.PlanType != tt.wantPlanType {
				t.Fatalf("PlanType = %q, want %q", token.PlanType, tt.wantPlanType)
			}
		})
	}
}

func TestLoadTokensFromDirRemovesCachedTokenWhenFileTurnsInvalid(t *testing.T) {
	tests := []struct {
		name        string
		invalidData string
	}{
		{
			name:        "invalid json",
			invalidData: `{"tokens":`,
		},
		{
			name:        "missing access token",
			invalidData: `{"tokens":{"refresh_token":"still-here"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewTokenStore()
			dir := t.TempDir()
			path := filepath.Join(dir, "active.json")
			writeTokenSessionFileForTest(t, path, "token-active")

			if err := loadTokensFromDir(store, dir); err != nil {
				t.Fatalf("loadTokensFromDir() initial error = %v", err)
			}
			store.SetSession("session-active", "active.json")

			if err := os.WriteFile(path, []byte(tt.invalidData), 0o644); err != nil {
				t.Fatalf("write invalid auth file: %v", err)
			}
			modTime := time.Now().UTC().Add(2 * time.Second)
			if err := os.Chtimes(path, modTime, modTime); err != nil {
				t.Fatalf("Chtimes() error = %v", err)
			}

			if err := loadTokensFromDir(store, dir); err != nil {
				t.Fatalf("loadTokensFromDir() second error = %v", err)
			}

			if _, ok := store.TokenSnapshot("active.json"); ok {
				t.Fatal("token should be removed from store after invalid reload")
			}
			if _, ok := store.SessionToken("session-active"); ok {
				t.Fatal("session should be cleared after invalid reload")
			}
		})
	}
}

func writeTokenSessionFileForTest(t *testing.T, path string, accessToken string) {
	t.Helper()
	payload := map[string]any{
		"auth_mode":    "chatgpt",
		"last_refresh": time.Now().UTC().Format(time.RFC3339Nano),
		"created_at":   time.Now().UTC().Format(time.RFC3339Nano),
		"tokens": map[string]any{
			"access_token":  accessToken,
			"account_id":    "",
			"refresh_token": "",
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal auth payload: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write auth file: %v", err)
	}
}
