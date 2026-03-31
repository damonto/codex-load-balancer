package stripe

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

const (
	buildHash                       = "5412f474d5"
	versionBase                     = "2025-03-31.basil"
	versionFull                     = "2025-03-31.basil; checkout_server_update_beta=v1; checkout_manual_approval_preview=v1"
	browserLocale                   = "en-US"
	browserLocaleShort              = "en"
	checkoutReferrerHost            = "chatgpt.com"
	elementsInitSource              = "custom_checkout"
	paymentMethodAllowRedisplay     = "unspecified"
	paymentMethodMerchantVersion    = "2021"
	confirmMerchantVersion          = "custom"
	pollAttempts                    = 6
	timeOnPageMinMillis             = 25_000
	timeOnPageMaxMillis             = 55_000
	elementsSessionPrefix           = "elements_session_"
	fixedHash                       = "fidnandhYHdWcXxpYCc%2FJ2FgY2RwaXEnKSdpamZkaWAnPydgaycpJ3ZwZ3Zmd2x1cWxqa1BrbHRwYGtgdnZAa2RnaWBhJz9jZGl2YCknZHVsTmB8Jz8ndW5aaWxzYFowNE1Kd1ZyRjNtNGt9QmpMNmlRRGJXb1xTd38xYVA2Y1NKZGd8RmZOVzZ1Z0BPYnBGU0RpdEZ9YX1GUHNqV200XVJyV2RmU2xqc1A2bklOc3Vub20yTHRuUjU1bF1Udm9qNmsnKSdjd2poVmB3c2B3Jz9xd3BgKSdnZGZuYndqcGthRmppancnPycmY2NjY2NjJyknaWR8anBxUXx1YCc%2FJ3Zsa2JpYFpscWBoJyknYGtkZ2lgVWlkZmBtamlhYHd2Jz9xd3BgeCUl"
	fingerprintPlugins              = "PDF Viewer,internal-pdf-viewer,application/pdf,pdf++text/pdf,pdf, Chrome PDF Viewer,internal-pdf-viewer,application/pdf,pdf++text/pdf,pdf, Chromium PDF Viewer,internal-pdf-viewer,application/pdf,pdf++text/pdf,pdf, Microsoft Edge PDF Viewer,internal-pdf-viewer,application/pdf,pdf++text/pdf,pdf, WebKit built-in PDF,internal-pdf-viewer,application/pdf,pdf++text/pdf,pdf"
	fingerprintBrowserState         = "sessionStorage-enabled, localStorage-enabled"
	fingerprintPlatform             = "Win32"
	fingerprintTag                  = "$npm_package_version"
	fingerprintTelemetrySource      = "js"
	confirmTermsAccepted            = "accepted"
	expectedPaymentMethodType       = "card"
	paymentMethodIntegrationSource  = "elements"
	paymentMethodIntegrationSubtype = "payment-element"
	confirmIntegrationSource        = "checkout"
	deferredIntentType              = "deferred_intent"
)

var canvasFingerprints = []string{
	"0100100101111111101111101111111001110010110111110111111",
	"0100100101111111101111101111111001110010110111110111110",
	"0100100101111111101111101111111001110010110111110111101",
}

var audioFingerprints = []string{
	"d331ca493eb692cfcd19ae5db713ad4b",
	"a7c5f72e1b3d4e8f9c0d2a6b7e8f1c3d",
	"e4b8d6f2a0c3d5e7f9b1c3d5e7f9a0b2",
}

var screenProfiles = []screenProfile{
	{Width: 1920, Height: 1080, DPR: 1},
	{Width: 1536, Height: 864, DPR: 1.25},
	{Width: 2560, Height: 1440, DPR: 1},
	{Width: 1440, Height: 900, DPR: 1},
}

type HTTPClient interface {
	GetJSON(ctx context.Context, target string, headers map[string]string, out any) error
	PostForm(ctx context.Context, target string, headers map[string]string, values url.Values, out any) error
	PostRawJSON(ctx context.Context, target string, headers map[string]string, body string, contentType string, out any) error
}

type ResponseError struct {
	StatusCode int
	Body       string
}

func (e ResponseError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("status %d", e.StatusCode)
	}
	return fmt.Sprintf("status %d: %s", e.StatusCode, e.Body)
}

type Checkout struct {
	SessionID      string
	PublishableKey string
}

func (c Checkout) URL() string {
	return fmt.Sprintf("https://checkout.stripe.com/c/pay/%s#%s", c.SessionID, fixedHash)
}

type Billing struct {
	Name         string
	Email        string
	Country      string
	AddressLine1 string
	AddressCity  string
	AddressState string
	PostalCode   string
}

type Card struct {
	Number   string
	CVC      string
	ExpMonth string
	ExpYear  string
}

type Processor struct {
	client    HTTPClient
	checkout  Checkout
	currency  string
	userAgent string
}

type screenProfile struct {
	Width  int
	Height int
	DPR    float64
}

type fingerprint struct {
	GUID string `json:"guid"`
	MUID string `json:"muid"`
	SID  string `json:"sid"`
}

type paymentMethodResponse struct {
	ID string `json:"id"`
}

type paymentPageInitResponse struct {
	EID             string `json:"eid"`
	InitChecksum    string `json:"init_checksum"`
	ConfigID        string `json:"config_id"`
	URL             string `json:"url"`
	StripeHostedURL string `json:"stripe_hosted_url"`
	LineItems       []struct {
		Amount int `json:"amount"`
	} `json:"line_items"`
	TotalSummary struct {
		Due int `json:"due"`
	} `json:"total_summary"`
}

type elementsSessionResponse struct {
	SessionID string `json:"session_id"`
	ID        string `json:"id"`
	ConfigID  string `json:"config_id"`
}

type paymentPageConfirmResponse struct {
	Status        string `json:"status"`
	ClientSecret  string `json:"client_secret"`
	PaymentIntent struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"payment_intent"`
}

type paymentPagePollResponse struct {
	State               string `json:"state"`
	PaymentObjectStatus string `json:"payment_object_status"`
	Mode                string `json:"mode"`
	ReturnURL           string `json:"return_url"`
}

type checkoutContext struct {
	StripeVersion           string
	StripeJSID              string
	ElementsSessionID       string
	ElementsSessionConfigID string
	ClientSessionID         string
	ReturnURL               string
	CheckoutConfigID        string
	ExpectedAmount          int
	TimeOnPageMillis        int
}

func shortLogID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return value
	}
	return value[:4] + "..." + value[len(value)-4:]
}
