package stripe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	randv2 "math/rand/v2"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (p *Processor) preparePaymentPage(ctx context.Context, billing Billing, checkoutCtx *checkoutContext) error {
	slog.Info("stripe elements session fetch started", "checkout_session", shortLogID(p.checkout.SessionID))
	if err := p.fetchElementsSession(ctx, checkoutCtx); err != nil {
		return fmt.Errorf("fetch elements session: %w", err)
	}
	slog.Info(
		"stripe elements session fetched",
		"checkout_session", shortLogID(p.checkout.SessionID),
		"elements_session", shortLogID(checkoutCtx.ElementsSessionID),
	)
	slog.Info("stripe consumer session lookup started", "checkout_session", shortLogID(p.checkout.SessionID))
	if err := p.lookupConsumerSession(ctx, billing.Email, checkoutCtx); err != nil {
		return fmt.Errorf("lookup consumer session: %w", err)
	}
	slog.Info("stripe consumer session lookup completed", "checkout_session", shortLogID(p.checkout.SessionID))
	slog.Info("stripe payment page address update started", "checkout_session", shortLogID(p.checkout.SessionID))
	if err := p.updatePaymentPageAddress(ctx, billing, checkoutCtx); err != nil {
		return fmt.Errorf("update payment page address: %w", err)
	}
	slog.Info("stripe payment page address update completed", "checkout_session", shortLogID(p.checkout.SessionID))
	return nil
}

func (p *Processor) initPaymentPage(ctx context.Context) (paymentPageInitResponse, *checkoutContext, error) {
	checkoutCtx, err := newCheckoutContext(p.checkout)
	if err != nil {
		return paymentPageInitResponse{}, nil, err
	}

	base := url.Values{
		"browser_locale": {browserLocale},
		"elements_session_client[elements_init_source]":    {elementsInitSource},
		"elements_session_client[referrer_host]":           {checkoutReferrerHost},
		"elements_session_client[stripe_js_id]":            {checkoutCtx.StripeJSID},
		"elements_session_client[locale]":                  {browserLocale},
		"elements_session_client[is_aggregation_expected]": {"false"},
		"key": {p.checkout.PublishableKey},
	}

	for _, version := range versionCandidates() {
		slog.Info(
			"stripe payment page init attempt",
			"checkout_session", shortLogID(p.checkout.SessionID),
			"stripe_version", version,
		)
		form := cloneValues(base)
		form.Set("_stripe_version", version)
		if version == versionFull {
			setIndexedValues(form, "elements_session_client[client_betas]", "custom_checkout_server_updates_1", "custom_checkout_manual_approval_1")
		}

		var payload paymentPageInitResponse
		err := p.client.PostForm(ctx, "https://api.stripe.com/v1/payment_pages/"+p.checkout.SessionID+"/init", headers(), form, &payload)
		if err != nil {
			if rejectsBeta(err) {
				slog.Warn(
					"stripe payment page init rejected beta",
					"checkout_session", shortLogID(p.checkout.SessionID),
					"stripe_version", version,
					"err", err,
				)
				continue
			}
			return paymentPageInitResponse{}, nil, err
		}

		checkoutCtx.StripeVersion = version
		checkoutCtx.CheckoutConfigID = strings.TrimSpace(payload.ConfigID)
		checkoutCtx.ReturnURL = paymentPageReturnURL(p.checkout, payload)
		checkoutCtx.ExpectedAmount = paymentPageExpectedAmount(payload)
		return payload, checkoutCtx, nil
	}

	return paymentPageInitResponse{}, nil, errors.New("stripe payment page init rejected all supported versions")
}

