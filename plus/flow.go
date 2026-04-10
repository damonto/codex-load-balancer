package plus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

var (
	executePrepareAccountProfile = func(r *registrationFlow, ctx context.Context) (RegisterResult, error) {
		return r.prepareAccountProfile(ctx)
	}
	executeCompleteRegistrationFlow = func(r *registrationFlow, ctx context.Context) (ChatGPTSession, error) {
		return r.completeRegistrationFlow(ctx)
	}
	executeNewPurchase = func(client purchaseHTTPClient, session ChatGPTSession, cfg PurchaseConfig, lease *PurchaseTokenLease) *Purchase {
		return NewPurchase(client, session, cfg, lease)
	}
	executeCompleteCodexLoginFlow = func(r *registrationFlow, ctx context.Context) (AuthTokens, string, error) {
		return r.completeCodexLoginFlow(ctx)
	}
	executeSaveCredentialFile = func(r *registrationFlow, result RegisterResult) (string, error) {
		return r.saveCredentialFile(result)
	}
)

func (r *registrationFlow) execute(ctx context.Context) (result RegisterResult, err error) {
	result, err = executePrepareAccountProfile(r, ctx)
	if err != nil {
		return RegisterResult{}, err
	}
	if err := r.reservePurchaseToken(ctx); err != nil {
		return RegisterResult{}, fmt.Errorf("reserve purchase token: %w", err)
	}
	defer func() {
		if err == nil {
			return
		}
		if releaseErr := r.releaseReservedPurchaseToken(ctx); releaseErr != nil {
			slog.Warn("release purchase token", "err", releaseErr)
		}
	}()

	session, err := executeCompleteRegistrationFlow(r, ctx)
	if err != nil {
		return RegisterResult{}, err
	}
	result.Session = session

	if r.cfg.Purchase.Enabled {
		purchase := executeNewPurchase(r.client, session, r.cfg.Purchase, r.purchaseTokenLease)
		if err := purchase.Checkout(ctx); err != nil {
			return RegisterResult{}, fmt.Errorf("checkout: %w", err)
		}
		slog.Info("purchase step finished", "email", r.email, "account_id", session.Account.ID, "purchase_token_id", r.purchaseTokenLease.ID())
	} else {
		slog.Info("purchase skipped", "email", r.email, "account_id", session.Account.ID)
	}

	token, accountID, err := executeCompleteCodexLoginFlow(r, ctx)
	if err != nil {
		return RegisterResult{}, err
	}
	result.AccountID = accountID
	result.Tokens = token
	slog.Info("codex oauth completed", "email", r.email, "account_id", accountID)

	path, err := executeSaveCredentialFile(r, result)
	if err != nil {
		return RegisterResult{}, fmt.Errorf("save credential file: %w", err)
	}
	result.FilePath = path
	slog.Info("credential file saved", "email", r.email, "file", path)

	return result, nil
}

func (r *registrationFlow) reservePurchaseToken(ctx context.Context) error {
	if !r.cfg.Purchase.Enabled {
		return nil
	}
	lease, err := r.cfg.Purchase.Store.LeaseToken(ctx)
	if err != nil {
		return err
	}
	r.purchaseTokenLease = lease
	slog.Info("purchase token leased", "purchase_token_id", lease.ID())
	return nil
}

func (r *registrationFlow) releaseReservedPurchaseToken(ctx context.Context) error {
	if r.purchaseTokenLease == nil {
		return nil
	}
	cleanupCtx, cancel := cleanupContext(ctx)
	defer cancel()
	return r.purchaseTokenLease.Release(cleanupCtx)
}

func (r *registrationFlow) prepareAccountProfile(ctx context.Context) (RegisterResult, error) {
	result := RegisterResult{Proxy: r.client.Proxy()}

	email, err := GenerateWithContext(ctx)
	if err != nil {
		return RegisterResult{}, fmt.Errorf("generate email: %w", err)
	}
	result.Email = email
	r.email = email
	slog.Info("account registration started", "email", r.email, "proxy_host", proxyHostOnly(r.client.Proxy()))

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

	previousOTPEmail, err := latestEmailCursorWithContext(ctx, r.email)
	if err != nil {
		return ChatGPTSession{}, fmt.Errorf("load latest registration email: %w", err)
	}

	if err := r.sendVerificationEmailRegistered(ctx); err != nil {
		return ChatGPTSession{}, fmt.Errorf("send registration otp email: %w", err)
	}
	slog.Info("otp email requested", "email", r.email)

	otp, err := r.waitOTP(ctx, previousOTPEmail)
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

	previousOTPEmail, err := latestEmailCursorWithContext(ctx, r.email)
	if err != nil {
		return AuthTokens{}, "", fmt.Errorf("load latest codex login email: %w", err)
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
		otpCode, err := r.waitOTP(ctx, previousOTPEmail)
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
