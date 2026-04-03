package plus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestFetchEmailList(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		responseBody string
		wantCount    int
		wantErrText  string
	}{
		{
			name:         "returns payload on success",
			statusCode:   http.StatusOK,
			responseBody: `[{"id":"1","recipient":"demo@example.com","plaintext":"otp","received_at":"2026-04-02 10:00:00"}]`,
			wantCount:    1,
		},
		{
			name:         "reports upstream http status",
			statusCode:   http.StatusBadGateway,
			responseBody: "upstream failed",
			wantErrText:  "email list status 502: upstream failed",
		},
		{
			name:         "reports bad json",
			statusCode:   http.StatusOK,
			responseBody: "{",
			wantErrText:  "decode response JSON",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Fatalf("method = %s, want GET", r.Method)
				}
				if r.URL.Path != "/api.php" {
					t.Fatalf("path = %q, want %q", r.URL.Path, "/api.php")
				}
				if got := r.URL.Query().Get("to"); got != "demo@example.com" {
					t.Fatalf("query to = %q, want %q", got, "demo@example.com")
				}
				if got := r.Header.Get("Authorization"); got != "" {
					t.Fatalf("authorization = %q, want empty", got)
				}

				w.WriteHeader(tt.statusCode)
				if _, err := w.Write([]byte(tt.responseBody)); err != nil {
					t.Fatalf("Write() error = %v", err)
				}
			}))
			defer server.Close()

			oldAPIURL := emailAPIURL
			oldHTTPClient := emailHTTPClient
			emailAPIURL = server.URL
			emailHTTPClient = server.Client()
			t.Cleanup(func() {
				emailAPIURL = oldAPIURL
				emailHTTPClient = oldHTTPClient
			})

			got, err := fetchEmailList(context.Background(), "demo@example.com")
			if tt.wantErrText != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("fetchEmailList() error = %v, want contains %q", err, tt.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatalf("fetchEmailList() error = %v", err)
			}
			if len(got) != tt.wantCount {
				t.Fatalf("fetchEmailList() count = %d, want %d", len(got), tt.wantCount)
			}
			if got[0].EmailID != 1 {
				t.Fatalf("fetchEmailList() id = %d, want %d", got[0].EmailID, 1)
			}
		})
	}
}

func TestGenerateWithContext(t *testing.T) {
	tests := []struct {
		name     string
		canceled bool
		wantErr  bool
	}{
		{name: "active context"},
		{name: "canceled context", canceled: true, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.canceled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			got, err := GenerateWithContext(ctx)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GenerateWithContext() err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			localPart, domain, ok := strings.Cut(got, "@")
			if !ok {
				t.Fatalf("GenerateWithContext() = %q, want local@domain", got)
			}
			if len(localPart) != defaultEmailLocalPartLen {
				t.Fatalf("local part len = %d, want %d", len(localPart), defaultEmailLocalPartLen)
			}

			labels := strings.Split(domain, ".")
			if len(labels) < 3 {
				t.Fatalf("domain labels = %v, want subdomain + base domain", labels)
			}
			subdomain := labels[0]
			if !strings.HasSuffix(subdomain, "mail") {
				t.Fatalf("subdomain = %q, want suffix %q", subdomain, "mail")
			}
			if got := strings.TrimSuffix(subdomain, "mail"); len(got) != defaultEmailSubdomainRandLen {
				t.Fatalf("subdomain random part len = %d, want %d", len(got), defaultEmailSubdomainRandLen)
			}

			baseDomain := strings.Join(labels[1:], ".")
			if !slices.Contains(emailDomains[:], baseDomain) {
				t.Fatalf("base domain = %q, want one of %v", baseDomain, emailDomains)
			}
		})
	}
}

