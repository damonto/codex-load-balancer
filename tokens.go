package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultTokenWatchInterval = 10 * time.Second

type authFile struct {
	Tokens      *tokenFields `json:"tokens"`
	LastRefresh *string      `json:"last_refresh"`
}

type tokenFields struct {
	AccessToken  string `json:"access_token"`
	AccountID    string `json:"account_id"`
	RefreshToken string `json:"refresh_token"`
}

func loadTokensFromDir(store *TokenStore, dir string) error {
	_, err := loadTokensFromDirChanged(store, dir)
	return err
}

func loadTokensFromDirChanged(store *TokenStore, dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, fmt.Errorf("read token dir: %w", err)
	}

	existing := make(map[string]struct{}, len(entries))
	added := 0
	updated := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		existing[filepath.Clean(path)] = struct{}{}
		info, err := entry.Info()
		if err != nil {
			slog.Warn("token file stat", "path", path, "err", err)
			continue
		}
		if !store.ShouldReload(path, info.ModTime()) {
			continue
		}

		token, accountID, refreshToken, lastRefresh, err := parseAuthFile(path)
		if err != nil {
			_, removed := store.RemoveToken(entry.Name())
			store.NoteFileMod(path, info.ModTime())
			slog.Warn("token file parse", "path", path, "err", err, "cached_token_removed", removed)
			continue
		}
		state := TokenState{
			ID:           entry.Name(),
			Path:         path,
			Token:        token,
			AccountID:    accountID,
			RefreshToken: refreshToken,
			LastRefresh:  lastRefresh,
		}
		add, upd := store.UpsertToken(state, info.ModTime())
		if add {
			added++
		} else if upd {
			updated++
		}
	}

	if added > 0 || updated > 0 {
		slog.Info("tokens loaded", "added", added, "updated", updated)
	}
	removed := store.PruneMissingTokens(dir, existing)
	if len(removed) > 0 {
		slog.Info("tokens pruned", "removed", len(removed))
	}
	return added > 0 || updated > 0, nil
}

func parseAuthFile(path string) (token, accountID, refreshToken string, lastRefresh time.Time, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", time.Time{}, fmt.Errorf("read auth file: %w", err)
	}

	var payload authFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", "", "", time.Time{}, fmt.Errorf("decode auth file: %w", err)
	}

	if payload.Tokens != nil && payload.Tokens.AccessToken != "" {
		return payload.Tokens.AccessToken, payload.Tokens.AccountID, payload.Tokens.RefreshToken, parseLastRefresh(payload.LastRefresh), nil
	}
	return "", "", "", time.Time{}, errors.New("auth file missing access token")
}

func parseLastRefresh(raw *string) time.Time {
	if raw == nil || *raw == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, *raw)
	if err == nil {
		return parsed
	}
	parsed, err = time.Parse(time.RFC3339, *raw)
	if err == nil {
		return parsed
	}
	return time.Time{}
}

func reloadTokensAndSyncUsage(ctx context.Context, store *TokenStore, dir string, usageURL string, syncOpts usageSyncOptions) error {
	changed, err := loadTokensFromDirChanged(store, dir)
	if err != nil {
		return err
	}
	if changed {
		syncUsageNow(ctx, store, usageURL, syncOpts)
	}
	return nil
}

func runTokenWatcher(ctx context.Context, store *TokenStore, dir string, usageURL string, syncOpts usageSyncOptions) {
	ticker := time.NewTicker(defaultTokenWatchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := reloadTokensAndSyncUsage(ctx, store, dir, usageURL, syncOpts); err != nil {
				slog.Warn("token scan", "err", err)
			}
		}
	}
}
