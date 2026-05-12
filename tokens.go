package main

import (
	"context"
	"encoding/base64"
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
	IDToken      string `json:"id_token"`
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

		token, userID, accountID, email, refreshToken, lastRefresh, err := parseAuthFile(path)
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
			UserID:       userID,
			AccountID:    accountID,
			Email:        email,
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

func parseAuthFile(path string) (token, userID, accountID, email, refreshToken string, lastRefresh time.Time, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", "", "", time.Time{}, fmt.Errorf("read auth file: %w", err)
	}

	var payload authFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", "", "", "", "", time.Time{}, fmt.Errorf("decode auth file: %w", err)
	}

	if payload.Tokens != nil && payload.Tokens.AccessToken != "" {
		userID, email := parseIDTokenIdentity(payload.Tokens.IDToken)
		return payload.Tokens.AccessToken, userID, payload.Tokens.AccountID, email, payload.Tokens.RefreshToken, parseLastRefresh(payload.LastRefresh), nil
	}
	return "", "", "", "", "", time.Time{}, errors.New("auth file missing access token")
}

func parseIDTokenIdentity(idToken string) (userID string, email string) {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return "", ""
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ""
	}

	var claims struct {
		Subject string `json:"sub"`
		Email   string `json:"email"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", ""
	}
	return strings.TrimSpace(claims.Subject), strings.TrimSpace(claims.Email)
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

func reloadTokensAndSyncUsage(ctx context.Context, store *TokenStore, usageDB *UsageDB, dir string, usageURL string, syncOpts usageSyncOptions) error {
	changed, err := loadTokensFromDirChanged(store, dir)
	if err != nil {
		return err
	}
	if changed {
		syncUsageNow(ctx, store, usageDB, usageURL, syncOpts)
	}
	return nil
}

func runTokenWatcher(ctx context.Context, store *TokenStore, usageDB *UsageDB, dir string, usageURL string, syncOpts usageSyncOptions) {
	ticker := time.NewTicker(defaultTokenWatchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := reloadTokensAndSyncUsage(ctx, store, usageDB, dir, usageURL, syncOpts); err != nil {
				slog.Warn("token scan", "err", err)
			}
		}
	}
}
