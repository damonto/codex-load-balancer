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

	forwardPath := normalizeAPIPath(r.URL.Path)
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
	var retryResp *http.Response
	var retryBody []byte
	for attempt := range 2 {
		token, sticky, err := s.selectProxyToken(
			r.Context(),
			sessionID,
			tried,
			attempt,
			"token select",
			"token refresh failed",
		)
		if err != nil {
			if retryResp != nil {
				if err := writeResponse(w, retryResp, retryBody); err != nil {
					slog.Warn("write retry response", "session", sessionID, "err", err)
				}
				return
			}
			http.Error(w, "no available tokens", http.StatusServiceUnavailable)
			return
		}

		bodyForToken := responseBodyForToken(forwardPath, body, token, sessionID)
		resp, respBody, stream, err := s.forwardRequest(r, bodyForToken, token, forwardPath)
		if err != nil {
			slog.Warn("upstream request", "token", token.ID, "session", sessionID, "err", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		if resp.StatusCode == http.StatusUnauthorized {
			if s.refreshTokenAfterUnauthorized(r.Context(), &token, "") {
				bodyForToken = responseBodyForToken(forwardPath, body, token, sessionID)
				resp, respBody, stream, err = s.forwardRequest(r, bodyForToken, token, forwardPath)
				if err != nil {
					slog.Warn("upstream request", "token", token.ID, "session", sessionID, "err", err)
					http.Error(w, "upstream error", http.StatusBadGateway)
					return
				}
			}
		}

		if resp.StatusCode == http.StatusUnauthorized {
			if !s.retryWithAlternateToken(token.ID, tried, attempt) {
				if err := writeResponse(w, resp, respBody); err != nil {
					slog.Warn("write unauthorized response", "token", token.ID, "session", sessionID, "err", err)
				}
				return
			}
			retryResp = resp
			retryBody = respBody
			continue
		}

		s.applyUsageFromHeaders(token.ID, resp.Header)
		if !stream {
			if usage, ok := extractTokenUsageFromBody(respBody); ok {
				s.recordTokenUsage(token, forwardPath, resp.StatusCode, false, usage)
			}
		}
		if !stream && isLimitError(resp.StatusCode, respBody) {
			if !s.retryAfterUsageLimit(token.ID, tried, attempt) {
				if err := writeResponse(w, resp, respBody); err != nil {
					slog.Warn("write limit response", "token", token.ID, "session", sessionID, "err", err)
				}
				return
			}
			retryResp = resp
			retryBody = respBody
			continue
		}

		s.bindProxySession(sessionID, prevTokenID, token, sticky, "")

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

		if err := writeResponse(w, resp, respBody); err != nil {
			slog.Warn("write upstream response", "token", token.ID, "session", sessionID, "err", err)
		}
		return
	}
}

func responseBodyForToken(path string, body []byte, token TokenState, sessionID string) []byte {
	if !shouldInjectResponseTools(path) {
		return body
	}
	updated, changed, err := injectResponseTools(body, responseToolInjectionContextForToken(token))
	if err != nil {
		slog.Warn("inject response tools", "session", sessionID, "token", token.ID, "err", err)
		return body
	}
	if !changed {
		return body
	}
	return updated
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