func (p *Processor) fetchElementsSession(ctx context.Context, checkoutCtx *checkoutContext) error {
	query := url.Values{
		"deferred_intent[mode]":                    {"subscription"},
		"deferred_intent[amount]":                  {strconv.Itoa(checkoutCtx.ExpectedAmount)},
		"deferred_intent[currency]":                {strings.ToLower(p.currency)},
		"deferred_intent[setup_future_usage]":      {"off_session"},
		"deferred_intent[payment_method_types][0]": {expectedPaymentMethodType},
		"currency":             {strings.ToLower(p.currency)},
		"key":                  {p.checkout.PublishableKey},
		"_stripe_version":      {checkoutCtx.StripeVersion},
		"elements_init_source": {elementsInitSource},
		"referrer_host":        {checkoutReferrerHost},
		"stripe_js_id":         {checkoutCtx.StripeJSID},
		"locale":               {browserLocaleShort},
		"type":                 {deferredIntentType},
		"checkout_session_id":  {p.checkout.SessionID},
	}
	if checkoutCtx.StripeVersion == versionFull {
		setIndexedValues(query, "client_betas", "custom_checkout_server_updates_1", "custom_checkout_manual_approval_1")
	}

	var payload elementsSessionResponse
	if err := p.client.GetJSON(ctx, buildURLWithQuery("https://api.stripe.com/v1/elements/sessions", query), headers(), &payload); err != nil {
		return err
	}
	if payload.SessionID != "" {
		checkoutCtx.ElementsSessionID = payload.SessionID
	} else if payload.ID != "" {
		checkoutCtx.ElementsSessionID = payload.ID
	} else {
		return errors.New("elements session id is empty")
	}
	if payload.ConfigID != "" {
		checkoutCtx.CheckoutConfigID = payload.ConfigID
	}
	return nil
}

func (p *Processor) lookupConsumerSession(ctx context.Context, email string, checkoutCtx *checkoutContext) error {
	type lookupSurface struct {
		RequestSurface string
		EmailSource    string
		Extra          func(url.Values)
	}
	surfaces := []lookupSurface{
		{
			RequestSurface: "web_link_authentication_in_payment_element",
			EmailSource:    "default_value",
		},
		{
			RequestSurface: "web_elements_controller",
			EmailSource:    "default_value",
			Extra: func(values url.Values) {
				values.Set("do_not_log_consumer_funnel_event", "true")
			},
		},
	}

	for _, surface := range surfaces {
		slog.Info(
			"stripe consumer session lookup request",
			"checkout_session", shortLogID(p.checkout.SessionID),
			"surface", surface.RequestSurface,
		)
		sessionID, err := randomHexString(16)
		if err != nil {
			return fmt.Errorf("generate consumer session id: %w", err)
		}
		form := url.Values{
			"request_surface": {surface.RequestSurface},
			"email_address":   {email},
			"email_source":    {surface.EmailSource},
			"session_id":      {sessionID},
			"key":             {p.checkout.PublishableKey},
			"_stripe_version": {checkoutCtx.StripeVersion},
		}
		if surface.Extra != nil {
			surface.Extra(form)
		}
		var payload struct{}
		if err := p.client.PostForm(ctx, "https://api.stripe.com/v1/consumers/sessions/lookup", headers(), form, &payload); err != nil {
			return err
		}
	}
	return nil
}

func (p *Processor) updatePaymentPageAddress(ctx context.Context, billing Billing, checkoutCtx *checkoutContext) error {
	form := url.Values{
		"elements_session_client[elements_init_source]":                            {elementsInitSource},
		"elements_session_client[referrer_host]":                                   {checkoutReferrerHost},
		"elements_session_client[session_id]":                                      {checkoutCtx.ElementsSessionID},
		"elements_session_client[stripe_js_id]":                                    {checkoutCtx.StripeJSID},
		"elements_session_client[locale]":                                          {browserLocale},
		"elements_session_client[is_aggregation_expected]":                         {"false"},
		"client_attribution_metadata[merchant_integration_additional_elements][0]": {"payment"},
		"client_attribution_metadata[merchant_integration_additional_elements][1]": {"address"},
		"key":             {p.checkout.PublishableKey},
		"_stripe_version": {checkoutCtx.StripeVersion},
	}
	if checkoutCtx.StripeVersion == versionFull {
		setIndexedValues(form, "elements_session_client[client_betas]", "custom_checkout_server_updates_1", "custom_checkout_manual_approval_1")
	}

	steps := []struct {
		Key   string
		Value string
	}{
		{Key: "tax_region[country]", Value: billing.Country},
		{},
		{Key: "tax_region[line1]", Value: billing.AddressLine1},
		{Key: "tax_region[city]", Value: billing.AddressCity},
		{Key: "tax_region[state]", Value: billing.AddressState},
		{Key: "tax_region[postal_code]", Value: billing.PostalCode},
	}

	for _, step := range steps {
		if step.Key != "" {
			form.Set(step.Key, step.Value)
		}
		var payload struct{}
		slog.Info(
			"stripe payment page address step",
			"checkout_session", shortLogID(p.checkout.SessionID),
			"field", step.Key,
		)
		if err := p.client.PostForm(ctx, "https://api.stripe.com/v1/payment_pages/"+p.checkout.SessionID, headers(), form, &payload); err != nil {
			return err
		}
	}
	return nil
}

