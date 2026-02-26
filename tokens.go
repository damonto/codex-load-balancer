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

type authFile struct {
	Tokens      *tokenFields `json:"tokens"`
	LastRefresh *string      `json:"last_refresh"`
}

type tokenFields struct {
	AccessToken  string `json:"access_token"`
	AccountID    string `json:"account_id"`
	RefreshToken string `json:"refresh_token"`
}

// parseJWTClaims extracts email and plan type from a ChatGPT JWT access token.
// The JWT payload carries these under the standard "email" claim and the
// "https://api.openai.com/auth" namespace respectively.
func parseJWTClaims(accessToken string) (email, planType string) {
	parts := strings.SplitN(accessToken, ".", 3)
	if len(parts) != 3 {
		return "", ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ""
	}
	var claims struct {
		Email   string `json:"email"`
		Profile struct {
			Email string `json:"email"`
		} `json:"https://api.openai.com/profile"`
		Auth struct {
			PlanType string `json:"chatgpt_plan_type"`
		} `json:"https://api.openai.com/auth"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", ""
	}
	email = claims.Email
	if email == "" {
		email = claims.Profile.Email
	}
	if p := claims.Auth.PlanType; p != "" {
		planType = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
	}
	return email, planType
}

func loadTokensFromDir(store *TokenStore, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read token dir: %w", err)
	}

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
		info, err := entry.Info()
		if err != nil {
			slog.Warn("token file stat", "path", path, "err", err)
			continue
		}
		if !store.ShouldReload(path, info.ModTime()) {
			continue
		}

		token, accountID, refreshToken, lastRefresh, email, planType, err := parseAuthFile(path)
		if err != nil {
			slog.Warn("token file parse", "path", path, "err", err)
			continue
		}

		state := TokenState{
			ID:           entry.Name(),
			Path:         path,
			Token:        token,
			AccountID:    accountID,
			RefreshToken: refreshToken,
			LastRefresh:  lastRefresh,
			Email:        email,
			PlanType:     planType,
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
	return nil
}

func parseAuthFile(path string) (token, accountID, refreshToken string, lastRefresh time.Time, email, planType string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", time.Time{}, "", "", fmt.Errorf("read auth file: %w", err)
	}

	var payload authFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", "", "", time.Time{}, "", "", fmt.Errorf("decode auth file: %w", err)
	}

	if payload.Tokens != nil && payload.Tokens.AccessToken != "" {
		email, planType = parseJWTClaims(payload.Tokens.AccessToken)
		return payload.Tokens.AccessToken, payload.Tokens.AccountID, payload.Tokens.RefreshToken, parseLastRefresh(payload.LastRefresh), email, planType, nil
	}
	return "", "", "", time.Time{}, "", "", errors.New("missing tokens.access_token")
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

func runTokenWatcher(ctx context.Context, store *TokenStore, dir string) {
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := loadTokensFromDir(store, dir); err != nil {
				slog.Warn("token scan", "err", err)
			}
		}
	}
}
