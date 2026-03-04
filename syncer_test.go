package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRemoveTokenAfterUsageUnauthorized(t *testing.T) {
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

			if got := removeTokenAfterUsageUnauthorized(store, tt.tokenID); got != tt.want {
				t.Fatalf("removeTokenAfterUsageUnauthorized() = %v, want %v", got, tt.want)
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
