package main

import "testing"

func TestAllowedPathIncludesModels(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "responses", path: "/responses", want: true},
		{name: "responses child", path: "/responses/abc", want: true},
		{name: "v1 responses", path: "/v1/responses", want: true},
		{name: "models", path: "/models", want: true},
		{name: "models child", path: "/models/abc", want: true},
		{name: "v1 models", path: "/v1/models", want: true},
		{name: "chat completions", path: "/v1/chat/completions", want: false},
		{name: "models prefix only", path: "/models-extra", want: false},
		{name: "responses prefix only", path: "/responses-extra", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := allowedPath(tt.path); got != tt.want {
				t.Fatalf("allowedPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestNormalizeAPIPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "responses", path: "/responses", want: "/responses"},
		{name: "v1 responses", path: "/v1/responses", want: "/responses"},
		{name: "v1 responses child", path: "/v1/responses/abc", want: "/responses/abc"},
		{name: "models", path: "/models", want: "/models"},
		{name: "v1 models", path: "/v1/models", want: "/models"},
		{name: "v1 models child", path: "/v1/models/abc", want: "/models/abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeAPIPath(tt.path); got != tt.want {
				t.Fatalf("normalizeAPIPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsLimitError(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   bool
	}{
		{
			name:   "too many requests status",
			status: 429,
			want:   true,
		},
		{
			name: "codex usage limit error type",
			body: `{"error":{"type":"usage_limit_reached","message":"limit reached"}}`,
			want: true,
		},
		{
			name: "wrapped websocket usage limit status",
			body: `{"type":"error","status":429,"error":{"type":"usage_limit_reached","message":"limit reached"}}`,
			want: true,
		},
		{
			name: "response failed usage limit code",
			body: `{"type":"response.failed","response":{"error":{"code":"usage_limit_exceeded","message":"limit reached"}}}`,
			want: true,
		},
		{
			name: "rate limit exceeded code is not usage limit",
			body: `{"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded","message":"try again later"}}}`,
		},
		{
			name: "non limit json",
			body: `{"error":{"type":"invalid_request_error","message":"bad request"}}`,
		},
		{
			name: "non json body",
			body: `upstream error`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLimitError(tt.status, []byte(tt.body)); got != tt.want {
				t.Fatalf("isLimitError() = %v, want %v", got, tt.want)
			}
		})
	}
}
