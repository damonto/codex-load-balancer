package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Server struct {
	store        *TokenStore
	client       *http.Client
	upstreamBase *url.URL
	apiKey       string
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	slog.Info(
		"proxy request",
		"method", r.Method,
		"path", r.URL.Path,
		"host", r.Host,
		"remote", r.RemoteAddr,
		"content_length", r.ContentLength,
	)
	if !allowedPath(r.URL.Path) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read request body", http.StatusBadRequest)
		return
	}

	sessionID := extractSessionID(r.Header)
	sessionLabel := sessionID
	if sessionLabel == "" {
		sessionLabel = "-"
	}
	prevTokenID := ""
	if sessionID != "" {
		if tokenID, ok := s.store.SessionToken(sessionID); ok {
			prevTokenID = tokenID
		}
	}

	forwardPath := normalizeResponsesPath(r.URL.Path)
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
		slog.Info("token select", "token", token.ID, "reason", reason, "session", sessionLabel, "attempt", attempt+1)

		refreshed, err := maybeRefreshTokenIfStale(r.Context(), s.store, token.ID)
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
			slog.Warn("upstream request", "token", token.ID, "session", sessionLabel, "err", err)
			http.Error(w, "upstream error", http.StatusBadGateway)
			return
		}
		slog.Info(
			"upstream response",
			"token", token.ID,
			"session", sessionLabel,
			"status", resp.StatusCode,
			"content_type", resp.Header.Get("Content-Type"),
			"stream", stream,
			"attempt", attempt+1,
		)

		if resp.StatusCode == http.StatusUnauthorized {
			refreshed, err := maybeRefreshToken(r.Context(), s.store, token.ID)
			if err != nil {
				if isPermanentRefreshError(err) {
					slog.Warn("token refresh permanently failed", "token", token.ID, "err", err)
					s.store.MarkInvalid(token.ID)
				} else {
					slog.Warn("token refresh failed", "token", token.ID, "err", err)
					s.store.MarkCooldown(token.ID, time.Now().Add(cooldownDuration))
				}
			}
			if refreshed {
				if updated, ok := s.store.TokenSnapshot(token.ID); ok {
					token = updated
				}
				resp, respBody, stream, err = s.forwardRequest(r, body, token, forwardPath)
				if err != nil {
					slog.Warn("upstream request", "token", token.ID, "session", sessionLabel, "err", err)
					http.Error(w, "upstream error", http.StatusBadGateway)
					return
				}
				slog.Info(
					"upstream response",
					"token", token.ID,
					"session", sessionLabel,
					"status", resp.StatusCode,
					"content_type", resp.Header.Get("Content-Type"),
					"stream", stream,
					"attempt", attempt+1,
				)
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
		if !stream && isLimitError(resp.StatusCode, respBody) {
			tried[token.ID] = true
			s.store.MarkCooldown(token.ID, time.Now().Add(cooldownDuration))
			slog.Info("token cooldown after usage limit", "token", token.ID)
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
			slog.Info(
				"stream start",
				"token", token.ID,
				"session", sessionLabel,
				"status", resp.StatusCode,
				"content_type", resp.Header.Get("Content-Type"),
			)
			written, err := streamResponse(w, resp)
			ctxErr := r.Context().Err()
			if err != nil {
				slog.Warn("stream end", "token", token.ID, "session", sessionLabel, "bytes", written, "err", err, "ctx_err", ctxErr)
			} else {
				slog.Info("stream end", "token", token.ID, "session", sessionLabel, "bytes", written, "ctx_err", ctxErr)
			}
			return
		}

		writeResponse(w, resp, respBody)
		return
	}
}

func (s *Server) forwardRequest(r *http.Request, body []byte, token TokenState, path string) (*http.Response, []byte, bool, error) {
	target := *s.upstreamBase
	target.Path = joinURLPath(s.upstreamBase.Path, path)
	target.RawQuery = r.URL.RawQuery

	req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, nil, false, fmt.Errorf("build upstream request: %w", err)
	}

	req.Header = cloneHeaders(r.Header)
	req.Header.Set("Authorization", authHeaderValue(token.Token))
	if token.AccountID != "" && req.Header.Get("ChatGPT-Account-ID") == "" {
		req.Header.Set("ChatGPT-Account-ID", token.AccountID)
	}
	req.ContentLength = r.ContentLength
	req.TransferEncoding = append([]string(nil), r.TransferEncoding...)
	req.Host = s.upstreamBase.Host

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, nil, false, fmt.Errorf("send upstream request: %w", err)
	}

	if isEventStream(resp) {
		return resp, nil, true, nil
	}

	respBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, nil, false, fmt.Errorf("read upstream response: %w", err)
	}
	return resp, respBody, false, nil
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	expected := authHeaderValue(s.apiKey)
	if auth == "" || (auth != s.apiKey && auth != expected) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	stats := s.store.Stats()
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	if err := enc.Encode(stats); err != nil {
		http.Error(w, "encode response", http.StatusInternalServerError)
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

func allowedPath(path string) bool {
	return strings.HasPrefix(path, "/responses") || strings.HasPrefix(path, "/v1/responses")
}

func normalizeResponsesPath(path string) string {
	if strings.HasPrefix(path, "/v1/responses") {
		return strings.TrimPrefix(path, "/v1")
	}
	return path
}

func extractSessionID(headers http.Header) string {
	for _, key := range sessionHeaders {
		if value := headers.Get(key); value != "" {
			return value
		}
	}
	return ""
}

func isLimitError(status int, body []byte) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	return bytes.Contains(body, []byte("You've hit your usage limit"))
}

func cloneHeaders(in http.Header) http.Header {
	out := make(http.Header, len(in))
	for key, values := range in {
		copied := make([]string, len(values))
		copy(copied, values)
		out[key] = copied
	}
	return out
}

func writeResponse(w http.ResponseWriter, resp *http.Response, body []byte) {
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

func streamResponse(w http.ResponseWriter, resp *http.Response) (int64, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return 0, fmt.Errorf("stream response: response writer does not support flushing")
	}

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	flusher.Flush()

	defer resp.Body.Close()
	written, err := io.Copy(flushWriter{w: w, f: flusher}, resp.Body)
	if err != nil {
		return written, fmt.Errorf("stream response body: %w", err)
	}
	return written, nil
}

type flushWriter struct {
	w http.ResponseWriter
	f http.Flusher
}

func (fw flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if err != nil {
		return n, err
	}
	fw.f.Flush()
	return n, nil
}

func isEventStream(resp *http.Response) bool {
	if resp.StatusCode != http.StatusOK {
		return false
	}
	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	return strings.HasPrefix(contentType, "text/event-stream")
}

func copyHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		copied := make([]string, len(values))
		copy(copied, values)
		dst[key] = copied
	}
}

func joinURLPath(basePath, reqPath string) string {
	base := strings.TrimSuffix(basePath, "/")
	if !strings.HasPrefix(reqPath, "/") {
		reqPath = "/" + reqPath
	}
	return base + reqPath
}
