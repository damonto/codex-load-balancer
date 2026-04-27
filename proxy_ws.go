package main

import (
	"context"
	"log/slog"
	"net/http"
)

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request, path string, sessionID string) {
	s.websocketWG.Add(1)
	defer s.websocketWG.Done()

	prevTokenID := ""
	if sessionID != "" {
		if tokenID, ok := s.store.SessionToken(sessionID); ok {
			prevTokenID = tokenID
		}
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
			"websocket token select",
			"websocket token refresh failed",
		)
		if err != nil {
			if retryResp != nil {
				if err := writeResponse(w, retryResp, retryBody); err != nil {
					slog.Warn("write websocket retry response", "session", sessionID, "err", err)
				}
				return
			}
			http.Error(w, "no available tokens", http.StatusServiceUnavailable)
			return
		}

		upstream, err := s.forwardWebSocketRequest(r, token, path)
		if err != nil {
			slog.Warn("websocket upstream request", "token", token.ID, "session", sessionID, "err", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}

		if upstream.resp.StatusCode == http.StatusUnauthorized {
			if s.refreshTokenAfterUnauthorized(r.Context(), &token, "websocket") {
				upstream, err = s.forwardWebSocketRequest(r, token, path)
				if err != nil {
					slog.Warn("websocket upstream request", "token", token.ID, "session", sessionID, "err", err)
					http.Error(w, "upstream error", http.StatusBadGateway)
					return
				}
			}
		}

		if upstream.resp.StatusCode == http.StatusUnauthorized {
			if !s.retryWithAlternateToken(token.ID, tried, attempt) {
				if err := writeResponse(w, upstream.resp, upstream.body); err != nil {
					slog.Warn("write websocket unauthorized response", "token", token.ID, "session", sessionID, "err", err)
				}
				return
			}
			retryResp = upstream.resp
			retryBody = upstream.body
			continue
		}

		s.applyUsageFromHeaders(token.ID, upstream.resp.Header)
		if upstream.resp.StatusCode != http.StatusSwitchingProtocols {
			if usage, ok := extractTokenUsageFromBody(upstream.body); ok {
				s.recordTokenUsage(token, path, upstream.resp.StatusCode, false, usage)
			}
		}

		if isLimitError(upstream.resp.StatusCode, upstream.body) {
			if !s.retryAfterUsageLimit(token.ID, tried, attempt) {
				if err := writeResponse(w, upstream.resp, upstream.body); err != nil {
					slog.Warn("write websocket limit response", "token", token.ID, "session", sessionID, "err", err)
				}
				return
			}
			retryResp = upstream.resp
			retryBody = upstream.body
			continue
		}

		if upstream.resp.StatusCode != http.StatusSwitchingProtocols {
			if err := writeResponse(w, upstream.resp, upstream.body); err != nil {
				slog.Warn("write websocket upstream response", "token", token.ID, "session", sessionID, "err", err)
			}
			return
		}

		s.bindProxySession(sessionID, prevTokenID, token, sticky, "websocket")

		usageCapture := newWebsocketUsageCapture(func(usage TokenUsage) {
			s.recordTokenUsage(token, path, upstream.resp.StatusCode, true, usage)
		}, func() {
			s.markTokenUsageLimit(token.ID)
		})
		tunnelCtx, cancelTunnel := context.WithCancel(context.Background())
		stopRequestClose := context.AfterFunc(r.Context(), cancelTunnel)
		stopShutdownClose := afterSignal(s.shutdownSignal(), cancelTunnel)
		defer stopShutdownClose()
		defer stopRequestClose()
		defer cancelTunnel()

		clientToUpstream, upstreamToClient, tunnelErr := tunnelWebSocket(
			tunnelCtx,
			w,
			upstream,
			usageCapture,
			shouldInjectResponseTools(path),
			responseToolInjectionContextForToken(token),
		)
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
		return
	}
}

func afterSignal(done <-chan struct{}, f func()) func() {
	if done == nil {
		return func() {}
	}
	stop := make(chan struct{})
	go func() {
		select {
		case <-done:
			f()
		case <-stop:
		}
	}()
	return func() { close(stop) }
}
