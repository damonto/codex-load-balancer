package main

import (
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestForwardRequestWithTargetStripsHopByHopHeaders(t *testing.T) {
	tests := []struct {
		name string
	}{
		{
			name: "drops hop by hop request headers",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotHeaders http.Header
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotHeaders = r.Header.Clone()
				w.WriteHeader(http.StatusNoContent)
			}))
			defer upstream.Close()

			target, err := url.Parse(upstream.URL)
			if err != nil {
				t.Fatalf("url.Parse() error = %v", err)
			}

			server := &Server{client: upstream.Client()}
			req := httptest.NewRequest(http.MethodPost, "http://proxy.local/responses", strings.NewReader(`{"ok":true}`))
			req.Header.Set("Authorization", "Bearer client-token")
			req.Header.Set("Connection", "keep-alive, X-Test-Hop")
			req.Header.Set("Keep-Alive", "timeout=5")
			req.Header.Set("Proxy-Authorization", "Basic abc123")
			req.Header.Set("Proxy-Connection", "keep-alive")
			req.Header.Set("Te", "trailers")
			req.Header.Set("Trailer", "X-Trailer")
			req.Header.Set("Upgrade", "websocket")
			req.Header.Set("X-Test-Hop", "drop-me")
			req.Header.Set("X-Keep", "keep-me")

			resp, body, stream, err := server.forwardRequestWithTarget(req, []byte(`{"ok":true}`), *target, "proxy-token", "acct-1")
			if err != nil {
				t.Fatalf("forwardRequestWithTarget() error = %v", err)
			}
			if stream {
				t.Fatal("stream = true, want false")
			}
			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
			}
			if len(body) != 0 {
				t.Fatalf("body = %q, want empty", string(body))
			}

			for _, key := range []string{"Connection", "Keep-Alive", "Proxy-Authorization", "Proxy-Connection", "Te", "Trailer", "Upgrade", "X-Test-Hop"} {
				if value := gotHeaders.Get(key); value != "" {
					t.Fatalf("%s = %q, want empty", key, value)
				}
			}
			if got := gotHeaders.Get("Authorization"); got != "Bearer proxy-token" {
				t.Fatalf("Authorization = %q, want %q", got, "Bearer proxy-token")
			}
			if got := gotHeaders.Get("ChatGPT-Account-ID"); got != "acct-1" {
				t.Fatalf("ChatGPT-Account-ID = %q, want %q", got, "acct-1")
			}
			if got := gotHeaders.Get("X-Keep"); got != "keep-me" {
				t.Fatalf("X-Keep = %q, want %q", got, "keep-me")
			}
		})
	}
}

func TestCopyHeadersStripsHopByHopHeaders(t *testing.T) {
	tests := []struct {
		name string
		src  http.Header
	}{
		{
			name: "drops response hop by hop headers",
			src: http.Header{
				"Connection":        []string{"keep-alive, X-Test-Hop"},
				"Keep-Alive":        []string{"timeout=5"},
				"Proxy-Connection":  []string{"keep-alive"},
				"Trailer":           []string{"X-Trailer"},
				"Upgrade":           []string{"websocket"},
				"X-Test-Hop":        []string{"drop-me"},
				"X-Keep":            []string{"keep-me"},
				"Transfer-Encoding": []string{"chunked"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := make(http.Header)
			copyHeaders(dst, tt.src)

			for _, key := range []string{"Connection", "Keep-Alive", "Proxy-Connection", "Trailer", "Upgrade", "Transfer-Encoding", "X-Test-Hop"} {
				if value := dst.Get(key); value != "" {
					t.Fatalf("%s = %q, want empty", key, value)
				}
			}
			if got := dst.Get("X-Keep"); got != "keep-me" {
				t.Fatalf("X-Keep = %q, want %q", got, "keep-me")
			}
		})
	}
}

func TestForwardRequestWithTargetDecompressesInspectableResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "gzip response body stays parseable for usage extraction",
			body: `{"usage":{"input_tokens":12,"cached_input_tokens":3,"output_tokens":4}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !headerHasToken(r.Header.Get("Accept-Encoding"), "gzip") {
					t.Fatalf("Accept-Encoding = %q, want gzip negotiation", r.Header.Get("Accept-Encoding"))
				}
				w.Header().Set("Content-Encoding", "gzip")
				gz := gzip.NewWriter(w)
				if _, err := gz.Write([]byte(tt.body)); err != nil {
					t.Fatalf("gzip write: %v", err)
				}
				if err := gz.Close(); err != nil {
					t.Fatalf("gzip close: %v", err)
				}
			}))
			defer upstream.Close()

			target, err := url.Parse(upstream.URL)
			if err != nil {
				t.Fatalf("url.Parse() error = %v", err)
			}

			server := &Server{client: upstream.Client()}
			req := httptest.NewRequest(http.MethodPost, "http://proxy.local/responses", strings.NewReader(`{"ok":true}`))
			req.Header.Set("Accept-Encoding", "gzip")

			resp, body, stream, err := server.forwardRequestWithTarget(req, []byte(`{"ok":true}`), *target, "proxy-token", "")
			if err != nil {
				t.Fatalf("forwardRequestWithTarget() error = %v", err)
			}
			if stream {
				t.Fatal("stream = true, want false")
			}
			if resp.Header.Get("Content-Encoding") != "" {
				t.Fatalf("Content-Encoding = %q, want empty after transparent decompression", resp.Header.Get("Content-Encoding"))
			}

			usage, ok := extractTokenUsageFromBody(body)
			if !ok {
				t.Fatalf("extractTokenUsageFromBody(%q) = ok false, want true", string(body))
			}
			if usage.InputTokens != 9 || usage.CachedTokens != 3 || usage.OutputTokens != 4 {
				t.Fatalf("usage = %+v, want input=9 cached=3 output=4", usage)
			}
		})
	}
}
