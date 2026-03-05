package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
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
				writeAuthFileForTest(t, path, "token-"+name)
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

func TestTokenStoreRemoveTokenAndRefreshLockConcurrent(t *testing.T) {
	tests := []struct {
		name       string
		iterations int
	}{
		{
			name:       "concurrent remove and lock lookup",
			iterations: 2000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewTokenStore()
			now := time.Now().UTC()
			tokenID := "race.json"
			tokenPath := filepath.Join(t.TempDir(), tokenID)
			lockHits := 0

			var wg sync.WaitGroup
			wg.Go(func() {
				for range tt.iterations {
					store.UpsertToken(TokenState{ID: tokenID, Path: tokenPath, Token: "token"}, now)
					store.RemoveToken(tokenID)
				}
			})
			wg.Go(func() {
				for range tt.iterations {
					lock := store.RefreshLock(tokenID)
					lock.Lock()
					lockHits++
					lock.Unlock()
				}
			})
			wg.Wait()
			if lockHits == 0 {
				t.Fatal("lock should be acquired at least once")
			}
		})
	}
}

func writeAuthFileForTest(t *testing.T, path string, accessToken string) {
	t.Helper()
	payload := map[string]any{
		"tokens": map[string]any{
			"access_token": accessToken,
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
