package main

import (
	"context"
	"log/slog"
	"time"
)

func (s *Server) selectProxyToken(
	ctx context.Context,
	sessionID string,
	tried map[string]bool,
	attempt int,
	selectLogMessage string,
	refreshLogMessage string,
) (TokenState, bool, error) {
	token, sticky, err := s.store.SelectToken(sessionID, tried)
	if err != nil {
		return TokenState{}, false, err
	}

	reason := "ranked"
	if sticky {
		reason = "sticky"
	}
	slog.Info(selectLogMessage, "token", token.ID, "reason", reason, "session", sessionID, "attempt", attempt+1)

	refreshed, err := maybeRefreshTokenIfStale(ctx, s.store, token.ID, defaultProxyRefreshConfig())
	if err != nil {
		slog.Warn(refreshLogMessage, "token", token.ID, "err", err)
	}
	if refreshed {
		if updated, ok := s.store.TokenSnapshot(token.ID); ok {
			token = updated
		}
	}
	return token, sticky, nil
}

func (s *Server) refreshTokenAfterUnauthorized(ctx context.Context, token *TokenState, logPrefix string) bool {
	refreshed, err := maybeRefreshToken(ctx, s.store, token.ID, defaultProxyRefreshConfig())
	if err != nil {
		if isPermanentRefreshError(err) {
			slog.Warn(proxyLogMessage(logPrefix, "token refresh permanently failed"), "token", token.ID, "err", err)
			s.store.MarkInvalid(token.ID)
		} else {
			slog.Warn(proxyLogMessage(logPrefix, "token refresh failed"), "token", token.ID, "err", err)
			s.store.MarkCooldown(token.ID, time.Now().Add(defaultCooldownDuration))
		}
		return false
	}
	if !refreshed {
		return false
	}
	if updated, ok := s.store.TokenSnapshot(token.ID); ok {
		*token = updated
	}
	return true
}

func (s *Server) retryWithAlternateToken(tokenID string, tried map[string]bool, attempt int) bool {
	tried[tokenID] = true
	s.store.MarkCooldown(tokenID, time.Now().Add(defaultCooldownDuration))
	s.store.ClearSessionsForToken(tokenID)
	return attempt != 1 && s.store.HasAvailableToken(tried)
}

func (s *Server) retryAfterUsageLimit(tokenID string, tried map[string]bool, attempt int) bool {
	tried[tokenID] = true
	s.markTokenUsageLimit(tokenID)
	return attempt != 1 && s.store.HasAvailableToken(tried)
}

func (s *Server) markTokenUsageLimit(tokenID string) {
	s.store.MarkCooldown(tokenID, time.Now().Add(defaultCooldownDuration))
	slog.Info("token cooldown", "token", tokenID, "reason", "usage limit")
	s.store.ClearSessionsForToken(tokenID)
}

func (s *Server) bindProxySession(sessionID string, prevTokenID string, token TokenState, sticky bool, logPrefix string) {
	if sessionID == "" || sticky {
		return
	}
	s.store.SetSession(sessionID, token.ID)
	if prevTokenID != "" && prevTokenID != token.ID {
		slog.Info(proxyLogMessage(logPrefix, "session switched"), "session", sessionID, "from", prevTokenID, "to", token.ID)
		return
	}
	if prevTokenID == "" {
		slog.Info(proxyLogMessage(logPrefix, "session bound"), "session", sessionID, "token", token.ID)
	}
}

func proxyLogMessage(prefix string, message string) string {
	if prefix == "" {
		return message
	}
	return prefix + " " + message
}
