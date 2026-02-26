package main

import (
	"log/slog"
	"net/http"
	"time"
)

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request, path string, sessionID string, prevTokenID string) {
	tried := make(map[string]bool)
	for attempt := range 2 {
		token, sticky, err := s.store.SelectToken(sessionID, tried)
		if err != nil {
			http.Error(w, "no available tokens", http.StatusServiceUnavailable)
			return
		}
		reason := "ranked"
		if sticky {
			reason = "sticky"
		}
		slog.Info("websocket token select", "token", token.ID, "reason", reason, "session", sessionID, "attempt", attempt+1)

		refreshed, err := maybeRefreshTokenIfStale(r.Context(), s.store, token.ID)
		if err != nil {
			slog.Warn("websocket token refresh failed", "token", token.ID, "err", err)
		}
		if refreshed {
			if updated, ok := s.store.TokenSnapshot(token.ID); ok {
				token = updated
			}
		}

		upstream, err := s.forwardWebSocketRequest(r, token, path)
		if err != nil {
			slog.Warn("websocket upstream request", "token", token.ID, "session", sessionID, "err", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}

		if upstream.resp.StatusCode == http.StatusUnauthorized {
			refreshed, err := maybeRefreshToken(r.Context(), s.store, token.ID)
			if err != nil {
				if isPermanentRefreshError(err) {
					slog.Warn("websocket token refresh permanently failed", "token", token.ID, "err", err)
					s.store.MarkInvalid(token.ID)
				} else {
					slog.Warn("websocket token refresh failed", "token", token.ID, "err", err)
					s.store.MarkCooldown(token.ID, time.Now().Add(cooldownDuration))
				}
			}
			if refreshed {
				if updated, ok := s.store.TokenSnapshot(token.ID); ok {
					token = updated
				}
				upstream, err = s.forwardWebSocketRequest(r, token, path)
				if err != nil {
					slog.Warn("websocket upstream request", "token", token.ID, "session", sessionID, "err", err)
					http.Error(w, "upstream error", http.StatusBadGateway)
					return
				}
			}
		}

		s.applyUsageFromHeaders(token.ID, upstream.resp.Header)
		if upstream.resp.StatusCode != http.StatusSwitchingProtocols {
			if usage, ok := extractTokenUsageFromBody(upstream.body); ok {
				s.recordTokenUsage(token, path, upstream.resp.StatusCode, false, usage)
			}
		}

		if upstream.resp.StatusCode == http.StatusUnauthorized {
			tried[token.ID] = true
			if sessionID != "" {
				s.store.ClearSession(sessionID)
			}
			if attempt == 1 {
				writeResponse(w, upstream.resp, upstream.body)
				return
			}
			continue
		}

		if isLimitError(upstream.resp.StatusCode, upstream.body) {
			tried[token.ID] = true
			s.store.MarkCooldown(token.ID, time.Now().Add(cooldownDuration))
			slog.Info("websocket token cooldown after usage limit", "token", token.ID)
			if sessionID != "" {
				s.store.ClearSession(sessionID)
			}
			if attempt == 1 {
				writeResponse(w, upstream.resp, upstream.body)
				return
			}
			continue
		}

		if upstream.resp.StatusCode != http.StatusSwitchingProtocols {
			writeResponse(w, upstream.resp, upstream.body)
			return
		}

		if sessionID != "" && !sticky {
			s.store.SetSession(sessionID, token.ID)
			if prevTokenID != "" && prevTokenID != token.ID {
				slog.Info("websocket session switched", "session", sessionID, "from", prevTokenID, "to", token.ID)
			} else if prevTokenID == "" {
				slog.Info("websocket session bound", "session", sessionID, "token", token.ID)
			}
		}

		slog.Info(
			"websocket tunnel start",
			"token", token.ID,
			"session", sessionID,
			"status", upstream.resp.StatusCode,
			"content_type", upstream.resp.Header.Get("Content-Type"),
			"attempt", attempt+1,
		)
		clientToUpstream, upstreamToClient, tunnelErr := tunnelWebSocket(w, r, upstream)
		ctxErr := r.Context().Err()
		if tunnelErr != nil {
			slog.Warn(
				"websocket tunnel end",
				"token", token.ID,
				"session", sessionID,
				"client_to_upstream", clientToUpstream,
				"upstream_to_client", upstreamToClient,
				"err", tunnelErr,
				"ctx_err", ctxErr,
			)
			return
		}
		slog.Info(
			"websocket tunnel end",
			"token", token.ID,
			"session", sessionID,
			"client_to_upstream", clientToUpstream,
			"upstream_to_client", upstreamToClient,
			"ctx_err", ctxErr,
		)
		return
	}
}
