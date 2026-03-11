package plus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendCheckoutURL(t *testing.T) {
	tests := []struct {
		name       string
		botToken   string
		chatID     string
		statusCode int
		response   string
		wantErr    string
		wantCalls  int
	}{
		{
			name:       "success",
			botToken:   "bot-token",
			chatID:     "123456789",
			statusCode: http.StatusOK,
			response:   `{"ok":true,"result":{}}`,
			wantCalls:  1,
		},
		{
			name:       "telegram api error",
			botToken:   "bot-token",
			chatID:     "123456789",
			statusCode: http.StatusOK,
			response:   `{"ok":false,"description":"chat not found"}`,
			wantErr:    "telegram sendMessage: chat not found",
			wantCalls:  1,
		},
		{
			name:      "missing telegram config is noop",
			wantCalls: 0,
		},
		{
			name:      "partial telegram config errors",
			botToken:  "bot-token",
			wantErr:   "telegram chat id is empty",
			wantCalls: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			var gotMessage telegramSendMessageRequest
			callCount := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				callCount++
				gotPath = r.URL.Path
				if err := json.NewDecoder(r.Body).Decode(&gotMessage); err != nil {
					t.Fatalf("decode request body: %v", err)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.response))
			}))
			defer server.Close()

			previousBaseURL := telegramAPIBaseURL
			previousClient := telegramHTTPClient
			telegramAPIBaseURL = server.URL
			telegramHTTPClient = server.Client()
			defer func() {
				telegramAPIBaseURL = previousBaseURL
				telegramHTTPClient = previousClient
			}()

			purchase := NewPurchase(&client{}, ChatGPTSession{
				User:    ChatGPTSessionUser{Email: "demo@example.com"},
				Account: ChatGPTSessionAccount{ID: "account-1"},
			}, tt.botToken, tt.chatID)

			err := purchase.sendCheckoutURL(context.Background(), "https://checkout.example.com/pay")
			if tt.wantErr == "" && err != nil {
				t.Fatalf("sendCheckoutURL() error = %v", err)
			}
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("sendCheckoutURL() error = nil, want %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("sendCheckoutURL() error = %v, want contains %q", err, tt.wantErr)
				}
				return
			}
			if callCount != tt.wantCalls {
				t.Fatalf("telegram calls = %d, want %d", callCount, tt.wantCalls)
			}
			if tt.wantCalls == 0 {
				return
			}

			if gotPath != "/botbot-token/sendMessage" {
				t.Fatalf("request path = %q, want %q", gotPath, "/botbot-token/sendMessage")
			}
			if gotMessage.ChatID != "123456789" {
				t.Fatalf("chat_id = %q, want %q", gotMessage.ChatID, "123456789")
			}
			if !strings.Contains(gotMessage.Text, "https://checkout.example.com/pay") {
				t.Fatalf("text = %q, want checkout url", gotMessage.Text)
			}
			if gotMessage.Text != "https://checkout.example.com/pay" {
				t.Fatalf("text = %q, want exact checkout url", gotMessage.Text)
			}
		})
	}
}

func TestCheckoutResponseCheckoutURL(t *testing.T) {
	tests := []struct {
		name     string
		response checkoutResponse
		want     string
		wantErr  string
	}{
		{
			name:     "prefer explicit url",
			response: checkoutResponse{URL: "https://checkout.example.com/direct"},
			want:     "https://checkout.example.com/direct",
		},
		{
			name:     "fallback to checkout_url field",
			response: checkoutResponse{HostedURL: "https://checkout.example.com/hosted"},
			want:     "https://checkout.example.com/hosted",
		},
		{
			name:     "build from session id",
			response: checkoutResponse{CheckoutSessionID: "cs_test_123"},
			want:     stripeCheckoutBaseURL + "cs_test_123",
		},
		{
			name:    "missing checkout fields",
			wantErr: "checkout response missing checkout session id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.response.CheckoutURL()
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("CheckoutURL() error = nil, want %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("CheckoutURL() error = %v, want contains %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("CheckoutURL() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("CheckoutURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
