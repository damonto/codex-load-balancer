package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

func runUsageSyncer(ctx context.Context, store *TokenStore, dataDir string, topUpOpts topUpOptions) {
	client := &http.Client{Timeout: 15 * time.Second}
	syncUsageOnce(ctx, store, client, dataDir, topUpOpts)
	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		syncUsageOnce(ctx, store, client, dataDir, topUpOpts)
	}
}

func syncUsageOnce(ctx context.Context, store *TokenStore, client *http.Client, dataDir string, topUpOpts topUpOptions) {
	refs := store.TokenRefs()
	if len(refs) == 0 {
		return
	}
	sem := make(chan struct{}, syncConcurrency)
	removed := make(chan struct{}, len(refs))
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
			if syncOneToken(ctx, store, client, ref) {
				removed <- struct{}{}
			}
		}(ref)
	}
	wg.Wait()
	close(removed)

	removedCount := 0
	for range removed {
		removedCount++
	}
	if removedCount == 0 {
		return
	}

	slog.Warn("usage sync removed unauthorized tokens", "removed", removedCount)
	if err := topUpMissingAccounts(ctx, store, dataDir, removedCount, topUpOpts); err != nil && ctx.Err() == nil {
		slog.Warn("usage sync account top-up", "removed", removedCount, "err", err)
	}
}

func syncOneToken(ctx context.Context, store *TokenStore, client *http.Client, ref TokenRef) bool {
	refreshed, refreshErr := maybeRefreshTokenIfStale(ctx, store, ref.ID)
	if refreshErr != nil {
		slog.Warn("token refresh failed during usage sync", "token", ref.ID, "err", refreshErr)
	}
	if refreshed {
		if token, ok := store.TokenSnapshot(ref.ID); ok {
			ref.Token = token.Token
			ref.AccountID = token.AccountID
		}
	}

	snapshot, err := fetchUsage(ctx, client, ref)
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
