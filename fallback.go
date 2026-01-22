package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const fallbackFailureThreshold = 5

type fallbackFileAccount struct {
	BaseURL string               `json:"base_url"`
	APIKey  string               `json:"api_key"`
	Checkin *fallbackFileCheckin `json:"checkin"`
}

type fallbackFileCheckin struct {
	BaseURL string            `json:"base_url"`
	Headers map[string]string `json:"headers"`
}

type fallbackAccount struct {
	baseURL             *url.URL
	apiKey              string
	checkin             *fallbackCheckin
	consecutiveFailures int
}

type fallbackCheckin struct {
	baseURL *url.URL
	headers map[string]string
}

type FallbackManager struct {
	mu       sync.Mutex
	path     string
	modTime  time.Time
	accounts []fallbackAccount
	current  int
	rng      *rand.Rand
}

func NewFallbackManager(dir string) *FallbackManager {
	return &FallbackManager{
		path:    filepath.Join(dir, fallbackFileName),
		current: -1,
		rng:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (m *FallbackManager) Reload() (bool, error) {
	info, err := os.Stat(m.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return m.resetAccounts(nil, time.Time{}), nil
		}
		return false, fmt.Errorf("stat fallback file: %w", err)
	}
	if info.IsDir() {
		return false, fmt.Errorf("fallback path is directory")
	}

	modTime := info.ModTime()
	m.mu.Lock()
	needsReload := modTime.After(m.modTime)
	m.mu.Unlock()
	if !needsReload {
		return false, nil
	}

	data, err := os.ReadFile(m.path)
	if err != nil {
		return false, fmt.Errorf("read fallback file: %w", err)
	}

	accounts, err := parseFallbackAccounts(data)
	if err != nil {
		return false, err
	}

	m.mu.Lock()
	m.accounts = accounts
	m.modTime = modTime
	m.current = -1
	m.mu.Unlock()
	return true, nil
}

func (m *FallbackManager) AccountCount() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	count := len(m.accounts)
	m.mu.Unlock()
	return count
}

func (m *FallbackManager) Select() (fallbackAccount, int, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.accounts) == 0 {
		return fallbackAccount{}, -1, false
	}
	if m.current < 0 || m.current >= len(m.accounts) {
		m.current = m.rng.Intn(len(m.accounts))
	}
	selected := m.accounts[m.current]
	return selected, m.current, true
}

func (m *FallbackManager) Report(index int, success bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if index < 0 || index >= len(m.accounts) {
		return
	}
	if success {
		m.accounts[index].consecutiveFailures = 0
		return
	}
	m.accounts[index].consecutiveFailures++
	if m.accounts[index].consecutiveFailures >= fallbackFailureThreshold {
		m.accounts[index].consecutiveFailures = 0
		if len(m.accounts) > 1 {
			m.current = (index + 1) % len(m.accounts)
		}
	}
}

func (m *FallbackManager) CheckinTargets() []fallbackCheckin {
	m.mu.Lock()
	targets := make([]fallbackCheckin, 0, len(m.accounts))
	for _, account := range m.accounts {
		if account.checkin == nil {
			continue
		}
		targets = append(targets, *account.checkin)
	}
	m.mu.Unlock()
	return targets
}

func (m *FallbackManager) resetAccounts(accounts []fallbackAccount, modTime time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.accounts) == 0 && len(accounts) == 0 && m.modTime.Equal(modTime) {
		return false
	}
	m.accounts = accounts
	m.modTime = modTime
	m.current = -1
	return true
}

func parseFallbackAccounts(data []byte) ([]fallbackAccount, error) {
	var raw []fallbackFileAccount
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode fallback file: %w", err)
	}

	accounts := make([]fallbackAccount, 0, len(raw))
	for i, entry := range raw {
		baseURL := strings.TrimSpace(entry.BaseURL)
		apiKey := strings.TrimSpace(entry.APIKey)
		if baseURL == "" || apiKey == "" {
			slog.Warn("fallback entry missing base_url or api_key", "index", i)
			continue
		}
		parsedBase, err := parseFallbackURL(baseURL)
		if err != nil {
			slog.Warn("fallback base_url invalid", "index", i, "err", err)
			continue
		}

		account := fallbackAccount{
			baseURL: parsedBase,
			apiKey:  apiKey,
		}

		if entry.Checkin != nil {
			checkinURL := strings.TrimSpace(entry.Checkin.BaseURL)
			if checkinURL == "" {
				slog.Warn("fallback checkin base_url missing", "index", i)
			} else if parsedCheckin, err := parseFallbackURL(checkinURL); err != nil {
				slog.Warn("fallback checkin base_url invalid", "index", i, "err", err)
			} else {
				account.checkin = &fallbackCheckin{
					baseURL: parsedCheckin,
					headers: entry.Checkin.Headers,
				}
			}
		}

		accounts = append(accounts, account)
	}
	return accounts, nil
}

func parseFallbackURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("url missing scheme or host")
	}
	return parsed, nil
}

func runFallbackWatcher(ctx context.Context, manager *FallbackManager) {
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			changed, err := manager.Reload()
			if err != nil {
				slog.Warn("fallback reload", "err", err)
				continue
			}
			if changed {
				slog.Info("fallback loaded", "accounts", manager.AccountCount())
			}
		}
	}
}

func runFallbackCheckinScheduler(ctx context.Context, manager *FallbackManager) {
	client := &http.Client{Timeout: 20 * time.Second}
	for {
		next := nextMidnight(time.Now())
		wait := time.Until(next)
		if wait < time.Second {
			wait = time.Second
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		runFallbackCheckins(ctx, manager, client)
	}
}

func runFallbackCheckins(ctx context.Context, manager *FallbackManager, client *http.Client) {
	targets := manager.CheckinTargets()
	if len(targets) == 0 {
		return
	}
	for _, target := range targets {
		if ctx.Err() != nil {
			return
		}
		if err := performFallbackCheckin(ctx, client, target); err != nil {
			slog.Warn("fallback checkin", "url", target.baseURL.String(), "err", err)
		}
	}
}

func performFallbackCheckin(ctx context.Context, client *http.Client, target fallbackCheckin) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.baseURL.String(), nil)
	if err != nil {
		return fmt.Errorf("build checkin request: %w", err)
	}
	for key, value := range target.headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send checkin request: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("checkin status %d", resp.StatusCode)
	}
	return nil
}

func nextMidnight(now time.Time) time.Time {
	year, month, day := now.Date()
	return time.Date(year, month, day+1, 0, 0, 0, 0, now.Location())
}