func TestLatestWithContext(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context
		address string
		wantErr bool
	}{
		{
			name:    "empty address",
			ctx:     context.Background(),
			address: "",
			wantErr: true,
		},
		{
			name: "canceled context",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			}(),
			address: "demo@example.com",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LatestWithContext(tt.ctx, tt.address)
			if (err != nil) != tt.wantErr {
				t.Fatalf("LatestWithContext() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLatestAfterWithContext(t *testing.T) {
	tests := []struct {
		name        string
		response    []emailListRecord
		after       emailCursor
		wantContent string
		wantFound   bool
	}{
		{
			name: "newer record is returned",
			response: []emailListRecord{{
				EmailID:    2,
				ToEmail:    "demo@example.com",
				Subject:    "OTP",
				Text:       "Your OTP is 654321",
				CreateTime: "2026-04-02 10:00:00",
			}},
			after:       emailCursor{EmailID: 1},
			wantContent: "Your OTP is 654321",
			wantFound:   true,
		},
		{
			name: "same latest email is ignored",
			response: []emailListRecord{{
				EmailID:    2,
				ToEmail:    "demo@example.com",
				Subject:    "OTP",
				Text:       "Your OTP is 654321",
				CreateTime: "2026-04-02 10:00:00",
			}},
			after:     emailCursor{EmailID: 2},
			wantFound: false,
		},
		{
			name: "subject is used when body is empty",
			response: []emailListRecord{{
				EmailID:    3,
				ToEmail:    "demo@example.com",
				Subject:    "Verification code 999999",
				CreateTime: "2026-04-02T10:00:00",
			}},
			wantContent: "Verification code 999999",
			wantFound:   true,
		},
		{
			name: "openai records are preferred",
			response: []emailListRecord{
				{
					EmailID:    4,
					ToEmail:    "demo@example.com",
					Subject:    "Other service",
					Text:       "123456",
					CreateTime: "2026-04-02 09:00:00",
				},
				{
					EmailID:    3,
					ToEmail:    "demo@example.com",
					SendEmail:  "noreply@openai.com",
					Subject:    "OpenAI verification",
					Text:       "654321",
					CreateTime: "2026-04-02 08:00:00",
				},
			},
			wantContent: "654321",
			wantFound:   true,
		},
		{
			name:      "missing record reports not found",
			response:  []emailListRecord{},
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(tt.response); err != nil {
					t.Fatalf("Encode() error = %v", err)
				}
			}))
			defer server.Close()

			oldAPIURL := emailAPIURL
			oldHTTPClient := emailHTTPClient
			emailAPIURL = server.URL
			emailHTTPClient = server.Client()
			t.Cleanup(func() {
				emailAPIURL = oldAPIURL
				emailHTTPClient = oldHTTPClient
			})

			got, found, err := latestAfterWithContext(context.Background(), "demo@example.com", tt.after)
			if err != nil {
				t.Fatalf("latestAfterWithContext() error = %v", err)
			}
			if found != tt.wantFound {
				t.Fatalf("latestAfterWithContext() found = %v, want %v", found, tt.wantFound)
			}
			if got != tt.wantContent {
				t.Fatalf("latestAfterWithContext() content = %q, want %q", got, tt.wantContent)
			}
		})
	}
}

func TestRandomMailboxDomain(t *testing.T) {
	tests := []struct {
		name       string
		baseDomain string
		wantErr    bool
	}{
		{name: "active base domain", baseDomain: "example.invalid"},
		{name: "empty base domain", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := randomMailboxDomain(tt.baseDomain)
			if (err != nil) != tt.wantErr {
				t.Fatalf("randomMailboxDomain() err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !strings.HasSuffix(got, "."+tt.baseDomain) {
				t.Fatalf("randomMailboxDomain() = %q, want suffix %q", got, "."+tt.baseDomain)
			}
			subdomain := strings.TrimSuffix(got, "."+tt.baseDomain)
			if !strings.HasSuffix(subdomain, "mail") {
				t.Fatalf("subdomain = %q, want suffix %q", subdomain, "mail")
			}
			if got := strings.TrimSuffix(subdomain, "mail"); len(got) != defaultEmailSubdomainRandLen {
				t.Fatalf("subdomain random part len = %d, want %d", len(got), defaultEmailSubdomainRandLen)
			}
		})
	}
}
