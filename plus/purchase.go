package plus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	http "github.com/bogdanfinn/fhttp"
)

const (
	revenueCatReceiptsURL = "https://api.revenuecat.com/v1/receipts"
	revenueCatUserAgent   = "Dalvik/2.1.0 (Linux; U; Android 16; 2211133C Build/BP2A.250605.031.A3)"

	revenueCatPlusProductID  = "oai.chatgpt.plus"
	revenueCatPlusBasePlanID = "oai-chatgpt-plus-1m-1999"
	revenueCatPlusOfferID    = "plus-1-month-free-trial"
)

type purchaseHTTPClient interface {
	Post(ctx context.Context, target string, headers map[string]string, body io.Reader, contentType string) (*http.Response, error)
}

type Purchase struct {
	client  purchaseHTTPClient
	session ChatGPTSession
	cfg     PurchaseConfig
	lease   *PurchaseTokenLease
}

type PurchaseConfig struct {
	Enabled             bool
	RevenueCatBearerKey string
	Store               *PurchaseTokenStore
}

type revenueCatReceiptRequest struct {
	FetchToken         string                        `json:"fetch_token"`
	ProductIDs         []string                      `json:"product_ids"`
	PlatformProductIDs []revenueCatPlatformProductID `json:"platform_product_ids"`
	AppUserID          string                        `json:"app_user_id"`
}

type revenueCatPlatformProductID struct {
	ProductID  string `json:"product_id"`
	BasePlanID string `json:"base_plan_id"`
	OfferID    string `json:"offer_id"`
}

func NewPurchase(client purchaseHTTPClient, session ChatGPTSession, cfg PurchaseConfig, lease *PurchaseTokenLease) *Purchase {
	return &Purchase{
		client:  client,
		session: session,
		cfg:     cfg,
		lease:   lease,
	}
}

func (p *Purchase) Checkout(ctx context.Context) error {
	if !p.cfg.Enabled {
		return nil
	}
	if strings.TrimSpace(p.cfg.RevenueCatBearerKey) == "" {
		return errors.New("purchase revenuecat bearer key is empty")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	body, err := json.Marshal(revenueCatReceiptRequest{
		FetchToken: p.lease.FetchToken(),
		ProductIDs: []string{revenueCatPlusProductID},
		PlatformProductIDs: []revenueCatPlatformProductID{
			{
				ProductID:  revenueCatPlusProductID,
				BasePlanID: revenueCatPlusBasePlanID,
				OfferID:    revenueCatPlusOfferID,
			},
		},
		AppUserID: p.session.Account.ID,
	})
	if err != nil {
		return fmt.Errorf("encode revenuecat purchase request: %w", err)
	}

	slog.InfoContext(
		ctx,
		"purchase request started",
		"account_id", p.session.Account.ID,
		"purchase_token_id", p.lease.ID(),
	)

	resp, err := p.client.Post(ctx, revenueCatReceiptsURL, map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", p.cfg.RevenueCatBearerKey),
		"User-Agent":    revenueCatUserAgent,
		"X-Platform":    "android",
	}, bytes.NewReader(body), "application/json")
	if err != nil {
		markErr := p.markLeaseDead(ctx, nil, err.Error())
		if markErr != nil {
			return errors.Join(
				fmt.Errorf("post revenuecat receipt: %w", err),
				fmt.Errorf("mark purchase token dead: %w", markErr),
			)
		}
		return fmt.Errorf("post revenuecat receipt: %w", err)
	}
	defer resp.Body.Close()

	responseBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		markErr := p.markLeaseDead(ctx, &resp.StatusCode, readErr.Error())
		if markErr != nil {
			return errors.Join(
				fmt.Errorf("read revenuecat response body: %w", readErr),
				fmt.Errorf("mark purchase token dead: %w", markErr),
			)
		}
		return fmt.Errorf("read revenuecat response body: %w", readErr)
	}
	responseText := strings.TrimSpace(string(responseBody))

	switch {
	case resp.StatusCode == http.StatusOK:
		if err := p.markLeaseConsumed(ctx, resp.StatusCode); err != nil {
			return fmt.Errorf("mark purchase token consumed: %w", err)
		}
		slog.InfoContext(
			ctx,
			"purchase request completed",
			"account_id", p.session.Account.ID,
			"purchase_token_id", p.lease.ID(),
			"status_code", resp.StatusCode,
		)
		return nil
	case resp.StatusCode >= http.StatusInternalServerError:
		markErr := p.markLeaseRetryable(ctx, resp.StatusCode, responseText)
		if markErr != nil {
			return errors.Join(
				responseError{StatusCode: resp.StatusCode, Body: responseText},
				fmt.Errorf("mark purchase token retryable: %w", markErr),
			)
		}
		return responseError{StatusCode: resp.StatusCode, Body: responseText}
	default:
		markErr := p.markLeaseDead(ctx, &resp.StatusCode, responseText)
		if markErr != nil {
			return errors.Join(
				responseError{StatusCode: resp.StatusCode, Body: responseText},
				fmt.Errorf("mark purchase token dead: %w", markErr),
			)
		}
		return responseError{StatusCode: resp.StatusCode, Body: responseText}
	}
}

func (p *Purchase) markLeaseConsumed(ctx context.Context, statusCode int) error {
	cleanupCtx, cancel := cleanupContext(ctx)
	defer cancel()
	return p.lease.MarkConsumed(cleanupCtx, p.session.Account.ID, statusCode)
}

func (p *Purchase) markLeaseRetryable(ctx context.Context, statusCode int, lastErr string) error {
	cleanupCtx, cancel := cleanupContext(ctx)
	defer cancel()
	return p.lease.MarkRetryable(cleanupCtx, p.session.Account.ID, statusCode, lastErr)
}

func (p *Purchase) markLeaseDead(ctx context.Context, statusCode *int, lastErr string) error {
	cleanupCtx, cancel := cleanupContext(ctx)
	defer cancel()
	return p.lease.MarkDead(cleanupCtx, p.session.Account.ID, statusCode, lastErr)
}
