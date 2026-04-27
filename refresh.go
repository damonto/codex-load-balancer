package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	refreshClientID      = "app_EMoamEEZ73f0CkXaXp7hrann"
	refreshScope         = "openid profile email"
	defaultRefreshWindow = 8 * 24 * time.Hour
	defaultRefreshGap    = 30 * time.Second
)

type refreshConfig struct {
	Interval time.Duration
	Debounce time.Duration
}

func defaultProxyRefreshConfig() refreshConfig {
	return refreshConfig{
		Interval: defaultRefreshWindow,
		Debounce: defaultRefreshGap,
	}
}

func normalizeRefreshConfig(cfg refreshConfig) refreshConfig {
	if cfg.Interval <= 0 {
		cfg.Interval = defaultRefreshWindow
	}
	if cfg.Debounce <= 0 {
		cfg.Debounce = defaultRefreshGap
	}
	return cfg
}

type refreshTokenError struct {
	permanent bool
	err       error
}

func (e refreshTokenError) Error() string {
	if e.err == nil {
		return "refresh token"
	}
	return e.err.Error()
}

func (e refreshTokenError) Unwrap() error {
	return e.err
}

func isPermanentRefreshError(err error) bool {
	var refreshErr refreshTokenError
	return errors.As(err, &refreshErr) && refreshErr.permanent
}

type refreshRequest struct {
	ClientID     string `json:"client_id"`
	GrantType    string `json:"grant_type"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

var refreshHTTPClient = &http.Client{Timeout: 15 * time.Second}

func maybeRefreshToken(ctx context.Context, store *TokenStore, tokenID string, cfg refreshConfig) (bool, error) {
	return maybeRefreshTokenWithMode(ctx, store, tokenID, false, cfg)
}

func maybeRefreshTokenIfStale(ctx context.Context, store *TokenStore, tokenID string, cfg refreshConfig) (bool, error) {
	return maybeRefreshTokenWithMode(ctx, store, tokenID, true, cfg)
}

func maybeRefreshTokenWithMode(ctx context.Context, store *TokenStore, tokenID string, refreshIfStale bool, cfg refreshConfig) (bool, error) {
	cfg = normalizeRefreshConfig(cfg)

	token, ok := store.TokenSnapshot(tokenID)
	if !ok {
		return false, errors.New("token not found")
	}
	initialToken := token
	if token.RefreshToken == "" {
		return false, nil
	}
	if refreshIfStale && !tokenNeedsRefresh(token.LastRefresh, time.Now(), cfg.Interval) {
		return false, nil
	}

	lock := store.RefreshLock(tokenID)
	lock.Lock()
	defer lock.Unlock()

	token, ok = store.TokenSnapshot(tokenID)
	if !ok {
		return false, errors.New("token not found")
	}
	if token.RefreshToken == "" {
		return false, nil
	}
	if refreshIfStale && !tokenNeedsRefresh(token.LastRefresh, time.Now(), cfg.Interval) {
		return tokenRefreshedSince(initialToken, token), nil
	}
	if !refreshIfStale && !token.LastRefresh.IsZero() && time.Since(token.LastRefresh) < cfg.Debounce {
		return tokenRefreshedSince(initialToken, token), nil
	}

	accessToken, refreshToken, err := refreshAccessToken(ctx, token.RefreshToken)
	if err != nil {
		return false, err
	}
	if refreshToken == "" {
		refreshToken = token.RefreshToken
	}

	if err := updateAuthFileTokens(token.Path, accessToken, refreshToken); err != nil {
		return false, err
	}

	store.UpdateCredentials(token.ID, accessToken, refreshToken)
	return true, nil
}

func tokenRefreshedSince(before TokenState, after TokenState) bool {
	if after.LastRefresh.After(before.LastRefresh) {
		return true
	}
	return after.Token != before.Token
}

func tokenNeedsRefresh(lastRefresh time.Time, now time.Time, interval time.Duration) bool {
	if lastRefresh.IsZero() {
		return false
	}
	return lastRefresh.Before(now.Add(-interval))
}

func refreshAccessToken(ctx context.Context, refreshToken string) (string, string, error) {
	requestBody, err := json.Marshal(refreshRequest{
		ClientID:     refreshClientID,
		GrantType:    "refresh_token",
		RefreshToken: refreshToken,
		Scope:        refreshScope,
	})
	if err != nil {
		return "", "", fmt.Errorf("encode refresh request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, refreshTokenURL, bytes.NewReader(requestBody))
	if err != nil {
		return "", "", fmt.Errorf("build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := refreshHTTPClient.Do(req)
	if err != nil {
		return "", "", refreshTokenError{
			permanent: false,
			err:       fmt.Errorf("send refresh request: %w", err),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", "", refreshTokenError{
			permanent: true,
			err:       errors.New("refresh token unauthorized"),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", refreshTokenError{
			permanent: false,
			err:       fmt.Errorf("refresh request status %d", resp.StatusCode),
		}
	}

	var payload refreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", "", refreshTokenError{
			permanent: false,
			err:       fmt.Errorf("decode refresh response: %w", err),
		}
	}
	if payload.AccessToken == "" {
		return "", "", refreshTokenError{
			permanent: false,
			err:       errors.New("refresh response missing access token"),
		}
	}
	return payload.AccessToken, payload.RefreshToken, nil
}

func updateAuthFileTokens(path string, accessToken string, refreshToken string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat auth file: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read auth file: %w", err)
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("decode auth file: %w", err)
	}

	tokens := make(map[string]json.RawMessage)
	if rawTokens, ok := payload["tokens"]; ok && len(rawTokens) > 0 {
		if err := json.Unmarshal(rawTokens, &tokens); err != nil {
			return fmt.Errorf("decode auth file tokens: %w", err)
		}
	}
	if tokens == nil {
		tokens = make(map[string]json.RawMessage)
	}
	accessTokenJSON, err := json.Marshal(accessToken)
	if err != nil {
		return fmt.Errorf("encode access token: %w", err)
	}
	tokens["access_token"] = accessTokenJSON
	if refreshToken != "" {
		refreshTokenJSON, err := json.Marshal(refreshToken)
		if err != nil {
			return fmt.Errorf("encode refresh token: %w", err)
		}
		tokens["refresh_token"] = refreshTokenJSON
	}
	tokensJSON, err := json.Marshal(tokens)
	if err != nil {
		return fmt.Errorf("encode auth file tokens: %w", err)
	}
	payload["tokens"] = tokensJSON
	lastRefreshJSON, err := json.Marshal(time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("encode last refresh: %w", err)
	}
	payload["last_refresh"] = lastRefreshJSON

	updated, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encode auth file: %w", err)
	}
	updated = append(updated, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(path), ".auth-update-*")
	if err != nil {
		return fmt.Errorf("create temp auth file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(updated); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp auth file: %w", err)
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("chmod temp auth file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp auth file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename auth file: %w", err)
	}
	return nil
}
