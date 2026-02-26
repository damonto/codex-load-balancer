package main

import (
	"net/http"
	"testing"
)

func TestAllowedPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "responses exact",
			path: "/responses",
			want: true,
		},
		{
			name: "responses nested",
			path: "/responses/abc",
			want: true,
		},
		{
			name: "v1 responses exact",
			path: "/v1/responses",
			want: true,
		},
		{
			name: "v1 responses nested",
			path: "/v1/responses/abc",
			want: true,
		},
		{
			name: "responses prefix false positive",
			path: "/responsesx",
			want: false,
		},
		{
			name: "v1 responses prefix false positive",
			path: "/v1/responsesx",
			want: false,
		},
		{
			name: "other path",
			path: "/v1/chat/completions",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := allowedPath(tt.path); got != tt.want {
				t.Fatalf("allowedPath() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHeaderHasToken(t *testing.T) {
	tests := []struct {
		name  string
		value string
		token string
		want  bool
	}{
		{
			name:  "single token",
			value: "upgrade",
			token: "upgrade",
			want:  true,
		},
		{
			name:  "mixed case and spaces",
			value: "keep-alive, Upgrade",
			token: "upgrade",
			want:  true,
		},
		{
			name:  "missing token",
			value: "keep-alive",
			token: "upgrade",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := headerHasToken(tt.value, tt.token); got != tt.want {
				t.Fatalf("headerHasToken() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsWebSocketRequest(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		connection string
		upgrade    string
		want       bool
	}{
		{
			name:       "valid websocket upgrade",
			method:     http.MethodGet,
			connection: "keep-alive, Upgrade",
			upgrade:    "websocket",
			want:       true,
		},
		{
			name:       "missing connection upgrade token",
			method:     http.MethodGet,
			connection: "keep-alive",
			upgrade:    "websocket",
			want:       false,
		},
		{
			name:       "wrong upgrade value",
			method:     http.MethodGet,
			connection: "upgrade",
			upgrade:    "h2c",
			want:       false,
		},
		{
			name:       "non get websocket handshake",
			method:     http.MethodPost,
			connection: "upgrade",
			upgrade:    "websocket",
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, "http://localhost/responses", nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Connection", tt.connection)
			req.Header.Set("Upgrade", tt.upgrade)
			if got := isWebSocketRequest(req); got != tt.want {
				t.Fatalf("isWebSocketRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}
