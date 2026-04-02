package plus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	stripeflow "github.com/damonto/codex-load-balancer/plus/stripe"
)

type Purchase struct {
	client  *client
	session ChatGPTSession
	cfg     PurchaseConfig
}

type PurchaseConfig struct {
	Enabled         bool
	PlanName        string
	Currency        string
	PromoCampaignID string
	CheckoutUIMode  string
	Billing         PurchaseBillingConfig
	PaymentCard     PaymentCardConfig
}

type PurchaseBillingConfig struct {
	Name         string
	Country      string
	AddressLine1 string
	AddressCity  string
	AddressState string
	PostalCode   string
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

func (c checkoutResponse) String() string {
	return stripeflow.Checkout{SessionID: c.CheckoutSessionID}.URL()
}

func NewPurchase(client *client, session ChatGPTSession, cfg PurchaseConfig) *Purchase {
	return &Purchase{
		client:  client,
		session: session,
		cfg:     cfg,
	}
}

func (p *Purchase) Checkout(ctx context.Context) error {
	if !p.cfg.Enabled {
		return nil
	}

	slog.Info(
		"purchase checkout started",
		"email", p.session.User.Email,
		"plan", p.cfg.PlanName,
		"currency", p.cfg.Currency,
		"checkout_ui_mode", p.cfg.CheckoutUIMode,
	)
	checkout, err := p.requestCheckoutURL(ctx)
	if err != nil {
		slog.Warn("purchase checkout request failed", "email", p.session.User.Email, "err", err)
		return fmt.Errorf("request plus checkout: %w", err)
	}
	slog.Info(
		"purchase checkout session ready",
		"email", p.session.User.Email,
		"checkout_session", shortPurchaseID(checkout.CheckoutSessionID),
		"checkout_url", checkout.String(),
	)
	var lastErr error
	for attempt := range 10 {
		slog.Info(
			"purchase payment attempt started",
			"email", p.session.User.Email,
			"attempt", attempt+1,
			"max_attempts", 10,
		)
		if err := p.pay(ctx, checkout); err != nil {
			lastErr = err
			slog.Warn(
				"purchase payment attempt failed",
				"email", p.session.User.Email,
				"attempt", attempt+1,
				"err", err,
			)
			continue
		}
		slog.Info(
			"purchase completed",
			"email", p.session.User.Email,
			"attempt", attempt+1,
		)
		return nil
	}
	slog.Error("purchase failed after retries", "email", p.session.User.Email, "attempts", 10, "err", lastErr)
	return lastErr
}

func (p *Purchase) requestCheckoutURL(ctx context.Context) (checkoutResponse, error) {
	slog.Info(
		"requesting plus checkout url",
		"email", p.session.User.Email,
		"plan", p.cfg.PlanName,
		"currency", p.cfg.Currency,
	)
	request := checkoutRequest{
		PlanName: p.cfg.PlanName,
		BillingDetails: checkoutBillingDetails{
			Country:  p.cfg.Billing.Country,
			Currency: p.cfg.Currency,
		},
		PromoCampaign: checkoutPromoCampaign{
			PromoCampaignID:        p.cfg.PromoCampaignID,
			IsCouponFromQueryParam: false,
		},
		CheckoutUIMode: p.cfg.CheckoutUIMode,
	}
	var response checkoutResponse
	err := p.client.PostJSON(ctx, chatgptURL+"/backend-api/payments/checkout", map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", p.session.AccessToken),
		"User-Agent":    chromeUserAgent,
	}, request, &response)
	if err != nil {
		return checkoutResponse{}, fmt.Errorf("post checkout request: %w", err)
	}
	slog.Info(
		"plus checkout url received",
		"email", p.session.User.Email,
		"checkout_session", shortPurchaseID(response.CheckoutSessionID),
		"has_publishable_key", strings.TrimSpace(response.PublishableKey) != "",
	)
	return response, nil
}

func (p *Purchase) pay(ctx context.Context, checkout checkoutResponse) error {
	billing := stripeflow.Billing{
		Name:         p.cfg.Billing.Name,
		Email:        strings.TrimSpace(p.session.User.Email),
		Country:      p.cfg.Billing.Country,
		AddressLine1: p.cfg.Billing.AddressLine1,
		AddressCity:  p.cfg.Billing.AddressCity,
		AddressState: p.cfg.Billing.AddressState,
		PostalCode:   p.cfg.Billing.PostalCode,
	}

	card, err := randomCard(p.cfg.PaymentCard)
	if err != nil {
		return fmt.Errorf("pick payment card: %w", err)
	}
	slog.Info(
		"purchase card selected",
		"email", p.session.User.Email,
		"checkout_session", shortPurchaseID(checkout.CheckoutSessionID),
		"billing_country", billing.Country,
		"card_number", card.Number,
		"card_last4", last4(card.Number),
	)
	client, err := p.client.Refresh()
	if err != nil {
		return fmt.Errorf("refresh session: %w", err)
	}
	processor := stripeflow.NewProcessor(
		stripeHTTPClient{raw: client},
		stripeflow.Checkout{
			SessionID:      checkout.CheckoutSessionID,
			PublishableKey: checkout.PublishableKey,
		},
		p.session.AccessToken,
		p.cfg.Currency,
		chromeUserAgent,
	)
	slog.Info(
		"stripe processor started",
		"email", p.session.User.Email,
		"checkout_session", shortPurchaseID(checkout.CheckoutSessionID),
	)
	if err := processor.Pay(ctx, billing, stripeflow.Card{
		Number:   card.Number,
		CVC:      card.CVC,
		ExpMonth: card.ExpMonth,
		ExpYear:  card.ExpYear,
	}); err != nil {
		return fmt.Errorf("stripe checkout flow: %w", err)
	}
	slog.Info(
		"stripe processor completed",
		"email", p.session.User.Email,
		"checkout_session", shortPurchaseID(checkout.CheckoutSessionID),
	)
	return nil
}

type stripeHTTPClient struct {
	raw *client
}

func (c stripeHTTPClient) GetJSON(ctx context.Context, target string, headers map[string]string, out any) error {
	return convertStripeError(c.raw.GetJSON(ctx, target, headers, out))
}

func (c stripeHTTPClient) PostForm(ctx context.Context, target string, headers map[string]string, values url.Values, out any) error {
	return convertStripeError(c.raw.PostForm(ctx, target, headers, values, out))
}

func (c stripeHTTPClient) PostRawJSON(ctx context.Context, target string, headers map[string]string, body string, contentType string, out any) error {
	resp, err := c.raw.Post(ctx, target, headers, strings.NewReader(body), contentType)
	if err != nil {
		return convertStripeError(err)
	}
	defer resp.Body.Close()

	if err := expectStatus(resp, 200); err != nil {
		return convertStripeError(err)
	}
	if err := decodeJSON(resp.Body, out); err != nil {
		return err
	}
	return nil
}

func convertStripeError(err error) error {
	var responseErr responseError
	if errors.As(err, &responseErr) {
		return stripeflow.ResponseError{
			StatusCode: responseErr.StatusCode,
			Body:       responseErr.Body,
		}
	}
	return err
}

func shortPurchaseID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return value
	}
	return value[:4] + "..." + value[len(value)-4:]
}

func last4(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 4 {
		return value
	}
	return value[len(value)-4:]
}
