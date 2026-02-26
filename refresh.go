package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type refreshTokenError struct {
	permanent bool
	err       error
}

func (e refreshTokenError) Error() string {
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

// refreshHTTPClient is shared across all refresh calls to reuse TCP/TLS connections.
var refreshHTTPClient = &http.Client{Timeout: 15 * time.Second}

func maybeRefreshToken(ctx context.Context, store *TokenStore, tokenID string) (bool, error) {
	return maybeRefreshTokenWithMode(ctx, store, tokenID, false)
}

func maybeRefreshTokenIfStale(ctx context.Context, store *TokenStore, tokenID string) (bool, error) {
	return maybeRefreshTokenWithMode(ctx, store, tokenID, true)
}

func maybeRefreshTokenWithMode(ctx context.Context, store *TokenStore, tokenID string, refreshIfStale bool) (bool, error) {
	token, ok := store.TokenSnapshot(tokenID)
	if !ok {
		return false, errors.New("token not found")
	}
	if token.RefreshToken == "" {
		return false, nil
	}
	if refreshIfStale && !tokenNeedsRefresh(token.LastRefresh, time.Now()) {
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
	if refreshIfStale && !tokenNeedsRefresh(token.LastRefresh, time.Now()) {
		return false, nil
	}
	// For forced refreshes (on 401): if another goroutine already refreshed
	// within the debounce window, treat it as done to avoid hammering the auth
	// endpoint with a thundering herd of concurrent 401 retries.
	if !refreshIfStale && !token.LastRefresh.IsZero() && time.Since(token.LastRefresh) < refreshDebounce {
		return true, nil
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

func tokenNeedsRefresh(lastRefresh time.Time, now time.Time) bool {
	if lastRefresh.IsZero() {
		return false
	}
	return lastRefresh.Before(now.Add(-refreshInterval))
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", refreshTokenError{
			permanent: false,
			err:       fmt.Errorf("read refresh response: %w", err),
		}
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return "", "", refreshTokenError{
			permanent: true,
			err:       fmt.Errorf("refresh token unauthorized"),
		}
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", refreshTokenError{
			permanent: false,
			err:       fmt.Errorf("refresh request status %d: %s", resp.StatusCode, string(body)),
		}
	}

	var payload refreshResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", "", refreshTokenError{
			permanent: false,
			err:       fmt.Errorf("decode refresh response: %w", err),
		}
	}
	if payload.AccessToken == "" {
		return "", "", refreshTokenError{
			permanent: false,
			err:       errors.New("refresh response missing access_token"),
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

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("decode auth file: %w", err)
	}

	tokens, ok := payload["tokens"].(map[string]any)
	if !ok {
		tokens = make(map[string]any)
		payload["tokens"] = tokens
	}
	tokens["access_token"] = accessToken
	if refreshToken != "" {
		tokens["refresh_token"] = refreshToken
	}
	payload["last_refresh"] = time.Now().UTC().Format(time.RFC3339Nano)

	updated, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encode auth file: %w", err)
	}
	updated = append(updated, '\n')

	// Write to a sibling temp file then rename atomically to avoid a corrupt
	// auth file if the process is killed mid-write.
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
