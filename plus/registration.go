package plus

import (
	"context"
	"fmt"
	"regexp"
	"time"
)

const (
	authOriginURL       = "https://auth.openai.com"
	chatgptURL          = "https://chatgpt.com"
	codexClientID       = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexRedirectURI    = "http://localhost:1455/auth/callback"
	defaultDataDir      = "data"
	defaultOTPPoll      = 5 * time.Second
	defaultOTPWait      = 3 * time.Minute
	defaultHTTPTimeout  = 30 * time.Second
	defaultBirthMinYear = 1980
	defaultBirthMaxYear = 2002
	chromeUserAgent     = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36"

	sentinelActionAuthorizeContinue = "authorize_continue"
	sentinelActionPasswordVerify    = "password_verify"

	authPageTypeLoginPassword          authPageType = "login_password"
	authPageTypeEmailOTPVerification   authPageType = "email_otp_verification"
	authPageTypeChatGPTConsent         authPageType = "sign_in_with_chatgpt_consent"
	authPageTypeChatGPTCodexConsent    authPageType = "sign_in_with_chatgpt_codex_consent"
	authPageTypeWorkspaceSelection     authPageType = "workspace_selection"
	authPageTypeExternalURL            authPageType = "external_url"
	defaultOAuthFollowRedirectMaxSteps              = 15
)

var (
	otpPattern           = regexp.MustCompile(`\b(\d{6})\b`)
	oauthCallbackPattern = buildOAuthCallbackPattern(codexRedirectURI)
	codexRedirectURL     = buildRedirectURL(codexRedirectURI)
)

type RegisterOptions struct {
	DataDir             string
	OTPWait             time.Duration
	OTPPoll             time.Duration
	Proxy               string
	RegistrationProxies []string
}

type AuthTokens struct {
	IDToken      string
	AccessToken  string
	RefreshToken string
}

type RegisterResult struct {
	Email     string
	Proxy     string
	AccountID string
	Session   ChatGPTSession
	Tokens    AuthTokens
	FilePath  string
}

type registrationConfig struct {
	Proxy   string
	DataDir string
	OTPWait time.Duration
	OTPPoll time.Duration
}

type registrationFlow struct {
	cfg           registrationConfig
	client        *client
	oaiDID        string
	email         string
	password      string
	name          string
	birthdate     string
	codeVerifier  string
	state         string
	codeChallenge string
}

type workspace struct {
	ID string `json:"id"`
}

type authPageType string

type authPageResponse struct {
	Page struct {
		Type authPageType `json:"type"`
	} `json:"page"`
}

type codexLoginContinueRequest struct {
	Username struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	} `json:"username"`
}

type credentialFile struct {
	AuthMode     string        `json:"auth_mode"`
	OpenAIAPIKey *string       `json:"OPENAI_API_KEY"`
	LastRefresh  string        `json:"last_refresh"`
	CreatedAt    string        `json:"created_at,omitempty"`
	Tokens       credentialJWT `json:"tokens"`
}

type credentialJWT struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
}

// RegisterCodexCredential executes registration + Codex login + purchase flow,
// then writes the active credential file after purchase succeeds.
func RegisterCodexCredential(ctx context.Context, opts RegisterOptions) (RegisterResult, error) {
	cfg, err := normalizeOptions(opts)
	if err != nil {
		return RegisterResult{}, fmt.Errorf("normalize options: %w", err)
	}

	r, err := newRegistrationFlow(cfg)
	if err != nil {
		return RegisterResult{}, fmt.Errorf("build registration flow: %w", err)
	}

	result, err := r.execute(ctx)
	if err != nil {
		return RegisterResult{}, fmt.Errorf("execute registration flow: %w", err)
	}
	return result, nil
}
