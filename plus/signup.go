package plus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"
)

func (r *registrationFlow) initChatGPTSignup(ctx context.Context) error {
	if err := r.client.GetOK(ctx, chatgptURL, nil); err != nil {
		return fmt.Errorf("open chatgpt home: %w", err)
	}

	chatURL, _ := url.Parse(chatgptURL)
	did := cookieValue(r.client, chatURL, "oai-did")
	if did == "" {
		return errors.New("oai-did cookie missing on chatgpt.com")
	}
	r.oaiDID = did

	csrfCookie := cookieValue(r.client, chatURL, "__Host-next-auth.csrf-token")
	csrfToken := extractCSRFToken(csrfCookie)
	if csrfToken == "" {
		return errors.New("csrf token cookie missing on chatgpt.com")
	}

	loginURL := chatgptURL + "/auth/login?openaicom-did=" + url.QueryEscape(r.oaiDID)
	if err := r.client.GetOK(ctx, loginURL, nil); err != nil {
		return fmt.Errorf("open chatgpt login page: %w", err)
	}

	authSessionID, err := randomHexString(16)
	if err != nil {
		return fmt.Errorf("generate auth session id: %w", err)
	}
	signinQuery := url.Values{
		"prompt":                  {"login"},
		"ext-oai-did":             {r.oaiDID},
		"auth_session_logging_id": {authSessionID},
		"screen_hint":             {"login_or_signup"},
		"login_hint":              {r.email},
	}
	signinURL := chatgptURL + "/api/auth/signin/openai?" + signinQuery.Encode()
	form := url.Values{
		"callbackUrl": {chatgptURL + "/"},
		"csrfToken":   {csrfToken},
		"json":        {"true"},
	}
	headers := map[string]string{
		"Origin":  chatgptURL,
		"Referer": loginURL,
		"Accept":  "application/json",
	}
	var payload struct {
		URL string `json:"url"`
	}
	err = r.client.PostForm(ctx, signinURL, headers, form, &payload)
	if err != nil {
		return fmt.Errorf("request chatgpt openai signin url: %w", err)
	}
	if payload.URL == "" || !strings.Contains(payload.URL, "auth.openai.com") {
		return errors.New("chatgpt signin response missing auth.openai.com url")
	}

	if err := r.client.GetOK(ctx, payload.URL, nil); err != nil {
		return fmt.Errorf("open auth authorize url: %w", err)
	}

	authURL, _ := url.Parse(authOriginURL)
	if did := cookieValue(r.client, authURL, "oai-did"); did != "" {
		r.oaiDID = did
	}
	return nil
}

func extractCSRFToken(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	decoded, err := url.QueryUnescape(raw)
	if err == nil {
		raw = decoded
	}

	parts := strings.SplitN(raw, "|", 2)
	return strings.TrimSpace(parts[0])
}

func (r *registrationFlow) registerPassword(ctx context.Context) error {
	body := map[string]string{
		"username": r.email,
		"password": r.password,
	}
	headers := map[string]string{
		"Accept":  "application/json",
		"Origin":  authOriginURL,
		"Referer": authOriginURL + "/create-account/password",
	}
	if err := r.client.PostJSONOK(ctx, authOriginURL+"/api/accounts/user/register", headers, body); err != nil {
		return fmt.Errorf("post password register: %w", err)
	}
	return nil
}

func (r *registrationFlow) sendVerificationEmailRegistered(ctx context.Context) error {
	headers := map[string]string{
		"Referer": authOriginURL + "/create-account/password",
		"Accept":  "application/json",
	}
	if err := r.client.GetOK(ctx, authOriginURL+"/api/accounts/email-otp/send", headers); err != nil {
		return fmt.Errorf("request registered otp send: %w", err)
	}
	return nil
}

func (r *registrationFlow) validateOTP(ctx context.Context, code string) error {
	body := map[string]string{"code": code}
	headers := map[string]string{
		"Referer": authOriginURL + "/email-verification",
		"Accept":  "application/json",
	}
	if err := r.client.PostJSONOK(ctx, authOriginURL+"/api/accounts/email-otp/validate", headers, body); err != nil {
		return fmt.Errorf("post otp validate: %w", err)
	}
	return nil
}

func (r *registrationFlow) validateCodexLoginOTP(ctx context.Context, code string) error {
	body := map[string]string{"code": code}
	headers := map[string]string{
		"Origin": authOriginURL,
		"Accept": "application/json",
	}
	if err := r.client.PostJSONOK(ctx, authOriginURL+"/api/accounts/email-otp/validate", headers, body); err != nil {
		return fmt.Errorf("post codex login otp validate: %w", err)
	}
	return nil
}

func (r *registrationFlow) createAccount(ctx context.Context) (ChatGPTSession, error) {
	body := map[string]string{"name": r.name, "birthdate": r.birthdate}
	headers := map[string]string{
		"Referer": authOriginURL + "/about-you",
		"Accept":  "application/json",
	}
	var out struct {
		ContinueURL string `json:"continue_url"`
	}
	ok, err := r.client.PostJSONOptional(ctx, authOriginURL+"/api/accounts/create_account", headers, body, &out)
	if err != nil {
		return ChatGPTSession{}, fmt.Errorf("post create account: %w", err)
	}
	if ok && out.ContinueURL != "" {
		if err := r.client.GetOK(ctx, out.ContinueURL, nil); err != nil {
			return ChatGPTSession{}, fmt.Errorf("open continue url: %w", err)
		}
	}

	session, err := r.fetchSession(ctx)
	if err != nil {
		return ChatGPTSession{}, fmt.Errorf("fetch auth session: %w", err)
	}
	return session, nil
}

func (r *registrationFlow) waitOTP(ctx context.Context, previousEmail emailCursor) (string, error) {
	waitCtx, cancel := context.WithTimeout(ctx, r.cfg.OTPWait)
	defer cancel()

	ticker := time.NewTicker(r.cfg.OTPPoll)
	defer ticker.Stop()

	var lastErr error
	slog.Info("waiting for otp", "email", r.email, "timeout", r.cfg.OTPWait.String(), "poll", r.cfg.OTPPoll.String(), "has_previous_email", previousEmail.EmailID != 0 || previousEmail.HasCreatedAt)

	for {
		content, found, err := latestAfterWithContext(waitCtx, r.email, previousEmail)
		if err != nil {
			slog.Warn("otp poll failed", "email", r.email, "err", err)
			lastErr = err
		} else if !found {
			slog.Info("otp email not arrived yet", "email", r.email)
			lastErr = errors.New("otp email not arrived yet")
		} else {
			otp := extractOTP(content)
			if otp != "" {
				return otp, nil
			}
			slog.Info("otp not found in latest new email yet", "email", r.email)
			lastErr = errors.New("otp not found in latest new email")
		}

		select {
		case <-waitCtx.Done():
			if ctxErr := ctx.Err(); ctxErr != nil {
				return "", fmt.Errorf("wait otp canceled: %w", ctxErr)
			}
			if lastErr == nil {
				lastErr = errors.New("otp wait timeout")
			}
			return "", fmt.Errorf("waiting otp: %w", lastErr)
		case <-ticker.C:
		}
	}
}
