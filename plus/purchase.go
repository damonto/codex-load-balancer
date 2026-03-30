package plus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
)

const (
	stripeBuildHash        = "f197c9c0f0"
	stripeFingerprintBuild = "m-outer-3437aaddcdf6922d623e172c2d6f9278"
)

const stripeFixedHash = "fidnandhYHdWcXxpYCc%2FJ2FgY2RwaXEnKSdpamZkaWAnPydgaycpJ3ZwZ3Zmd2x1cWxqa1BrbHRwYGtgdnZAa2RnaWBhJz9jZGl2YCknZHVsTmB8Jz8ndW5aaWxzYFowNE1Kd1ZyRjNtNGt9QmpMNmlRRGJXb1xTd38xYVA2Y1NKZGd8RmZOVzZ1Z0BPYnBGU0RpdEZ9YX1GUHNqV200XVJyV2RmU2xqc1A2bklOc3Vub20yTHRuUjU1bF1Udm9qNmsnKSdjd2poVmB3c2B3Jz9xd3BgKSdnZGZuYndqcGthRmppancnPycmY2NjY2NjJyknaWR8anBxUXx1YCc%2FJ3Zsa2JpYFpscWBoJyknYGtkZ2lgVWlkZmBtamlhYHd2Jz9xd3BgeCUl"

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
	AddressState string
	PostalCode   string
}

type stripeFingerprint struct {
	GUID string `json:"guid"`
	MUID string `json:"muid"`
	SID  string `json:"sid"`
}

type fingerprintRequest struct {
	V string `json:"v"`
	T int    `json:"t"`
}

type paymentMethodResponse struct {
	ID string `json:"id"`
}

type paymentPageInitResponse struct {
	EID          string `json:"eid"`
	InitChecksum string `json:"init_checksum"`
	TotalSummary struct {
		Due int `json:"due"`
	} `json:"total_summary"`
	TaxMeta struct {
		Status string `json:"status"`
	} `json:"tax_meta"`
	TaxContext struct {
		AutomaticTaxEnabled bool `json:"automatic_tax_enabled"`
	} `json:"tax_context"`
}

