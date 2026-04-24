package main

import (
	"bytes"
	"net/http"
	"strings"
)

func allowedPath(path string) bool {
	return hasAPIPathPrefix(path, "/responses") ||
		hasAPIPathPrefix(path, "/v1/responses") ||
		hasAPIPathPrefix(path, "/models") ||
		hasAPIPathPrefix(path, "/v1/models")
}

func normalizeAPIPath(path string) string {
	if strings.HasPrefix(path, "/v1/responses") || strings.HasPrefix(path, "/v1/models") {
		return strings.TrimPrefix(path, "/v1")
	}
	return path
}

func extractSessionID(headers http.Header) string {
	return headers.Get("session_id")
}

func isLimitError(status int, body []byte) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	return bytes.Contains(body, []byte("You've hit your usage limit"))
}

func isWebSocketRequest(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	if !headerHasToken(r.Header.Get("Connection"), "upgrade") {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket")
}

func headerHasToken(value string, token string) bool {
	for part := range strings.SplitSeq(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

func joinURLPath(basePath, reqPath string) string {
	base := strings.TrimSuffix(basePath, "/")
	if !strings.HasPrefix(reqPath, "/") {
		reqPath = "/" + reqPath
	}
	return base + reqPath
}

func hasAPIPathPrefix(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}