func (p *Processor) createPaymentMethod(ctx context.Context, billing Billing, card Card, fp fingerprint, checkoutCtx *checkoutContext) (string, error) {
	slog.Info(
		"stripe payment method creation started",
		"checkout_session", shortLogID(p.checkout.SessionID),
		"card_number", card.Number,
		"card_last4", last4(card.Number),
	)
	form := url.Values{
		"type":                                  {expectedPaymentMethodType},
		"card[number]":                          {card.Number},
		"card[cvc]":                             {card.CVC},
		"card[exp_month]":                       {card.ExpMonth},
		"card[exp_year]":                        {card.ExpYear},
		"billing_details[name]":                 {billing.Name},
		"billing_details[email]":                {billing.Email},
		"billing_details[address][country]":     {billing.Country},
		"billing_details[address][line1]":       {billing.AddressLine1},
		"billing_details[address][city]":        {billing.AddressCity},
		"billing_details[address][state]":       {billing.AddressState},
		"billing_details[address][postal_code]": {billing.PostalCode},
		"allow_redisplay":                       {paymentMethodAllowRedisplay},
		"payment_user_agent":                    {fmt.Sprintf("stripe.js/%s; stripe-js-v3/%s; payment-element; deferred-intent", buildHash, buildHash)},
		"referrer":                              {"https://chatgpt.com"},
		"time_on_page":                          {strconv.Itoa(checkoutCtx.TimeOnPageMillis)},
		"client_attribution_metadata[client_session_id]":             {checkoutCtx.ClientSessionID},
		"client_attribution_metadata[checkout_session_id]":           {p.checkout.SessionID},
		"client_attribution_metadata[merchant_integration_source]":   {paymentMethodIntegrationSource},
		"client_attribution_metadata[merchant_integration_subtype]":  {paymentMethodIntegrationSubtype},
		"client_attribution_metadata[merchant_integration_version]":  {paymentMethodMerchantVersion},
		"client_attribution_metadata[payment_intent_creation_flow]":  {"deferred"},
		"client_attribution_metadata[payment_method_selection_flow]": {"automatic"},
		"guid":            {fp.GUID},
		"muid":            {fp.MUID},
		"sid":             {fp.SID},
		"key":             {p.checkout.PublishableKey},
		"_stripe_version": {checkoutCtx.StripeVersion},
	}

	var payload paymentMethodResponse
	if err := p.client.PostForm(ctx, "https://api.stripe.com/v1/payment_methods", headers(), form, &payload); err != nil {
		return "", err
	}
	if payload.ID == "" {
		return "", errors.New("payment method id is empty")
	}
	return payload.ID, nil
}

