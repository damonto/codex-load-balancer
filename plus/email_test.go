package plus

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchLatestEmail(t *testing.T) {
	tests := []struct {
		name          string
		statusCode    int
		responseBody  string
		wantRecipient string
		wantErr       error
		wantErrText   string
	}{
		{
			name:       "returns latest email on success",
			statusCode: http.StatusOK,
			responseBody: `{
				"id": "msg-1",
				"recipient": "demo@example.com",
				"sender": "sender@example.com",
				"nexthop": "example.com",
				"subject": "Hello",
				"content": "Your OTP is 123456",
				"received_at": 1760000000000
			}`,
			wantRecipient: "demo@example.com",
		},
		{
			name:         "maps not found status",
			statusCode:   http.StatusNotFound,
			responseBody: `{"message":"not found"}`,
			wantErr:      errEmailNotFound,
		},
		{
			name:         "reports unexpected status",
			statusCode:   http.StatusBadGateway,
			responseBody: "upstream failed",
			wantErrText:  "latest email status 502: upstream failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Fatalf("method = %s, want GET", r.Method)
				}
				if got := r.URL.Query().Get("to"); got != "demo@example.com" {
					t.Fatalf("query to = %q, want %q", got, "demo@example.com")
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

			got, err := fetchLatestEmail(context.Background(), "demo@example.com")
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("fetchLatestEmail() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if tt.wantErrText != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("fetchLatestEmail() error = %v, want contains %q", err, tt.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatalf("fetchLatestEmail() error = %v", err)
			}
			if got.Recipient != tt.wantRecipient {
				t.Fatalf("fetchLatestEmail() recipient = %q, want %q", got.Recipient, tt.wantRecipient)
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
		{name: "active context", canceled: false, wantErr: false},
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

			_, err := GenerateWithContext(ctx)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GenerateWithContext() err = %v, wantErr %v", err, tt.wantErr)
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
			address: "demo@example.invalid",
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

func TestLatestChangedWithContext(t *testing.T) {
	tests := []struct {
		name        string
		record      emailMessage
		previous    string
		wantContent string
		wantFound   bool
	}{
		{
			name: "new message is returned when fingerprint differs",
			record: emailMessage{
				ID:      "msg-2",
				Subject: "OTP",
				Content: "Your OTP is 654321",
			},
			previous:    "id:msg-1",
			wantContent: "Your OTP is 654321",
			wantFound:   true,
		},
		{
			name: "same latest email is ignored",
			record: emailMessage{
				ID:      "msg-2",
				Subject: "OTP",
				Content: "Your OTP is 654321",
			},
			previous:  "id:msg-2",
			wantFound: false,
		},
		{
			name: "subject is used when content is empty",
			record: emailMessage{
				ID:      "msg-3",
				Subject: "Verification code 999999",
			},
			previous:    "",
			wantContent: "Verification code 999999",
			wantFound:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(tt.record); err != nil {
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

			got, found, err := latestChangedWithContext(context.Background(), "demo@example.com", tt.previous)
			if err != nil {
				t.Fatalf("latestChangedWithContext() error = %v", err)
			}
			if found != tt.wantFound {
				t.Fatalf("latestChangedWithContext() found = %v, want %v", found, tt.wantFound)
			}
			if got != tt.wantContent {
				t.Fatalf("latestChangedWithContext() content = %q, want %q", got, tt.wantContent)
			}
		})
	}
}
