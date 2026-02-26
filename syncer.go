package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

func runUsageSyncer(ctx context.Context, store *TokenStore) {
	client := &http.Client{Timeout: 15 * time.Second}
	syncUsageOnce(ctx, store, client)
	ticker := time.NewTicker(syncInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		syncUsageOnce(ctx, store, client)
	}
}

func syncUsageOnce(ctx context.Context, store *TokenStore, client *http.Client) {
	refs := store.TokenRefs()
	if len(refs) == 0 {
		return
	}
	sem := make(chan struct{}, syncConcurrency)
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
			syncOneToken(ctx, store, client, ref)
		}(ref)
	}
	wg.Wait()
}

func syncOneToken(ctx context.Context, store *TokenStore, client *http.Client, ref TokenRef) {
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
			refreshed, refreshErr := maybeRefreshToken(ctx, store, ref.ID)
			if refreshErr != nil {
				if isPermanentRefreshError(refreshErr) {
					store.MarkInvalid(ref.ID)
					store.ClearSessionsForToken(ref.ID)
					slog.Warn("token invalidated after refresh failure", "token", ref.ID, "err", refreshErr)
				} else {
					slog.Warn("token refresh failed during usage sync", "token", ref.ID, "err", refreshErr)
				}
				return
			}
			if refreshed {
				refreshedRef := ref
				if token, ok := store.TokenSnapshot(ref.ID); ok {
					refreshedRef.Token = token.Token
					refreshedRef.AccountID = token.AccountID
				}
				snapshot, err = fetchUsage(ctx, client, refreshedRef)
				if err != nil {
					slog.Warn("usage sync after refresh", "token", ref.ID, "err", err)
					return
				}
			} else {
				store.MarkInvalid(ref.ID)
				store.ClearSessionsForToken(ref.ID)
				slog.Warn("token invalidated by 401 (no refresh token)", "token", ref.ID)
				return
			}
		} else {
			slog.Warn("usage sync failed", "token", ref.ID, "err", err)
			return
		}
	}
	if !snapshot.FiveHour.Known && !snapshot.Weekly.Known {
		slog.Warn("usage sync missing quota windows", "token", ref.ID)
	}
	store.UpdateUsage(ref.ID, snapshot.FiveHour, snapshot.Weekly, time.Now())
}
