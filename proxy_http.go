package main

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"
)

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	if !allowedPath(r.URL.Path) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !proxyAuthorized(r, s.apiKey) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="codex-load-balancer"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	sessionID := extractSessionID(r.Header)

	forwardPath := normalizeResponsesPath(r.URL.Path)
	if isWebSocketRequest(r) {
		if sessionID == "" {
			slog.Warn("websocket request missing session_id; reconnect may lose token stickiness")
		}
		s.handleWebSocket(w, r, forwardPath, sessionID)
		return
	}

	prevTokenID := ""
	if sessionID != "" {
		if tokenID, ok := s.store.SessionToken(sessionID); ok {
			prevTokenID = tokenID
		}
	}

	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, defaultMaxRequestBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "read request body", http.StatusBadRequest)
		}
		return
	}

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
		slog.Info("token select", "token", token.ID, "reason", reason, "session", sessionID, "attempt", attempt+1)

		refreshed, err := maybeRefreshTokenIfStale(r.Context(), s.store, token.ID, defaultProxyRefreshConfig())
		if err != nil {
			slog.Warn("token refresh failed", "token", token.ID, "err", err)
		}
		if refreshed {
			if updated, ok := s.store.TokenSnapshot(token.ID); ok {
				token = updated
			}
		}

		resp, respBody, stream, err := s.forwardRequest(r, body, token, forwardPath)
		if err != nil {
			slog.Warn("upstream request", "token", token.ID, "session", sessionID, "err", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		if resp.StatusCode == http.StatusUnauthorized {
			refreshed, err := maybeRefreshToken(r.Context(), s.store, token.ID, defaultProxyRefreshConfig())
			if err != nil {
				if isPermanentRefreshError(err) {
					slog.Warn("token refresh permanently failed", "token", token.ID, "err", err)
					s.store.MarkInvalid(token.ID)
				} else {
					slog.Warn("token refresh failed", "token", token.ID, "err", err)
					s.store.MarkCooldown(token.ID, time.Now().Add(defaultCooldownDuration))
				}
			}
			if refreshed {
				if updated, ok := s.store.TokenSnapshot(token.ID); ok {
					token = updated
				}
				resp, respBody, stream, err = s.forwardRequest(r, body, token, forwardPath)
				if err != nil {
					slog.Warn("upstream request", "token", token.ID, "session", sessionID, "err", err)
					http.Error(w, "upstream error", http.StatusBadGateway)
					return
				}
			}
		}

		if resp.StatusCode == http.StatusUnauthorized {
			tried[token.ID] = true
			if sessionID != "" {
				s.store.ClearSession(sessionID)
			}
			if attempt == 1 {
				writeResponse(w, resp, respBody)
				return
			}
			continue
		}

		s.applyUsageFromHeaders(token.ID, resp.Header)
		if !stream {
			if usage, ok := extractTokenUsageFromBody(respBody); ok {
				s.recordTokenUsage(token, forwardPath, resp.StatusCode, false, usage)
			}
		}
		if !stream && isLimitError(resp.StatusCode, respBody) {
			tried[token.ID] = true
			s.store.MarkCooldown(token.ID, time.Now().Add(defaultCooldownDuration))
			slog.Info("token cooldown", "token", token.ID, "reason", "usage limit")
			if sessionID != "" {
				s.store.ClearSession(sessionID)
			}
			if attempt == 1 {
				writeResponse(w, resp, respBody)
				return
			}
			continue
		}

		if sessionID != "" && !sticky {
			s.store.SetSession(sessionID, token.ID)
			if prevTokenID != "" && prevTokenID != token.ID {
				slog.Info("session switched", "session", sessionID, "from", prevTokenID, "to", token.ID)
			} else if prevTokenID == "" {
				slog.Info("session bound", "session", sessionID, "token", token.ID)
			}
		}

		if stream {
			usageCapture := newSSEUsageCapture()
			written, err := streamResponseWithObserver(w, resp, usageCapture)
			if usage, ok := usageCapture.Usage(); ok {
				s.recordTokenUsage(token, forwardPath, resp.StatusCode, true, usage)
			}
			ctxErr := r.Context().Err()
			if err != nil {
				slog.Warn("stream end", "token", token.ID, "session", sessionID, "bytes", written, "err", err, "ctx_err", ctxErr)
			}
			return
		}

		writeResponse(w, resp, respBody)
		return
	}
}

func (s *Server) applyUsageFromHeaders(tokenID string, headers http.Header) {
	fiveHour, weekly, hasFiveHour, hasWeekly := usageFromHeaders(headers)
	if !hasFiveHour && !hasWeekly {
		return
	}

	current, ok := s.store.TokenSnapshot(tokenID)
	if !ok {
		return
	}

	if hasFiveHour {
		fiveHour = mergeUsage(current.FiveHour, fiveHour)
	} else {
		fiveHour = current.FiveHour
	}
	if hasWeekly {
		weekly = mergeUsage(current.Weekly, weekly)
	} else {
		weekly = current.Weekly
	}
	s.store.UpdateUsage(tokenID, fiveHour, weekly, time.Now())
}

func mergeUsage(current WindowUsage, update WindowUsage) WindowUsage {
	if !update.Known {
		return current
	}
	if update.ResetAfterSeconds == 0 {
		update.ResetAfterSeconds = current.ResetAfterSeconds
	}
	if update.ResetAt.IsZero() {
		update.ResetAt = current.ResetAt
	}
	return update
}
