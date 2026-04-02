package plus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchEmailList(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		responseBody string
		wantCode     int
		wantErrText  string
	}{
		{
			name:       "returns payload on success",
			statusCode: http.StatusOK,
			responseBody: `{
				"code": 200,
				"message": "ok",
				"data": [{"emailId": 1, "toEmail": "demo@example.com", "text": "otp"}]
			}`,
			wantCode: http.StatusOK,
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
				if r.Method != http.MethodPost {
					t.Fatalf("method = %s, want POST", r.Method)
				}
				if r.URL.Path != "/api/public/emailList" {
					t.Fatalf("path = %q, want %q", r.URL.Path, "/api/public/emailList")
				}
				if got := r.Header.Get("Authorization"); got != emailAuthToken {
					t.Fatalf("authorization = %q, want %q", got, emailAuthToken)
				}
				if got := r.Header.Get("Content-Type"); got != "application/json" {
					t.Fatalf("content type = %q, want %q", got, "application/json")
				}

				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatalf("Decode() error = %v", err)
				}
				if got := body["toEmail"]; got != "demo@example.com" {
					t.Fatalf("toEmail = %q, want %q", got, "demo@example.com")
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
			if got.Code != tt.wantCode {
				t.Fatalf("fetchEmailList() code = %d, want %d", got.Code, tt.wantCode)
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
			if !tt.wantErr && !strings.Contains(got, "@") {
				t.Fatalf("GenerateWithContext() = %q, want email address", got)
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
		response    emailListResponse
		after       emailCursor
		wantContent string
		wantFound   bool
	}{
		{
			name: "newer record is returned",
			response: emailListResponse{
				Code: http.StatusOK,
				Data: []emailListRecord{{
					EmailID:    2,
					ToEmail:    "demo@example.com",
					Subject:    "OTP",
					Text:       "Your OTP is 654321",
					CreateTime: "2026-04-02 10:00:00",
				}},
			},
			after:       emailCursor{EmailID: 1},
			wantContent: "Your OTP is 654321",
			wantFound:   true,
		},
		{
			name: "same latest email is ignored",
			response: emailListResponse{
				Code: http.StatusOK,
				Data: []emailListRecord{{
					EmailID:    2,
					ToEmail:    "demo@example.com",
					Subject:    "OTP",
					Text:       "Your OTP is 654321",
					CreateTime: "2026-04-02 10:00:00",
				}},
			},
			after:     emailCursor{EmailID: 2},
			wantFound: false,
		},
		{
			name: "subject is used when body is empty",
			response: emailListResponse{
				Code: http.StatusOK,
				Data: []emailListRecord{{
					EmailID:    3,
					ToEmail:    "demo@example.com",
					Subject:    "Verification code 999999",
					CreateTime: "2026-04-02T10:00:00",
				}},
			},
			wantContent: "Verification code 999999",
			wantFound:   true,
		},
		{
			name: "openai records are preferred",
			response: emailListResponse{
				Code: http.StatusOK,
				Data: []emailListRecord{
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
						SendName:   "OpenAI",
						Subject:    "OpenAI verification",
						Text:       "654321",
						CreateTime: "2026-04-02 08:00:00",
					},
				},
			},
			wantContent: "654321",
			wantFound:   true,
		},
		{
			name:      "missing record reports not found",
			response:  emailListResponse{Code: http.StatusOK},
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
