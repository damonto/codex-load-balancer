package plus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

func (r *registrationFlow) execute(ctx context.Context) (RegisterResult, error) {
	result, err := r.prepareAccountProfile(ctx)
	if err != nil {
		return RegisterResult{}, err
	}

	session, err := r.completeRegistrationFlow(ctx)
	if err != nil {
		return RegisterResult{}, err
	}
	result.Session = session

	purchase := NewPurchase(r.client, session)
	if err := purchase.Checkout(ctx); err != nil {
		return RegisterResult{}, fmt.Errorf("checkout: %w", err)
	}

	token, accountID, err := r.completeCodexLoginFlow(ctx)
	if err != nil {
		return RegisterResult{}, err
	}
	result.AccountID = accountID
	result.Tokens = token
	slog.Info("codex oauth completed", "email", r.email, "account_id", accountID)

	path, err := r.saveCredentialFile(result)
	if err != nil {
		return RegisterResult{}, fmt.Errorf("save credential file: %w", err)
	}
	result.FilePath = path
	slog.Info("credential file saved", "email", r.email, "file", path)

	return result, nil
}

func (r *registrationFlow) prepareAccountProfile(ctx context.Context) (RegisterResult, error) {
	result := RegisterResult{Proxy: r.cfg.Proxy}

	email, err := GenerateWithContext(ctx)
	if err != nil {
		return RegisterResult{}, fmt.Errorf("generate email: %w", err)
	}
	result.Email = email
	r.email = email
	slog.Info("account registration started", "email", r.email, "proxy_host", proxyHostOnly(r.cfg.Proxy))

	r.password = generatePassword()
	r.name = generateName()
	r.birthdate = generateBirthdate()
	slog.Info("account profile generated", "email", r.email)
	return result, nil
}

func (r *registrationFlow) completeRegistrationFlow(ctx context.Context) (ChatGPTSession, error) {
	if err := r.initChatGPTSignup(ctx); err != nil {
		return ChatGPTSession{}, fmt.Errorf("init chatgpt signup flow: %w", err)
	}
	slog.Info("chatgpt signup session initialized", "email", r.email)

	if err := r.registerPassword(ctx); err != nil {
		return ChatGPTSession{}, fmt.Errorf("register account password: %w", err)
	}
	slog.Info("signup password submitted", "email", r.email)

	otpCursor, err := latestEmailCursorWithContext(ctx, r.email)
	if err != nil {
		return ChatGPTSession{}, fmt.Errorf("load registration otp cursor: %w", err)
	}

	if err := r.sendVerificationEmailRegistered(ctx); err != nil {
		return ChatGPTSession{}, fmt.Errorf("send registration otp email: %w", err)
	}
	slog.Info("otp email requested", "email", r.email)

	otp, err := r.waitOTP(ctx, otpCursor)
	if err != nil {
		return ChatGPTSession{}, fmt.Errorf("read registration otp: %w", err)
	}
	slog.Info("otp received", "email", r.email, "code_len", len(otp))

	if err := r.validateOTP(ctx, otp); err != nil {
		return ChatGPTSession{}, fmt.Errorf("validate registration otp: %w", err)
	}
	slog.Info("otp validated", "email", r.email)

	session, err := r.createAccount(ctx)
	if err != nil {
		return ChatGPTSession{}, fmt.Errorf("create account profile: %w", err)
	}
	slog.Info("account created", "email", r.email, "user_id", session.User.ID, "account_id", session.Account.ID, "plan_type", session.Account.PlanType)
	return session, nil
}

func (r *registrationFlow) completeCodexLoginFlow(ctx context.Context) (AuthTokens, string, error) {
	// Registration and Codex OAuth login are handled as two separate sessions in the reference flow.
	if err := r.resetAuthSession(); err != nil {
		return AuthTokens{}, "", fmt.Errorf("reset auth session for codex login: %w", err)
	}
	slog.Info("auth session reset for codex login", "email", r.email)

	if err := r.initPKCE(); err != nil {
		return AuthTokens{}, "", fmt.Errorf("init pkce: %w", err)
	}
	slog.Info("oauth pkce initialized", "email", r.email)

	landingURL, err := r.initCodexOAuth(ctx)
	if err != nil {
		return AuthTokens{}, "", fmt.Errorf("open codex oauth: %w", err)
	}
	slog.Info("codex oauth authorize page opened", "email", r.email, "landing_url", landingURL)

	otpCursor, err := latestEmailCursorWithContext(ctx, r.email)
	if err != nil {
		return AuthTokens{}, "", fmt.Errorf("load codex login otp cursor: %w", err)
	}

	pageType, err := r.submitEmailForCodex(ctx)
	if err != nil {
		return AuthTokens{}, "", fmt.Errorf("submit codex login email: %w", err)
	}
	slog.Info("codex login email submitted", "email", r.email, "page_type", pageType)

	pageType, err = r.resolvePageAfterEmailSubmit(ctx, pageType)
	if err != nil {
		return AuthTokens{}, "", err
	}

	if pageType == authPageTypeEmailOTPVerification {
		otpCode, err := r.waitOTP(ctx, otpCursor)
		if err != nil {
			return AuthTokens{}, "", fmt.Errorf("read codex login otp: %w", err)
		}
		if err := r.validateCodexLoginOTP(ctx, otpCode); err != nil {
			return AuthTokens{}, "", fmt.Errorf("validate codex login otp: %w", err)
		}
		slog.Info("codex login otp validated", "email", r.email)
	}

	token, accountID, err := r.finalizeCodexOAuth(ctx)
	if err != nil {
		return AuthTokens{}, "", fmt.Errorf("login codex: %w", err)
	}
	return token, accountID, nil
}

func (r *registrationFlow) resolvePageAfterEmailSubmit(ctx context.Context, pageType authPageType) (authPageType, error) {
	if pageType == authPageTypeLoginPassword {
		nextPageType, err := r.verifyPasswordForCodex(ctx)
		if err != nil && errors.Is(err, errLoginChallengeMissing) {
			slog.Warn("login challenge missing, resubmitting login email once", "email", r.email)
			if err := r.primeLoginStep(ctx); err != nil {
				return "", fmt.Errorf("prime login after missing challenge: %w", err)
			}
			pageType, err = r.submitEmailForCodex(ctx)
			if err != nil {
				return "", fmt.Errorf("resubmit codex login email: %w", err)
			}
			if pageType != authPageTypeLoginPassword {
				return "", fmt.Errorf("unexpected page type after codex login email resubmit: %s", pageType)
			}
			nextPageType, err = r.verifyPasswordForCodex(ctx)
		}
		if err != nil {
			return "", fmt.Errorf("verify codex login password: %w", err)
		}
		slog.Info("codex password verified", "email", r.email, "page_type", nextPageType)
		return nextPageType, nil
	}

	if isKnownCodexLoginPageType(pageType) {
		return pageType, nil
	}
	return "", fmt.Errorf("unexpected page type after codex email submit: %s", pageType)
}

func isKnownCodexLoginPageType(pageType authPageType) bool {
	switch pageType {
	case authPageTypeEmailOTPVerification,
		authPageTypeChatGPTConsent,
		authPageTypeChatGPTCodexConsent,
		authPageTypeWorkspaceSelection,
		authPageTypeExternalURL:
		return true
	default:
		return false
	}
}
