package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestHandleProxyInjectsDefaultResponseTools(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
		plan string
		want bool
	}{
		{
			name: "responses",
			path: "/responses",
			body: `{"model":"gpt-5.4","tools":[]}`,
			want: true,
		},
		{
			name: "v1 responses",
			path: "/v1/responses",
			body: `{"model":"gpt-5.4","tools":[]}`,
			want: true,
		},
		{
			name: "free plan",
			path: "/responses",
			body: `{"model":"gpt-5.4","tools":[]}`,
			plan: "free",
			want: false,
		},
		{
			name: "spark model",
			path: "/responses",
			body: `{"model":"gpt-5.4-spark","tools":[]}`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotBody []byte
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var err error
				gotBody, err = io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("read upstream body: %v", err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"resp_1"}`))
			}))
			defer upstream.Close()

			target, err := url.Parse(upstream.URL)
			if err != nil {
				t.Fatalf("url.Parse() error = %v", err)
			}
			store := NewTokenStore()
			store.UpsertToken(TokenState{
				ID:       "active.json",
				Path:     "/tmp/active.json",
				Token:    "upstream-token",
				PlanType: tt.plan,
			}, time.Now().UTC())
			server := &Server{
				store:       store,
				client:      upstream.Client(),
				upstreamURL: target,
				apiKey:      "proxy-secret",
			}

			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Authorization", "Bearer proxy-secret")
			rr := httptest.NewRecorder()

			server.handleProxy(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
			}
			if hasToolType(gotBody, imageGenerationToolType) != tt.want {
				t.Fatalf("hasToolType() = %v, want %v; body=%s", hasToolType(gotBody, imageGenerationToolType), tt.want, string(gotBody))
			}
		})
	}
}
