package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultUsageSyncInterval    = 5 * time.Minute
	defaultUsageSyncConcurrency = 8
)

type usageSyncOptions struct {
	Interval    time.Duration
	Concurrency int
}

func normalizeUsageSyncOptions(opts usageSyncOptions) usageSyncOptions {
	if opts.Interval <= 0 {
		opts.Interval = defaultUsageSyncInterval
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = defaultUsageSyncConcurrency
	}
	return opts
}

func runUsageSyncer(ctx context.Context, store *TokenStore, dataDir string, usageURL string, syncOpts usageSyncOptions, topUpOpts topUpOptions) {
	syncOpts = normalizeUsageSyncOptions(syncOpts)
	client := &http.Client{Timeout: 15 * time.Second}
	syncUsageOnce(ctx, store, client, dataDir, usageURL, syncOpts, topUpOpts)
	ticker := time.NewTicker(syncOpts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		syncUsageOnce(ctx, store, client, dataDir, usageURL, syncOpts, topUpOpts)
	}
}

func syncUsageOnce(ctx context.Context, store *TokenStore, client *http.Client, dataDir string, usageURL string, syncOpts usageSyncOptions, topUpOpts topUpOptions) {
	refs := store.TokenRefs()
	if len(refs) == 0 {
		return
	}
	sem := make(chan struct{}, syncOpts.Concurrency)
	var removedCount atomic.Int32
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
			if syncOneToken(ctx, store, client, usageURL, ref) {
				removedCount.Add(1)
			}
		}(ref)
	}
	wg.Wait()

	n := int(removedCount.Load())
	if n == 0 {
		return
	}

	slog.Warn("usage sync removed unauthorized tokens", "removed", n)
	if err := topUpMissingAccounts(ctx, store, dataDir, n, topUpOpts); err != nil && ctx.Err() == nil {
		slog.Warn("usage sync account top-up", "removed", n, "err", err)
	}
}

func syncOneToken(ctx context.Context, store *TokenStore, client *http.Client, usageURL string, ref TokenRef) bool {
	refreshed, refreshErr := maybeRefreshTokenIfStale(ctx, store, ref.ID, defaultProxyRefreshConfig())
	if refreshErr != nil {
		slog.Warn("token refresh failed during usage sync", "token", ref.ID, "err", refreshErr)
	}
	if refreshed {
		if token, ok := store.TokenSnapshot(ref.ID); ok {
			ref.Token = token.Token
			ref.AccountID = token.AccountID
		}
	}

	snapshot, err := fetchUsage(ctx, client, usageURL, ref)
	if err != nil {
		if errors.Is(err, errUnauthorized) {
			return removeTokenAfterUsageUnauthorized(store, ref.ID)
		} else {
			slog.Warn("usage sync failed", "token", ref.ID, "err", err)
			return false
		}
	}
	if !snapshot.FiveHour.Known && !snapshot.Weekly.Known {
		slog.Warn("usage sync missing quota windows", "token", ref.ID)
	}
	store.UpdateUsage(ref.ID, snapshot.FiveHour, snapshot.Weekly, time.Now())
	return false
}

func removeTokenAfterUsageUnauthorized(store *TokenStore, tokenID string) bool {
	token, ok := store.RemoveToken(tokenID)
	if !ok {
		slog.Warn("token missing on usage unauthorized removal", "token", tokenID)
		return false
	}

	if token.Path == "" {
		slog.Warn("token removed after usage unauthorized", "token", tokenID, "reason", "missing token path")
		return true
	}

	if err := os.Remove(token.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("remove token file after usage unauthorized", "token", tokenID, "path", token.Path, "err", err)
		return true
	}

	slog.Warn("token removed after usage unauthorized", "token", tokenID, "path", token.Path)
	return true
}
