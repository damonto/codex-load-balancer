package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProxyAuthorized(t *testing.T) {
	tests := []struct {
		name        string
		apiKey      string
		headers     map[string]string
		wantAllowed bool
	}{
		{
			name:   "valid bearer token",
			apiKey: "secret-123",
			headers: map[string]string{
				"Authorization": "Bearer secret-123",
			},
			wantAllowed: true,
		},
		{
			name:   "valid bearer token with mixed-case scheme",
			apiKey: "secret-123",
			headers: map[string]string{
				"Authorization": "bEaReR secret-123",
			},
			wantAllowed: true,
		},
		{
			name:   "valid x api key",
			apiKey: "secret-123",
			headers: map[string]string{
				"X-API-Key": "secret-123",
			},
			wantAllowed: true,
		},
		{
			name:   "wrong token",
			apiKey: "secret-123",
			headers: map[string]string{
				"Authorization": "Bearer wrong",
			},
			wantAllowed: false,
		},
		{
			name:        "missing token",
			apiKey:      "secret-123",
			headers:     map[string]string{},
			wantAllowed: false,
		},
		{
			name:   "empty configured key rejects all",
			apiKey: "",
			headers: map[string]string{
				"Authorization": "Bearer secret-123",
			},
			wantAllowed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/responses", nil)
			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}
			if got := proxyAuthorized(req, tt.apiKey); got != tt.wantAllowed {
				t.Fatalf("proxyAuthorized() = %v, want %v", got, tt.wantAllowed)
			}
		})
	}
}

func TestHandleProxyRejectsUnauthorizedBeforeSelection(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		authHeader string
		wantStatus int
	}{
		{
			name:       "responses without auth",
			path:       "/responses",
			authHeader: "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "responses wrong auth",
			path:       "/responses",
			authHeader: "Bearer wrong",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "forbidden path keeps forbidden",
			path:       "/v1/chat/completions",
			authHeader: "",
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{apiKey: "secret-123"}
			req := httptest.NewRequest(http.MethodPost, tt.path, nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rr := httptest.NewRecorder()
			s.handleProxy(rr, req)
			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
		})
	}
}
