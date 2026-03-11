package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/damonto/codex-load-balancer/plus"
)

var errActiveCredentialExists = errors.New("active credential already exists")

func pendingPurchaseDir(dataDir string) string {
	return filepath.Join(dataDir, plus.PendingDirName)
}

func ensurePendingPurchaseDir(dataDir string) error {
	if err := os.MkdirAll(pendingPurchaseDir(dataDir), 0o755); err != nil {
		return fmt.Errorf("create pending purchase dir: %w", err)
	}
	return nil
}

func countPendingPurchases(dataDir string) (int, error) {
	entries, err := os.ReadDir(pendingPurchaseDir(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read pending purchase dir: %w", err)
	}

	counted := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".json") {
			path := filepath.Join(pendingPurchaseDir(dataDir), entry.Name())
			activePath := filepath.Join(dataDir, entry.Name())
			if _, err := os.Stat(activePath); err == nil {
				continue
			} else if !errors.Is(err, os.ErrNotExist) {
				return 0, fmt.Errorf("stat active credential: %w", err)
			}
			_, accountID, _, _, err := parseAuthFile(path)
			if err != nil {
				continue
			}
			countedKey := accountKey(accountID, entry.Name())
			if countedKey == "" {
				continue
			}
			counted[countedKey] = struct{}{}
		}
	}
	return len(counted), nil
}

func runPendingPurchaseSyncer(ctx context.Context, activeStore *TokenStore, dataDir string, usageURL string, syncOpts usageSyncOptions) {
	if syncOpts.Interval <= 0 {
		syncOpts.Interval = defaultUsageSyncInterval
	}
	if syncOpts.Concurrency <= 0 {
		syncOpts.Concurrency = defaultUsageSyncConcurrency
	}

	slog.Info("pending purchase syncer started", "interval", syncOpts.Interval.String(), "data_dir", pendingPurchaseDir(dataDir))

	pendingStore := NewTokenStore()
	client := &http.Client{Timeout: 15 * time.Second}
	syncPendingPurchasesOnce(ctx, pendingStore, activeStore, client, dataDir, usageURL, syncOpts)

	ticker := time.NewTicker(syncOpts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		syncPendingPurchasesOnce(ctx, pendingStore, activeStore, client, dataDir, usageURL, syncOpts)
	}
}

func syncPendingPurchasesOnce(
	ctx context.Context,
	pendingStore *TokenStore,
	activeStore *TokenStore,
	client *http.Client,
	dataDir string,
	usageURL string,
	syncOpts usageSyncOptions,
) {
	if err := loadTokensFromDir(pendingStore, pendingPurchaseDir(dataDir)); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("pending purchase token scan", "err", err)
		}
		return
	}

	refs := pendingStore.TokenRefs()
	if len(refs) == 0 {
		return
	}

	sem := make(chan struct{}, syncOpts.Concurrency)
	var wg sync.WaitGroup
	for _, ref := range refs {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(ref TokenRef) {
			defer wg.Done()
			defer func() { <-sem }()
			syncOnePendingPurchase(ctx, pendingStore, activeStore, client, dataDir, usageURL, ref)
		}(ref)
	}
	wg.Wait()
}

func syncOnePendingPurchase(
	ctx context.Context,
	pendingStore *TokenStore,
	activeStore *TokenStore,
	client *http.Client,
	dataDir string,
	usageURL string,
	ref TokenRef,
) {
	refreshed, err := maybeRefreshTokenIfStale(ctx, pendingStore, ref.ID, defaultProxyRefreshConfig())
	if err != nil {
		slog.Warn("pending purchase token refresh",
			"token", ref.ID,
			"err", err,
		)
	}
	if refreshed {
		if token, ok := pendingStore.TokenSnapshot(ref.ID); ok {
			ref.Token = token.Token
			ref.AccountID = token.AccountID
		}
	}

	snapshot, err := fetchUsage(ctx, client, usageURL, ref)
	if err != nil && errors.Is(err, errUnauthorized) {
		forced, refreshErr := maybeRefreshToken(ctx, pendingStore, ref.ID, defaultProxyRefreshConfig())
		if refreshErr != nil {
			slog.Warn("pending purchase forced token refresh",
				"token", ref.ID,
				"err", refreshErr,
			)
			return
		}
		if forced {
			if token, ok := pendingStore.TokenSnapshot(ref.ID); ok {
				ref.Token = token.Token
				ref.AccountID = token.AccountID
			}
		}
		snapshot, err = fetchUsage(ctx, client, usageURL, ref)
	}
	if err != nil {
		slog.Warn("pending purchase usage sync",
			"token", ref.ID,
			"err", err,
		)
		return
	}

	now := time.Now()
	previous, _ := pendingStore.TokenSnapshot(ref.ID)
	pendingStore.UpdateUsage(ref.ID, snapshot.FiveHour, snapshot.Weekly, now)
	pendingStore.UpdateUsageAccountMetadata(ref.ID, usageAccountMetadata{
		UserID:    snapshot.UserID,
		AccountID: snapshot.AccountID,
		Email:     snapshot.Email,
		PlanType:  snapshot.PlanType,
	})
	if !strings.EqualFold(snapshot.PlanType, "plus") {
		if !strings.EqualFold(previous.PlanType, snapshot.PlanType) {
			slog.Info("pending purchase status",
				"token", ref.ID,
				"email", snapshot.Email,
				"plan_type", snapshot.PlanType,
			)
		}
		return
	}

	token, ok := pendingStore.TokenSnapshot(ref.ID)
	if !ok {
		slog.Warn("pending purchase token missing during promotion", "token", ref.ID)
		return
	}

	activePath, err := promotePendingCredential(token.Path, dataDir)
	if err != nil {
		if errors.Is(err, errActiveCredentialExists) {
			removePendingCredential(pendingStore, ref.ID, token.Path, "active_exists")
			return
		}
		slog.Warn("promote pending purchase",
			"token", ref.ID,
			"path", token.Path,
			"err", err,
		)
		return
	}

	modTime := time.Now().UTC()
	if info, err := os.Stat(activePath); err == nil {
		modTime = info.ModTime()
	} else {
		slog.Warn("stat promoted credential", "token", ref.ID, "path", activePath, "err", err)
	}
	promoted := token
	promoted.Path = activePath
	activeStore.UpsertToken(promoted, modTime)
	pendingStore.RemoveToken(ref.ID)

	slog.Info("pending purchase promoted",
		"token", ref.ID,
		"path", activePath,
		"email", snapshot.Email,
		"account_id", snapshot.AccountID,
	)
}

func removePendingCredential(store *TokenStore, tokenID string, path string, reason string) {
	store.RemoveToken(tokenID)
	if path == "" {
		slog.Warn("remove pending credential", "token", tokenID, "reason", reason, "path_reason", "missing_token_path")
		return
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("remove pending credential", "token", tokenID, "path", path, "reason", reason, "err", err)
		return
	}
	slog.Warn("remove pending credential", "token", tokenID, "path", path, "reason", reason)
}

func promotePendingCredential(path string, dataDir string) (string, error) {
	if path == "" {
		return "", errors.New("pending credential path is empty")
	}

	activePath := filepath.Join(dataDir, filepath.Base(path))
	if _, err := os.Stat(activePath); err == nil {
		return "", fmt.Errorf("%w: %s", errActiveCredentialExists, activePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stat active credential: %w", err)
	}

	if err := os.Rename(path, activePath); err != nil {
		return "", fmt.Errorf("move pending credential: %w", err)
	}
	return activePath, nil
}
