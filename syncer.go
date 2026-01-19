package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
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
	synced := 0
	for _, ref := range refs {
		if ctx.Err() != nil {
			return
		}
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
					continue
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
						continue
					}
				} else {
					store.MarkInvalid(ref.ID)
					store.ClearSessionsForToken(ref.ID)
					slog.Warn("token invalidated by 401 (no refresh token)", "token", ref.ID)
					continue
				}
			}
			slog.Warn("usage sync failed", "token", ref.ID, "err", err)
			continue
		}
		store.UpdateUsage(ref.ID, snapshot.FiveHour, snapshot.Weekly, time.Now())
		synced++
	}
	slog.Info("usage sync done", "tokens", synced)
}
