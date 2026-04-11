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

type usageSyncOptions struct {
	Interval    time.Duration
	Concurrency int
}

func runUsageSyncer(ctx context.Context, store *TokenStore, usageURL string, syncOpts usageSyncOptions) {
	if syncOpts.Interval <= 0 {
		slog.Error("usage sync disabled", "reason", "interval must be positive")
		return
	}
	if syncOpts.Concurrency <= 0 {
		slog.Error("usage sync disabled", "reason", "concurrency must be positive")
		return
	}
	client := &http.Client{Timeout: 15 * time.Second}
	syncUsageOnce(ctx, store, client, usageURL, syncOpts)
	ticker := time.NewTicker(syncOpts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		syncUsageOnce(ctx, store, client, usageURL, syncOpts)
	}
}

func syncUsageOnce(ctx context.Context, store *TokenStore, client *http.Client, usageURL string, syncOpts usageSyncOptions) {
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

	slog.Warn("usage sync removed tokens", "removed", n)
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
	if err != nil && errors.Is(err, errUnauthorized) {
		forced, refreshErr := maybeRefreshToken(ctx, store, ref.ID, defaultProxyRefreshConfig())
		if refreshErr != nil {
			slog.Warn("token refresh failed after usage unauthorized", "token", ref.ID, "err", refreshErr)
		} else if forced {
			if token, ok := store.TokenSnapshot(ref.ID); ok {
				ref.Token = token.Token
				ref.AccountID = token.AccountID
			}
			snapshot, err = fetchUsage(ctx, client, usageURL, ref)
		}
	}
	if err != nil {
		if errors.Is(err, errUnauthorized) {
			return removeTokenAndFile(store, ref.ID, "unauthorized")
		}
		slog.Warn("usage sync failed", "token", ref.ID, "err", err)
		return false
	}
	if !snapshot.FiveHour.Known && !snapshot.Weekly.Known {
		slog.Warn("usage sync missing quota windows", "token", ref.ID)
	}
	store.UpdateUsage(ref.ID, snapshot.FiveHour, snapshot.Weekly, time.Now())
	store.UpdateUsageAccountMetadata(ref.ID, usageAccountMetadata{
		AccountID: snapshot.AccountID,
		Email:     snapshot.Email,
		PlanType:  snapshot.PlanType,
	})
	return false
}

func removeTokenAndFile(store *TokenStore, tokenID string, reason string) bool {
	token, ok := store.RemoveToken(tokenID)
	if !ok {
		slog.Warn("token missing on removal", "token", tokenID, "reason", reason)
		return false
	}

	if token.Path == "" {
		slog.Warn("token removed", "token", tokenID, "reason", reason, "path_reason", "missing_token_path")
		return true
	}

	if err := os.Remove(token.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("remove token file", "token", tokenID, "path", token.Path, "reason", reason, "err", err)
		return true
	}

	slog.Warn("token removed", "token", tokenID, "path", token.Path, "reason", reason)
	return true
}
