package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestHandleProxyPreservesUpstreamFailureWhenNoAlternateToken(t *testing.T) {
	tests := []struct {
		name           string
		upstreamStatus int
		upstreamBody   string
	}{
		{
			name:           "unauthorized",
			upstreamStatus: http.StatusUnauthorized,
			upstreamBody:   "upstream unauthorized",
		},
		{
			name:           "usage limit",
			upstreamStatus: http.StatusTooManyRequests,
			upstreamBody:   "You've hit your usage limit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotAuth string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				w.WriteHeader(tt.upstreamStatus)
				_, _ = w.Write([]byte(tt.upstreamBody))
			}))
			defer upstream.Close()

			target, err := url.Parse(upstream.URL)
			if err != nil {
				t.Fatalf("url.Parse() error = %v", err)
			}

			store := NewTokenStore()
			store.UpsertToken(TokenState{
				ID:    "active.json",
				Path:  "/tmp/active.json",
				Token: "upstream-token",
			}, time.Now().UTC())

			server := &Server{
				store:       store,
				client:      upstream.Client(),
				upstreamURL: target,
				apiKey:      "proxy-secret",
			}

			req := httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(`{"model":"gpt-5"}`))
			req.Header.Set("Authorization", "Bearer proxy-secret")
			rr := httptest.NewRecorder()

			server.handleProxy(rr, req)

			if rr.Code != tt.upstreamStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.upstreamStatus)
			}
			if got := rr.Body.String(); got != tt.upstreamBody {
				t.Fatalf("body = %q, want %q", got, tt.upstreamBody)
			}
			if gotAuth != "Bearer upstream-token" {
				t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer upstream-token")
			}
		})
	}
}

func TestHandleProxyForwardsModelsRequest(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		wantPath string
	}{
		{name: "models", path: "/models", wantPath: "/models"},
		{name: "v1 models", path: "/v1/models", wantPath: "/models"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			var gotQuery string
			var gotAuth string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotQuery = r.URL.RawQuery
				gotAuth = r.Header.Get("Authorization")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"models":[]}`))
			}))
			defer upstream.Close()

			target, err := url.Parse(upstream.URL)
			if err != nil {
				t.Fatalf("url.Parse() error = %v", err)
			}

			store := NewTokenStore()
			store.UpsertToken(TokenState{
				ID:    "active.json",
				Path:  "/tmp/active.json",
				Token: "upstream-token",
			}, time.Now().UTC())

			server := &Server{
				store:       store,
				client:      upstream.Client(),
				upstreamURL: target,
				apiKey:      "proxy-secret",
			}

			req := httptest.NewRequest(http.MethodGet, tt.path+"?client_version=1.2.3", nil)
			req.Header.Set("Authorization", "Bearer proxy-secret")
			rr := httptest.NewRecorder()

			server.handleProxy(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
			}
			if gotPath != tt.wantPath {
				t.Fatalf("upstream path = %q, want %q", gotPath, tt.wantPath)
			}
			if gotQuery != "client_version=1.2.3" {
				t.Fatalf("upstream query = %q, want %q", gotQuery, "client_version=1.2.3")
			}
			if gotAuth != "Bearer upstream-token" {
				t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer upstream-token")
			}
			if got := rr.Body.String(); got != `{"models":[]}` {
				t.Fatalf("body = %q, want %q", got, `{"models":[]}`)
			}
		})
	}
}

func TestHandleWebSocketPreservesUpstreamFailureWhenNoAlternateToken(t *testing.T) {
	tests := []struct {
		name           string
		upstreamStatus int
		upstreamBody   string
	}{
		{
			name:           "unauthorized",
			upstreamStatus: http.StatusUnauthorized,
			upstreamBody:   "upstream unauthorized",
		},
		{
			name:           "usage limit",
			upstreamStatus: http.StatusTooManyRequests,
			upstreamBody:   "You've hit your usage limit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotAuth string
			var gotAcceptEncoding string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				gotAcceptEncoding = r.Header.Get("Accept-Encoding")
				w.WriteHeader(tt.upstreamStatus)
				_, _ = w.Write([]byte(tt.upstreamBody))
			}))
			defer upstream.Close()

			target, err := url.Parse(upstream.URL)
			if err != nil {
				t.Fatalf("url.Parse() error = %v", err)
			}

			store := NewTokenStore()
			store.UpsertToken(TokenState{
				ID:    "active.json",
				Path:  "/tmp/active.json",
				Token: "upstream-token",
			}, time.Now().UTC())

			server := &Server{
				store:       store,
				client:      upstream.Client(),
				upstreamURL: target,
				apiKey:      "proxy-secret",
			}

			req := httptest.NewRequest(http.MethodGet, "/responses", nil)
			req.Header.Set("Authorization", "Bearer proxy-secret")
			req.Header.Set("Connection", "keep-alive, Upgrade")
			req.Header.Set("Upgrade", "websocket")
			req.Header.Set("Sec-WebSocket-Version", "13")
			req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
			req.Header.Set("Accept-Encoding", "gzip")
			rr := httptest.NewRecorder()

			server.handleProxy(rr, req)

			if rr.Code != tt.upstreamStatus {
				t.Fatalf("status = %d, want %d", rr.Code, tt.upstreamStatus)
			}
			if got := rr.Body.String(); got != tt.upstreamBody {
				t.Fatalf("body = %q, want %q", got, tt.upstreamBody)
			}
			if gotAuth != "Bearer upstream-token" {
				t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer upstream-token")
			}
			if gotAcceptEncoding != "" {
				t.Fatalf("Accept-Encoding = %q, want empty", gotAcceptEncoding)
			}
		})
	}
}
