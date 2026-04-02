package stripe

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

func NewProcessor(client HTTPClient, checkout Checkout, accessToken, currency, userAgent string) *Processor {
	return &Processor{
		client:      client,
		checkout:    checkout,
		accessToken: accessToken,
		currency:    strings.TrimSpace(currency),
		userAgent:   strings.TrimSpace(userAgent),
	}
}

func (p *Processor) Pay(ctx context.Context, billing Billing, card Card) error {
	slog.Info(
		"stripe payment flow started",
		"checkout_session", shortLogID(p.checkout.SessionID),
		"currency", p.currency,
		"billing_country", billing.Country,
	)
	fp, err := p.fetchFingerprint(ctx)
	if err != nil {
		return fmt.Errorf("fetch stripe fingerprint: %w", err)
	}
	slog.Info("stripe fingerprint ready", "checkout_session", shortLogID(p.checkout.SessionID), "has_guid", fp.GUID != "")

	initResp, checkoutCtx, err := p.initPaymentPage(ctx)
	if err != nil {
		return fmt.Errorf("init stripe payment page: %w", err)
	}
	slog.Info(
		"stripe payment page initialized",
		"checkout_session", shortLogID(p.checkout.SessionID),
		"stripe_version", checkoutCtx.StripeVersion,
		"expected_amount", checkoutCtx.ExpectedAmount,
	)
	if err := p.preparePaymentPage(ctx, billing, checkoutCtx); err != nil {
		return fmt.Errorf("prepare stripe payment page: %w", err)
	}
	slog.Info(
		"stripe payment page prepared",
		"checkout_session", shortLogID(p.checkout.SessionID),
		"elements_session", shortLogID(checkoutCtx.ElementsSessionID),
	)

	paymentMethod, err := p.createPaymentMethod(ctx, billing, card, fp, checkoutCtx)
	if err != nil {
		return fmt.Errorf("create payment method: %w", err)
	}
	slog.Info(
		"stripe payment method created",
		"checkout_session", shortLogID(p.checkout.SessionID),
		"payment_method", shortLogID(paymentMethod),
	)

	result, err := p.confirmPayment(ctx, paymentMethod, fp, initResp, checkoutCtx)
	if err != nil {
		return fmt.Errorf("confirm payment: %w", err)
	}
	slog.Info(
		"stripe payment confirm responded",
		"checkout_session", shortLogID(p.checkout.SessionID),
		"status", result.Status,
		"payment_status", result.PaymentIntent.Status,
	)
	if paymentSucceeded(result) {
		slog.Info("stripe payment completed", "checkout_session", shortLogID(p.checkout.SessionID), "mode", "confirm")
		return nil
	}

	if result.Status == "open" {
		slog.Info("stripe payment pending approving", "checkout_session", shortLogID((p.checkout.SessionID)))
		if err := p.approvePayment(ctx); err != nil {
			slog.Warn(
				"stripe payment approval failed",
				"checkout_session", shortLogID(p.checkout.SessionID),
			)
			return fmt.Errorf("approve payment: %w", err)
		}
	}

	slog.Info("stripe payment polling started", "checkout_session", shortLogID(p.checkout.SessionID))
	pollResult, err := p.pollPaymentResult(ctx, checkoutCtx)
	if err != nil {
		return fmt.Errorf("poll payment result: %w", err)
	}
	if paymentPollSucceeded(pollResult) {
		slog.Info(
			"stripe payment completed",
			"checkout_session", shortLogID(p.checkout.SessionID),
			"mode", "poll",
			"state", pollResult.State,
			"payment_status", pollResult.PaymentObjectStatus,
		)
		return nil
	}
	slog.Warn(
		"stripe payment ended with unsuccessful final state",
		"checkout_session", shortLogID(p.checkout.SessionID),
		"confirm_status", result.Status,
		"confirm_payment_status", result.PaymentIntent.Status,
		"poll_state", pollResult.State,
		"poll_payment_status", pollResult.PaymentObjectStatus,
	)
	return fmt.Errorf(
		"payment returned status %q / %q, poll state %q / %q",
		result.Status,
		result.PaymentIntent.Status,
		pollResult.State,
		pollResult.PaymentObjectStatus,
	)
}