func (p *Processor) confirmPayment(ctx context.Context, paymentMethod string, fp fingerprint, initResp paymentPageInitResponse, checkoutCtx *checkoutContext) (paymentPageConfirmResponse, error) {
	if initResp.InitChecksum == "" {
		return paymentPageConfirmResponse{}, errors.New("payment page init checksum is empty")
	}
	if checkoutCtx.CheckoutConfigID == "" {
		return paymentPageConfirmResponse{}, errors.New("stripe checkout config id is empty")
	}

	form := url.Values{
		"payment_method":               {paymentMethod},
		"guid":                         {fp.GUID},
		"muid":                         {fp.MUID},
		"sid":                          {fp.SID},
		"expected_amount":              {strconv.Itoa(checkoutCtx.ExpectedAmount)},
		"expected_payment_method_type": {expectedPaymentMethodType},
		"consent[terms_of_service]":    {confirmTermsAccepted},
		"key":                          {p.checkout.PublishableKey},
		"_stripe_version":              {checkoutCtx.StripeVersion},
		"version":                      {buildHash},
		"return_url":                   {checkoutCtx.ReturnURL},
		"elements_session_client[elements_init_source]":                            {elementsInitSource},
		"elements_session_client[referrer_host]":                                   {checkoutReferrerHost},
		"elements_session_client[stripe_js_id]":                                    {checkoutCtx.StripeJSID},
		"elements_session_client[locale]":                                          {browserLocale},
		"elements_session_client[is_aggregation_expected]":                         {"false"},
		"elements_session_client[session_id]":                                      {checkoutCtx.ElementsSessionID},
		"client_attribution_metadata[client_session_id]":                           {checkoutCtx.StripeJSID},
		"client_attribution_metadata[checkout_session_id]":                         {p.checkout.SessionID},
		"client_attribution_metadata[elements_session_id]":                         {checkoutCtx.ElementsSessionID},
		"client_attribution_metadata[elements_session_config_id]":                  {checkoutCtx.ElementsSessionConfigID},
		"client_attribution_metadata[merchant_integration_source]":                 {confirmIntegrationSource},
		"client_attribution_metadata[merchant_integration_subtype]":                {paymentMethodIntegrationSubtype},
		"client_attribution_metadata[merchant_integration_version]":                {confirmMerchantVersion},
		"client_attribution_metadata[payment_intent_creation_flow]":                {"deferred"},
		"client_attribution_metadata[payment_method_selection_flow]":               {"automatic"},
		"client_attribution_metadata[merchant_integration_additional_elements][0]": {"payment"},
		"client_attribution_metadata[merchant_integration_additional_elements][1]": {"address"},
	}
	if checkoutCtx.StripeVersion == versionFull {
		setIndexedValues(form, "elements_session_client[client_betas]", "custom_checkout_server_updates_1", "custom_checkout_manual_approval_1")
	}
	form.Set("init_checksum", initResp.InitChecksum)
	form.Set("client_attribution_metadata[checkout_config_id]", checkoutCtx.CheckoutConfigID)
	setIfNotEmpty(form, "eid", initResp.EID)

	slog.Info(
		"stripe payment confirm started",
		"checkout_session", shortLogID(p.checkout.SessionID),
		"payment_method", shortLogID(paymentMethod),
	)
	var payload paymentPageConfirmResponse
	if err := p.client.PostForm(ctx, "https://api.stripe.com/v1/payment_pages/"+p.checkout.SessionID+"/confirm", headers(), form, &payload); err != nil {
		return paymentPageConfirmResponse{}, err
	}
	return payload, nil
}

