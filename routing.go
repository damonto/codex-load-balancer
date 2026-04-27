package main

import (
	"encoding/json"
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
	return isLimitErrorBody(body)
}

func isLimitErrorBody(body []byte) bool {
	var envelope limitErrorEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false
	}
	if envelope.Status == http.StatusTooManyRequests {
		return true
	}
	if isLimitErrorDetail(envelope.Error) {
		return true
	}
	if envelope.Response != nil && isLimitErrorDetail(envelope.Response.Error) {
		return true
	}
	return false
}

type limitErrorEnvelope struct {
	Type     string            `json:"type"`
	Status   int               `json:"status"`
	Error    *limitErrorDetail `json:"error"`
	Response *struct {
		Error *limitErrorDetail `json:"error"`
	} `json:"response"`
}

type limitErrorDetail struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func isLimitErrorDetail(detail *limitErrorDetail) bool {
	if detail == nil {
		return false
	}
	switch detail.Type {
	case "usage_limit_reached", "workspace_owner_usage_limit_reached", "workspace_member_usage_limit_reached":
		return true
	}
	switch detail.Code {
	case "usage_limit_reached", "usage_limit_exceeded", "usageLimitExceeded":
		return true
	}
	return false
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
