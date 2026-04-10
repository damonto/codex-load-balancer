package plus

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	http "github.com/bogdanfinn/fhttp"
)

func TestPurchaseCheckoutSuccess(t *testing.T) {
	store := openPurchaseTokenStoreForTest(t)
	insertPurchaseTokenForTest(t, store, "fetch-token-1", purchaseTokenStatusAvailable, 1)

	lease, err := store.LeaseToken(context.Background())
	if err != nil {
		t.Fatalf("LeaseToken() error = %v", err)
	}

	client := &fakePurchaseHTTPClient{
		response: &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"subscriber":{}}`)),
		},
	}

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	purchase := NewPurchase(client, ChatGPTSession{
		Account: ChatGPTSessionAccount{ID: "account-1"},
	}, PurchaseConfig{
		Enabled:             true,
		RevenueCatBearerKey: "goog_test_key",
		Store:               store,
	}, lease)

	if err := purchase.Checkout(context.Background()); err != nil {
		t.Fatalf("Checkout() error = %v", err)
	}

	if client.target != revenueCatReceiptsURL {
		t.Fatalf("target = %q, want %q", client.target, revenueCatReceiptsURL)
	}
	if got := client.headers["Authorization"]; got != "Bearer goog_test_key" {
		t.Fatalf("Authorization = %q, want Bearer goog_test_key", got)
	}
	if got := client.headers["User-Agent"]; got != revenueCatUserAgent {
		t.Fatalf("User-Agent = %q, want %q", got, revenueCatUserAgent)
	}
	if got := client.headers["X-Platform"]; got != "android" {
		t.Fatalf("X-Platform = %q, want android", got)
	}
	if got := client.contentType; got != "application/json" {
		t.Fatalf("contentType = %q, want application/json", got)
	}
	if !strings.Contains(string(client.body), `"app_user_id":"account-1"`) {
		t.Fatalf("request body missing app_user_id: %s", client.body)
	}
	if !strings.Contains(string(client.body), `"product_ids":["oai.chatgpt.plus"]`) {
		t.Fatalf("request body missing product_ids: %s", client.body)
	}
	if !strings.Contains(string(client.body), `"platform_product_ids":[{"product_id":"oai.chatgpt.plus","base_plan_id":"oai-chatgpt-plus-1m-1999","offer_id":"plus-1-month-free-trial"}]`) {
		t.Fatalf("request body missing platform_product_ids: %s", client.body)
	}
	if strings.Contains(logs.String(), "fetch-token-1") {
		t.Fatalf("logs should not contain fetch token, got %q", logs.String())
	}

	row := loadPurchaseTokenRowForTest(t, store, lease.ID())
	if row.status != purchaseTokenStatusConsumed {
		t.Fatalf("status = %q, want %q", row.status, purchaseTokenStatusConsumed)
	}
	if row.attemptCount != 1 {
		t.Fatalf("attempt_count = %d, want 1", row.attemptCount)
	}
	if !row.accountID.Valid || row.accountID.String != "account-1" {
		t.Fatalf("account_id = %#v, want account-1", row.accountID)
	}
	if !row.responseStatusCode.Valid || row.responseStatusCode.Int64 != int64(http.StatusOK) {
		t.Fatalf("response_status_code = %#v, want 200", row.responseStatusCode)
	}
}

func TestPurchaseCheckoutServerErrorRequeuesToken(t *testing.T) {
	store := openPurchaseTokenStoreForTest(t)
	insertPurchaseTokenForTest(t, store, "fetch-token-2", purchaseTokenStatusAvailable, 1)

	lease, err := store.LeaseToken(context.Background())
	if err != nil {
		t.Fatalf("LeaseToken() error = %v", err)
	}

	purchase := NewPurchase(&fakePurchaseHTTPClient{
		response: &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(strings.NewReader(`upstream failed`)),
		},
	}, ChatGPTSession{
		Account: ChatGPTSessionAccount{ID: "account-2"},
	}, PurchaseConfig{
		Enabled:             true,
		RevenueCatBearerKey: "goog_test_key",
		Store:               store,
	}, lease)

	err = purchase.Checkout(context.Background())
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("Checkout() error = %v, want status 500", err)
	}

	row := loadPurchaseTokenRowForTest(t, store, lease.ID())
	if row.status != purchaseTokenStatusAvailable {
		t.Fatalf("status = %q, want %q", row.status, purchaseTokenStatusAvailable)
	}
	if row.attemptCount != 1 {
		t.Fatalf("attempt_count = %d, want 1", row.attemptCount)
	}
	if !row.accountID.Valid || row.accountID.String != "account-2" {
		t.Fatalf("account_id = %#v, want account-2", row.accountID)
	}
	if !row.responseStatusCode.Valid || row.responseStatusCode.Int64 != int64(http.StatusInternalServerError) {
		t.Fatalf("response_status_code = %#v, want 500", row.responseStatusCode)
	}
}

type fakePurchaseHTTPClient struct {
	response    *http.Response
	err         error
	target      string
	headers     map[string]string
	contentType string
	body        []byte
}

func (c *fakePurchaseHTTPClient) Post(_ context.Context, target string, headers map[string]string, body io.Reader, contentType string) (*http.Response, error) {
	c.target = target
	c.headers = headers
	c.contentType = contentType
	if body != nil {
		payload, err := io.ReadAll(body)
		if err != nil {
			return nil, err
		}
		c.body = payload
	}
	if c.err != nil {
		return nil, c.err
	}
	return c.response, nil
}