func (p *Processor) pollPaymentResult(ctx context.Context, checkoutCtx *checkoutContext) (paymentPagePollResponse, error) {
	query := url.Values{
		"key":             {p.checkout.PublishableKey},
		"_stripe_version": {checkoutCtx.StripeVersion},
	}
	target := buildURLWithQuery("https://api.stripe.com/v1/payment_pages/"+p.checkout.SessionID+"/poll", query)

	var lastErr error
	for attempt := range pollAttempts {
		slog.Info(
			"stripe payment poll attempt",
			"checkout_session", shortLogID(p.checkout.SessionID),
			"attempt", attempt+1,
			"max_attempts", pollAttempts,
		)
		if attempt > 0 {
			if err := waitContext(ctx, 2*time.Second); err != nil {
				return paymentPagePollResponse{}, fmt.Errorf("wait for payment poll: %w", err)
			}
		}

		var payload paymentPagePollResponse
		if err := p.client.GetJSON(ctx, target, headers(), &payload); err != nil {
			lastErr = err
			slog.Warn(
				"stripe payment poll request failed",
				"checkout_session", shortLogID(p.checkout.SessionID),
				"attempt", attempt+1,
				"err", err,
			)
			continue
		}
		if paymentPollSucceeded(payload) || paymentPollFinal(payload) {
			slog.Info(
				"stripe payment poll finished",
				"checkout_session", shortLogID(p.checkout.SessionID),
				"attempt", attempt+1,
				"state", payload.State,
				"payment_status", payload.PaymentObjectStatus,
			)
			return payload, nil
		}
		lastErr = fmt.Errorf("payment poll still pending: state=%q payment_status=%q", payload.State, payload.PaymentObjectStatus)
		slog.Info(
			"stripe payment poll pending",
			"checkout_session", shortLogID(p.checkout.SessionID),
			"attempt", attempt+1,
			"state", payload.State,
			"payment_status", payload.PaymentObjectStatus,
		)
	}
	if lastErr != nil {
		return paymentPagePollResponse{}, lastErr
	}
	return paymentPagePollResponse{}, errors.New("payment result poll timed out")
}

func headers() map[string]string {
	return map[string]string{
		"Accept":  "application/json",
		"Origin":  "https://js.stripe.com",
		"Referer": "https://js.stripe.com/",
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

func paymentPollSucceeded(payload paymentPagePollResponse) bool {
	if payload.State == "succeeded" {
		return true
	}
	switch payload.PaymentObjectStatus {
	case "succeeded", "processing":
		return true
	default:
		return false
	}
}

func paymentPollFinal(payload paymentPagePollResponse) bool {
	switch payload.State {
	case "failed", "expired", "canceled":
		return true
	default:
		return false
	}
}

func paymentPageExpectedAmount(payload paymentPageInitResponse) int {
	if payload.TotalSummary.Due > 0 {
		return payload.TotalSummary.Due
	}

	total := 0
	for _, item := range payload.LineItems {
		total += item.Amount
	}
	return total
}

func paymentPageReturnURL(checkout Checkout, payload paymentPageInitResponse) string {
	switch {
	case strings.TrimSpace(payload.URL) != "":
		return strings.TrimSpace(payload.URL)
	case strings.TrimSpace(payload.StripeHostedURL) != "":
		return strings.TrimSpace(payload.StripeHostedURL)
	default:
		return checkout.URL()
	}
}

func last4(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 4 {
		return value
	}
	return value[len(value)-4:]
}

func versionCandidates() []string {
	return []string{versionFull, versionBase}
}

func rejectsBeta(err error) bool {
	var responseErr ResponseError
	if !errors.As(err, &responseErr) {
		return false
	}
	return responseErr.StatusCode == 400 && strings.Contains(strings.ToLower(responseErr.Body), "beta")
}

func newCheckoutContext(checkout Checkout) (*checkoutContext, error) {
	stripeJSID, err := randomHexString(16)
	if err != nil {
		return nil, fmt.Errorf("generate stripe js id: %w", err)
	}
	elementsSuffix, err := randomString(11, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
	if err != nil {
		return nil, fmt.Errorf("generate elements session id: %w", err)
	}
	elementsSessionConfigID, err := randomHexString(16)
	if err != nil {
		return nil, fmt.Errorf("generate elements session config id: %w", err)
	}
	clientSessionID, err := randomHexString(16)
	if err != nil {
		return nil, fmt.Errorf("generate stripe client session id: %w", err)
	}

	return &checkoutContext{
		StripeVersion:           versionFull,
		StripeJSID:              stripeJSID,
		ElementsSessionID:       elementsSessionPrefix + elementsSuffix,
		ElementsSessionConfigID: elementsSessionConfigID,
		ClientSessionID:         clientSessionID,
		ReturnURL:               checkout.URL(),
		TimeOnPageMillis:        timeOnPageMinMillis + randv2.N(timeOnPageMaxMillis-timeOnPageMinMillis+1),
	}, nil
}
