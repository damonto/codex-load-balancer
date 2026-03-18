package plus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var (
	telegramAPIBaseURL = "https://api.telegram.org"
	telegramHTTPClient = &http.Client{Timeout: 15 * time.Second}
)

const stripeFixedHash = "fidnandhYHdWcXxpYCc%2FJ2FgY2RwaXEnKSdpamZkaWAnPydgaycpJ3ZwZ3Zmd2x1cWxqa1BrbHRwYGtgdnZAa2RnaWBhJz9jZGl2YCknZHVsTmB8Jz8ndW5aaWxzYFowNE1Kd1ZyRjNtNGt9QmpMNmlRRGJXb1xTd38xYVA2Y1NKZGd8RmZOVzZ1Z0BPYnBGU0RpdEZ9YX1GUHNqV200XVJyV2RmU2xqc1A2bklOc3Vub20yTHRuUjU1bF1Udm9qNmsnKSdjd2poVmB3c2B3Jz9xd3BgKSdnZGZuYndqcGthRmppancnPycmY2NjY2NjJyknaWR8anBxUXx1YCc%2FJ3Zsa2JpYFpscWBoJyknYGtkZ2lgVWlkZmBtamlhYHd2Jz9xd3BgeCUl"

type Purchase struct {
	client           *client
	session          ChatGPTSession
	telegramBotToken string
	telegramChatID   string
}

type checkoutRequest struct {
	PlanName       string                 `json:"plan_name"`
	BillingDetails checkoutBillingDetails `json:"billing_details"`
	PromoCampaign  checkoutPromoCampaign  `json:"promo_campaign"`
	CheckoutUIMode string                 `json:"checkout_ui_mode"`
}

type checkoutBillingDetails struct {
	Country  string `json:"country"`
	Currency string `json:"currency"`
}

type checkoutPromoCampaign struct {
	PromoCampaignID        string `json:"promo_campaign_id"`
	IsCouponFromQueryParam bool   `json:"is_coupon_from_query_param"`
}

type checkoutResponse struct {
	CheckoutSessionID string `json:"checkout_session_id"`
	PublishableKey    string `json:"publishable_key"`
	ClientSecret      string `json:"client_secret"`
}

type telegramSendMessageRequest struct {
	ChatID string `json:"chat_id"`
	Text   string `json:"text"`
}

type telegramSendMessageResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
}

func (c checkoutResponse) String() string {
	return fmt.Sprintf(
		"https://checkout.stripe.com/c/pay/%s#%s",
		c.CheckoutSessionID,
		stripeFixedHash,
	)
}

func NewPurchase(client *client, session ChatGPTSession, telegramBotToken string, telegramChatID string) *Purchase {
	return &Purchase{
		client:           client,
		session:          session,
		telegramBotToken: telegramBotToken,
		telegramChatID:   telegramChatID,
	}
}

func (p *Purchase) CheckoutURL(ctx context.Context) (string, error) {
	checkout, err := p.requestCheckoutURL(ctx)
	if err != nil {
		return "", err
	}
	return checkout.String(), nil
}

func (p *Purchase) requestCheckoutURL(ctx context.Context) (checkoutResponse, error) {
	request := checkoutRequest{
		PlanName: "chatgptplusplan",
		BillingDetails: checkoutBillingDetails{
			Country:  "KR",
			Currency: "KRW",
		},
		PromoCampaign: checkoutPromoCampaign{
			PromoCampaignID:        "plus-1-month-free",
			IsCouponFromQueryParam: false,
		},
		CheckoutUIMode: "custom",
	}
	var response checkoutResponse
	err := p.client.PostJSON(ctx, "https://chatgpt.com/backend-api/payments/checkout", map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", p.session.AccessToken),
		"User-Agent":    chromeUserAgent,
	}, request, &response)
	if err != nil {
		return checkoutResponse{}, fmt.Errorf("post checkout request: %w", err)
	}
	return response, nil
}

func (p *Purchase) sendCheckoutURL(ctx context.Context, checkoutURL string) error {
	if p.telegramBotToken == "" && p.telegramChatID == "" {
		return nil
	}
	if p.telegramBotToken == "" {
		return errors.New("telegram bot token is empty")
	}
	if p.telegramChatID == "" {
		return errors.New("telegram chat id is empty")
	}

	body, err := json.Marshal(telegramSendMessageRequest{
		ChatID: p.telegramChatID,
		Text:   checkoutURL,
	})
	if err != nil {
		return fmt.Errorf("encode telegram message: %w", err)
	}

	endpoint := telegramAPIBaseURL + "/bot" + p.telegramBotToken + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build telegram sendMessage request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := telegramHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("send telegram message: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram sendMessage status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var payload telegramSendMessageResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return fmt.Errorf("decode telegram sendMessage response: %w", err)
	}
	if !payload.OK {
		if payload.Description == "" {
			return errors.New("telegram sendMessage returned ok=false")
		}
		return fmt.Errorf("telegram sendMessage: %s", payload.Description)
	}
	return nil
}
