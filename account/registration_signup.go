package account

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
)

func (r *registrationFlow) initChatGPTSignup(ctx context.Context) error {
	resp, err := doRequest(ctx, r.client, http.MethodGet, chatgptBaseURL, nil, nil, "")
	if err != nil {
		return fmt.Errorf("open chatgpt home: %w", err)
	}
	resp.Body.Close()

	chatURL, _ := url.Parse(chatgptBaseURL)
	did := strings.TrimSpace(cookieValue(r.client, chatURL, "oai-did"))
	if did == "" {
		return errors.New("oai-did cookie missing on chatgpt.com")
	}
	r.oaiDID = did

	csrfCookie := strings.TrimSpace(cookieValue(r.client, chatURL, "__Host-next-auth.csrf-token"))
	csrfToken := extractCSRFToken(csrfCookie)
	if csrfToken == "" {
		return errors.New("csrf token cookie missing on chatgpt.com")
	}

	loginURL := chatgptBaseURL + "/auth/login?openaicom-did=" + url.QueryEscape(r.oaiDID)
	resp, err = doRequest(ctx, r.client, http.MethodGet, loginURL, nil, nil, "")
	if err != nil {
		return fmt.Errorf("open chatgpt login page: %w", err)
	}
	resp.Body.Close()

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
	signinURL := chatgptBaseURL + "/api/auth/signin/openai?" + signinQuery.Encode()
	form := url.Values{
		"callbackUrl": {chatgptBaseURL + "/"},
		"csrfToken":   {csrfToken},
		"json":        {"true"},
	}
	headers := map[string]string{
		"Origin":  chatgptBaseURL,
		"Referer": loginURL,
		"Accept":  "application/json",
	}
	resp, err = doRequest(
		ctx,
		r.client,
		http.MethodPost,
		signinURL,
		headers,
		strings.NewReader(form.Encode()),
		"application/x-www-form-urlencoded",
	)
	if err != nil {
		return fmt.Errorf("request chatgpt openai signin url: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("chatgpt signin status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var payload struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return fmt.Errorf("decode chatgpt signin response: %w", err)
	}
	payload.URL = strings.TrimSpace(payload.URL)
	if payload.URL == "" || !strings.Contains(payload.URL, "auth.openai.com") {
		return errors.New("chatgpt signin response missing auth.openai.com url")
	}

	resp2, err := doRequest(ctx, r.client, http.MethodGet, payload.URL, nil, nil, "")
	if err != nil {
		return fmt.Errorf("open auth authorize url: %w", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp2.Body)
		return fmt.Errorf("auth authorize status %d: %s", resp2.StatusCode, strings.TrimSpace(string(raw)))
	}

	authURL, _ := url.Parse(authBaseURL)
	if did := strings.TrimSpace(cookieValue(r.client, authURL, "oai-did")); did != "" {
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

func (r *registrationFlow) sendVerificationEmail(ctx context.Context) error {
	if r.passwordSet {
		err := r.sendVerificationEmailRegistered(ctx)
		if err == nil {
			return nil
		}
		slog.Warn("registered otp endpoint failed, trying passwordless otp endpoint", "email", r.email, "err", err)
		return r.sendVerificationEmailPasswordless(ctx)
	}

	err := r.sendVerificationEmailPasswordless(ctx)
	if err == nil {
		return nil
	}
	slog.Warn("passwordless otp endpoint failed, trying registered otp endpoint", "email", r.email, "err", err)
	return r.sendVerificationEmailRegistered(ctx)
}

func (r *registrationFlow) registerPassword(ctx context.Context) error {
	body := map[string]string{
		"username": r.email,
		"password": r.password,
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode register password request body: %w", err)
	}
	headers := map[string]string{
		"Accept":       "application/json",
		"Content-Type": "application/json",
		"Origin":       authBaseURL,
		"Referer":      authBaseURL + "/create-account/password",
	}
	resp, err := doJSONRequest(ctx, r.client, http.MethodPost, authBaseURL+"/api/accounts/user/register", headers, bodyJSON)
	if err != nil {
		return fmt.Errorf("post password register: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		r.passwordSet = true
		return nil
	}
	raw, _ := io.ReadAll(resp.Body)
	msg := strings.TrimSpace(string(raw))
	return fmt.Errorf("password register status %d: %s", resp.StatusCode, msg)
}

func (r *registrationFlow) sendVerificationEmailRegistered(ctx context.Context) error {
	headers := map[string]string{
		"Referer": authBaseURL + "/create-account/password",
		"Accept":  "application/json",
	}
	resp, err := doRequest(ctx, r.client, http.MethodGet, authBaseURL+"/api/accounts/email-otp/send", headers, nil, "")
	if err != nil {
		return fmt.Errorf("request registered otp send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("registered otp send status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func (r *registrationFlow) sendVerificationEmailPasswordless(ctx context.Context) error {
	headers := map[string]string{
		"Referer":      authBaseURL + "/create-account/password",
		"Accept":       "application/json",
		"Content-Type": "application/json",
	}
	resp, err := doRequest(ctx, r.client, http.MethodPost, authBaseURL+"/api/accounts/passwordless/send-otp", headers, nil, "")
	if err != nil {
		return fmt.Errorf("request passwordless otp send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("passwordless otp send status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func (r *registrationFlow) validateOTP(ctx context.Context, code string) error {
	body := map[string]string{"code": code}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode otp validate request body: %w", err)
	}
	headers := map[string]string{
		"Content-Type": "application/json",
		"Referer":      authBaseURL + "/email-verification",
		"Accept":       "application/json",
	}
	resp, err := doJSONRequest(ctx, r.client, http.MethodPost, authBaseURL+"/api/accounts/email-otp/validate", headers, bodyJSON)
	if err != nil {
		return fmt.Errorf("post otp validate: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("validate otp status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func (r *registrationFlow) validateCodexLoginOTP(ctx context.Context, code string) error {
	body := map[string]string{"code": code}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode codex login otp validate request body: %w", err)
	}
	headers := map[string]string{
		"Content-Type": "application/json",
		"Origin":       authBaseURL,
		"Accept":       "application/json",
	}
	resp, err := doJSONRequest(ctx, r.client, http.MethodPost, authBaseURL+"/api/accounts/email-otp/validate", headers, bodyJSON)
	if err != nil {
		return fmt.Errorf("post codex login otp validate: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("codex login otp validate status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func (r *registrationFlow) createAccount(ctx context.Context) error {
	body := map[string]string{"name": r.name, "birthdate": r.birthdate}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode create account request body: %w", err)
	}
	headers := map[string]string{
		"Content-Type": "application/json",
		"Referer":      authBaseURL + "/about-you",
		"Accept":       "application/json",
	}
	resp, err := doJSONRequest(ctx, r.client, http.MethodPost, authBaseURL+"/api/accounts/create_account", headers, bodyJSON)
	if err != nil {
		return fmt.Errorf("post create account: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create account status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read create account response: %w", err)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}

	var out struct {
		ContinueURL string `json:"continue_url"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("decode create account response: %w", err)
	}
	if out.ContinueURL == "" {
		return nil
	}

	resp2, err := doRequest(ctx, r.client, http.MethodGet, out.ContinueURL, nil, nil, "")
	if err != nil {
		return fmt.Errorf("open continue url: %w", err)
	}
	resp2.Body.Close()
	return nil
}

func (r *registrationFlow) waitOTP(ctx context.Context) (string, error) {
	waitCtx, cancel := context.WithTimeout(ctx, r.cfg.OTPWait)
	defer cancel()

	ticker := time.NewTicker(r.cfg.OTPPoll)
	defer ticker.Stop()

	var lastErr error
	slog.Info("waiting for otp", "email", r.email, "timeout", r.cfg.OTPWait.String(), "poll", r.cfg.OTPPoll.String())

	for {
		content, err := LatestWithContext(waitCtx, r.email)
		if err == nil {
			otp := extractOTP(content)
			if otp != "" {
				return otp, nil
			}
			slog.Info("otp not found in latest email yet", "email", r.email)
			lastErr = errors.New("otp not found in latest email")
		} else {
			slog.Warn("otp poll failed", "email", r.email, "err", err)
			lastErr = err
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