type paymentPageConfirmResponse struct {
	Status        string `json:"status"`
	ClientSecret  string `json:"client_secret"`
	PaymentIntent struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"payment_intent"`
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
	return fmt.Sprintf(
		"https://checkout.stripe.com/c/pay/%s#%s",
		c.CheckoutSessionID,
		stripeFixedHash,
	)
}

type PaymentBilling struct {
	Name         string
	Email        string
	Country      string
	AddressLine1 string
	AddressState string
	PostalCode   string
}

func ValidatePurchaseConfig(cfg PurchaseConfig) (PurchaseConfig, error) {
	if !cfg.Enabled {
		return cfg, nil
	}
	cfg.PlanName = strings.TrimSpace(cfg.PlanName)
	if cfg.PlanName == "" {
		return PurchaseConfig{}, errors.New("purchase plan name is empty")
	}
	cfg.Currency = strings.TrimSpace(cfg.Currency)
	if cfg.Currency == "" {
		return PurchaseConfig{}, errors.New("purchase currency is empty")
	}
	cfg.PromoCampaignID = strings.TrimSpace(cfg.PromoCampaignID)
	if cfg.PromoCampaignID == "" {
		return PurchaseConfig{}, errors.New("purchase promo campaign id is empty")
	}
	cfg.CheckoutUIMode = strings.TrimSpace(cfg.CheckoutUIMode)
	if cfg.CheckoutUIMode == "" {
		return PurchaseConfig{}, errors.New("purchase checkout ui mode is empty")
	}
	cfg.Billing.Name = strings.TrimSpace(cfg.Billing.Name)
	if cfg.Billing.Name == "" {
		return PurchaseConfig{}, errors.New("purchase billing name is empty")
	}
	cfg.Billing.Country = strings.TrimSpace(cfg.Billing.Country)
	if cfg.Billing.Country == "" {
		return PurchaseConfig{}, errors.New("purchase billing country is empty")
	}
	cfg.Billing.AddressLine1 = strings.TrimSpace(cfg.Billing.AddressLine1)
	if cfg.Billing.AddressLine1 == "" {
		return PurchaseConfig{}, errors.New("purchase billing address line1 is empty")
	}
	cfg.Billing.AddressState = strings.TrimSpace(cfg.Billing.AddressState)
	if cfg.Billing.AddressState == "" {
		return PurchaseConfig{}, errors.New("purchase billing address state is empty")
	}
	cfg.Billing.PostalCode = strings.TrimSpace(cfg.Billing.PostalCode)
	if cfg.Billing.PostalCode == "" {
		return PurchaseConfig{}, errors.New("purchase billing postal code is empty")
	}
	paymentCard, err := normalizePaymentCardConfig(cfg.PaymentCard)
	if err != nil {
		return PurchaseConfig{}, fmt.Errorf("normalize payment card config: %w", err)
	}
	cfg.PaymentCard = paymentCard
	return cfg, nil
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

	checkout, err := p.requestCheckoutURL(ctx)
	if err != nil {
		return fmt.Errorf("request plus checkout: %w", err)
	}
	slog.Info("checkout", "email", p.session.User.Email, "checkout_url", checkout)

	var lastErr error
	for range 10 {
		if err := p.pay(ctx, checkout); err != nil {
			lastErr = err
			slog.Error("checkout failed", "email", p.session.User.Email, "err", err)
			continue
		}
		return nil
	}
	return lastErr
}

func (p *Purchase) requestCheckoutURL(ctx context.Context) (checkoutResponse, error) {
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
	return response, nil
}

func (p *Purchase) pay(ctx context.Context, checkout checkoutResponse) error {
	fingerprint, err := p.fetchStripeFingerprint(ctx)
	if err != nil {
		return fmt.Errorf("fetch stripe fingerprint: %w", err)
	}

	billing := PaymentBilling{
		Name:         p.cfg.Billing.Name,
		Email:        strings.TrimSpace(p.session.User.Email),
		Country:      p.cfg.Billing.Country,
		AddressLine1: p.cfg.Billing.AddressLine1,
		AddressState: p.cfg.Billing.AddressState,
		PostalCode:   p.cfg.Billing.PostalCode,
	}

	card := randomCard(p.cfg.PaymentCard)
	paymentMethod, err := p.createPaymentMethod(ctx, checkout, billing, card, fingerprint)
	if err != nil {
		return fmt.Errorf("create payment method for %s: %w", card.Number, err)
	}
	slog.Info("stripe payment method", "email", p.session.User.Email, "card_number", card.Number, "payment_method", paymentMethod)

	paymentPageResp, err := p.fetchPaymentPageDetails(ctx, checkout)
	if err != nil {
		return fmt.Errorf("fetch payment page details for %s: %w", card.Number, err)
	}

	result, err := p.confirmPayment(ctx, checkout, paymentMethod, fingerprint, paymentPageResp)
	if err != nil {
		return fmt.Errorf("confirm payment for %s %w", card.Number, err)
	}
	if paymentSucceeded(result) {
		slog.Info("stripe payment confirmed", "email", p.session.User.Email, "card_number", card.Number, "payment_method", paymentMethod)
		return nil
	}
	return fmt.Errorf("payment for %s returned status %q / %q", card.Number, result.Status, result.PaymentIntent.Status)
}

func (p *Purchase) fetchStripeFingerprint(ctx context.Context) (stripeFingerprint, error) {
	headers := map[string]string{
		"Accept":  "*/*",
		"Origin":  "https://m.stripe.network",
		"Referer": "https://m.stripe.network/",
	}
	var fingerprint stripeFingerprint
	err := p.client.PostJSON(ctx, "https://m.stripe.com/6", headers, fingerprintRequest{
		V: stripeFingerprintBuild,
		T: 0,
	}, &fingerprint)
	if err == nil && fingerprint.GUID != "" {
		return fingerprint, nil
	}
	if err != nil {
		return stripeFingerprint{}, err
	}
	return stripeFingerprint{}, errors.New("stripe fingerprint guid is empty")
}

func (p *Purchase) createPaymentMethod(ctx context.Context, checkout checkoutResponse, billing PaymentBilling, card PaymentCard, fingerprint stripeFingerprint) (string, error) {
	form := url.Values{
		"type":                                  {"card"},
		"card[number]":                          {card.Number},
		"card[cvc]":                             {card.CVC},
		"card[exp_month]":                       {card.ExpMonth},
		"card[exp_year]":                        {card.ExpYear},
		"billing_details[name]":                 {billing.Name},
		"billing_details[email]":                {billing.Email},
		"billing_details[address][country]":     {billing.Country},
		"billing_details[address][line1]":       {billing.AddressLine1},
		"billing_details[address][state]":       {billing.AddressState},
		"billing_details[address][postal_code]": {billing.PostalCode},
		"allow_redisplay":                       {"always"},
		"guid":                                  {fingerprint.GUID},
		"muid":                                  {fingerprint.MUID},
		"sid":                                   {fingerprint.SID},
		"payment_user_agent":                    {fmt.Sprintf("stripe.js/%s; stripe-js-v3/%s; checkout", stripeBuildHash, stripeBuildHash)},
	}

	var payload paymentMethodResponse
	if err := p.client.PostForm(ctx, "https://api.stripe.com/v1/payment_methods", stripeHeaders(checkout.PublishableKey), form, &payload); err != nil {
		return "", err
	}
	if payload.ID == "" {
		return "", errors.New("payment method id is empty")
	}
	return payload.ID, nil
}

func (p *Purchase) fetchPaymentPageDetails(ctx context.Context, checkout checkoutResponse) (paymentPageInitResponse, error) {
	form := url.Values{
		"key":            {checkout.PublishableKey},
		"browser_locale": {"en"},
	}

	var payload paymentPageInitResponse
	if err := p.client.PostForm(ctx, "https://api.stripe.com/v1/payment_pages/"+checkout.CheckoutSessionID+"/init", stripeHeaders(checkout.PublishableKey), form, &payload); err != nil {
		return paymentPageInitResponse{}, err
	}
	return payload, nil
}

func (p *Purchase) confirmPayment(ctx context.Context, checkout checkoutResponse, paymentMethod string, fingerprint stripeFingerprint, paymentPageResp paymentPageInitResponse) (paymentPageConfirmResponse, error) {
	form := url.Values{
		"payment_method":  {paymentMethod},
		"guid":            {fingerprint.GUID},
		"muid":            {fingerprint.MUID},
		"sid":             {fingerprint.SID},
		"expected_amount": {"0"},
		"key":             {checkout.PublishableKey},
	}
	if paymentPageResp.EID != "" {
		form.Set("eid", paymentPageResp.EID)
	}
	if paymentPageResp.InitChecksum != "" {
		form.Set("init_checksum", paymentPageResp.InitChecksum)
	}

	var payload paymentPageConfirmResponse
	if err := p.client.PostForm(ctx, "https://api.stripe.com/v1/payment_pages/"+checkout.CheckoutSessionID+"/confirm", stripeHeaders(checkout.PublishableKey), form, &payload); err != nil {
		return paymentPageConfirmResponse{}, err
	}
	return payload, nil
}

func stripeHeaders(publishableKey string) map[string]string {
	return map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", publishableKey),
		"Content-Type":  "application/x-www-form-urlencoded",
		"Accept":        "application/json",
		"Origin":        "https://js.stripe.com",
		"Referer":       "https://js.stripe.com/",
	}
}

func paymentSucceeded(payload paymentPageConfirmResponse) bool {
	if payload.Status == "complete" || payload.Status == "succeeded" {
		return true
	}
	if payload.Status == "open" && payload.PaymentIntent.Status == "succeeded" {
		return true
	}
	switch payload.PaymentIntent.Status {
	case "succeeded", "processing":
		return true
	default:
		return false
	}
}
