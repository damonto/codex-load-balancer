package plus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
)

func (r *registrationFlow) finalizeCodexOAuth(ctx context.Context) (AuthTokens, string, error) {
	slog.Info("starting codex oauth finalization", "email", r.email)
	workspaces, err := r.loadWorkspaces()
	if err != nil {
		return AuthTokens{}, "", fmt.Errorf("load workspaces: %w", err)
	}
	if len(workspaces) == 0 {
		return AuthTokens{}, "", errors.New("workspace list is empty")
	}
	slog.Info("workspaces loaded", "email", r.email, "count", len(workspaces))

	workspaceID := workspaces[0].ID
	if workspaceID == "" {
		return AuthTokens{}, "", errors.New("workspace id is empty")
	}
	slog.Info("workspace selected", "email", r.email, "workspace_id", workspaceID)

	continueURL, err := r.selectWorkspace(ctx, workspaceID)
	if err != nil {
		return AuthTokens{}, "", fmt.Errorf("select workspace: %w", err)
	}
	slog.Info("workspace continue url received", "email", r.email)

	code, err := r.completeOAuth(ctx, continueURL)
	if err != nil {
		return AuthTokens{}, "", fmt.Errorf("complete oauth: %w", err)
	}
	slog.Info("oauth code captured", "email", r.email, "code_len", len(code))

	tokens, err := r.exchangeToken(ctx, code)
	if err != nil {
		return AuthTokens{}, "", fmt.Errorf("exchange token: %w", err)
	}
	slog.Info("oauth token exchanged", "email", r.email)

	return tokens, workspaceID, nil
}

func (r *registrationFlow) initPKCE() error {
	codeVerifier, err := randomBase64URL(64)
	if err != nil {
		return fmt.Errorf("generate code verifier: %w", err)
	}
	state, err := randomHexString(16)
	if err != nil {
		return fmt.Errorf("generate state: %w", err)
	}
	digest := sha256.Sum256([]byte(codeVerifier))

	r.codeVerifier = codeVerifier
	r.state = state
	r.codeChallenge = base64.RawURLEncoding.EncodeToString(digest[:])
	return nil
}

func (r *registrationFlow) initCodexOAuth(ctx context.Context) (string, error) {
	params := url.Values{
		"client_id":                  {codexClientID},
		"code_challenge":             {r.codeChallenge},
		"code_challenge_method":      {"S256"},
		"codex_cli_simplified_flow":  {"true"},
		"id_token_add_organizations": {"true"},
		"prompt":                     {"login"},
		"redirect_uri":               {codexRedirectURI},
		"response_type":              {"code"},
		"scope":                      {"openid email profile offline_access"},
		"state":                      {r.state},
	}
	u := authOriginURL + "/oauth/authorize?" + params.Encode()

	landingURL, err := r.client.GetFinalURL(ctx, u, nil)
	if err != nil {
		return "", fmt.Errorf("request codex oauth authorize: %w", err)
	}
	if !isExpectedCodexOAuthLandingURL(landingURL) {
		slog.Warn("codex oauth authorize landed on unexpected url, continuing", "email", r.email, "landing_url", landingURL)
	}
	if !isCodexLoginPageURL(landingURL) {
		slog.Info("codex oauth not on login page, priming log-in before submit email", "email", r.email, "landing_url", landingURL)
		if err := r.primeLoginStep(ctx); err != nil {
			return "", fmt.Errorf("prime login after oauth authorize: %w", err)
		}
		landingURL = authOriginURL + "/log-in"
	}

	authURL, _ := url.Parse(authOriginURL)
	r.oaiDID = cookieValue(r.client, authURL, "oai-did")
	if r.oaiDID == "" {
		slog.Warn("oai-did cookie missing after oauth authorize, opening login page once", "email", r.email, "landing_url", landingURL)
		if err := r.primeLoginStep(ctx); err != nil {
			return "", fmt.Errorf("prime login after oauth authorize: %w", err)
		}
	}

	return landingURL, nil
}

func (r *registrationFlow) submitEmailForCodex(ctx context.Context) (authPageType, error) {
	pageType, err := r.submitEmailForCodexOnce(ctx)
	if err != nil {
		if !errors.Is(err, errInvalidAuthStep) {
			return "", err
		}
		slog.Warn("invalid auth step, priming login page and retrying", "email", r.email)
		if err := r.primeLoginStep(ctx); err != nil {
			return "", fmt.Errorf("prime login step: %w", err)
		}
		pageType, err = r.submitEmailForCodexOnce(ctx)
		if err != nil {
			return "", err
		}
	}
	return pageType, nil
}

func (r *registrationFlow) submitEmailForCodexOnce(ctx context.Context) (authPageType, error) {
	sentinelToken, err := r.buildSentinelTokenHeader(ctx, sentinelActionAuthorizeContinue)
	if err != nil {
		return "", fmt.Errorf("build sentinel token: %w", err)
	}

	var body codexLoginContinueRequest
	body.Username.Kind = "email"
	body.Username.Value = r.email
	headers := map[string]string{
		"Accept":                "application/json",
		"Origin":                authOriginURL,
		"Referer":               authOriginURL + "/log-in",
		"openai-sentinel-token": sentinelToken,
	}
	var parsed authPageResponse
	err = r.client.PostJSON(ctx, authOriginURL+"/api/accounts/authorize/continue", headers, body, &parsed)
	if err != nil {
		var statusErr responseError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusBadRequest && strings.Contains(statusErr.Body, "invalid_auth_step") {
			return "", fmt.Errorf("post authorize continue: %w: %w", err, errInvalidAuthStep)
		}
		return "", fmt.Errorf("post authorize continue: %w", err)
	}
	pageType, err := authPageTypeFromResponse(parsed)
	if err != nil {
		return "", fmt.Errorf("decode authorize continue response: %w", err)
	}
	return pageType, nil
}

var (
	errInvalidAuthStep       = errors.New("invalid auth step")
	errLoginChallengeMissing = errors.New("login challenge missing in session")
)

func authPageTypeFromResponse(parsed authPageResponse) (authPageType, error) {
	pageType := parsed.Page.Type
	if pageType == "" {
		return "", errors.New("auth page type is empty")
	}
	return pageType, nil
}

func (r *registrationFlow) verifyPasswordForCodex(ctx context.Context) (authPageType, error) {
	sentinelToken, err := r.buildSentinelTokenHeader(ctx, sentinelActionPasswordVerify)
	if err != nil {
		return "", fmt.Errorf("build sentinel token: %w", err)
	}

	body := map[string]string{"password": r.password}
	headers := map[string]string{
		"Accept":                "application/json",
		"Origin":                authOriginURL,
		"Referer":               authOriginURL + "/log-in/password",
		"openai-sentinel-token": sentinelToken,
	}
	var parsed authPageResponse
	err = r.client.PostJSON(ctx, authOriginURL+"/api/accounts/password/verify", headers, body, &parsed)
	if err != nil {
		var statusErr responseError
		if errors.As(err, &statusErr) && strings.Contains(statusErr.Body, "login_challenge_not_found_in_session") {
			return "", fmt.Errorf("post password verify: %w: %w", err, errLoginChallengeMissing)
		}
		return "", fmt.Errorf("post password verify: %w", err)
	}
	pageType, err := authPageTypeFromResponse(parsed)
	if err != nil {
		return "", fmt.Errorf("decode password verify response: %w", err)
	}
	return pageType, nil
}

func (r *registrationFlow) primeLoginStep(ctx context.Context) error {
	if err := r.client.GetOK(ctx, authOriginURL+"/log-in", nil); err != nil {
		return fmt.Errorf("open log-in page: %w", err)
	}

	authURL, _ := url.Parse(authOriginURL)
	if did := cookieValue(r.client, authURL, "oai-did"); did != "" {
		r.oaiDID = did
	}
	if r.oaiDID == "" {
		return errors.New("oai-did cookie missing after log-in page")
	}
	return nil
}

func (r *registrationFlow) buildSentinelTokenHeader(ctx context.Context, action string) (string, error) {
	if action == "" {
		return "", errors.New("sentinel action is empty")
	}
	if r.oaiDID == "" {
		return "", errors.New("oai-did is empty")
	}

	reqPayload := map[string]string{
		"p":    "",
		"id":   r.oaiDID,
		"flow": action,
	}
	reqBody, err := json.Marshal(reqPayload)
	if err != nil {
		return "", fmt.Errorf("encode sentinel request body: %w", err)
	}

	headers := map[string]string{
		"origin":  "https://sentinel.openai.com",
		"referer": "https://sentinel.openai.com/backend-api/sentinel/frame.html?sv=20260219f9f6",
	}
	resp, err := r.client.Post(ctx, "https://sentinel.openai.com/backend-api/sentinel/req", headers, bytes.NewReader(reqBody), "text/plain;charset=UTF-8")
	if err != nil {
		return "", fmt.Errorf("request sentinel token: %w", err)
	}
	defer resp.Body.Close()
	if err := expectStatus(resp, http.StatusOK); err != nil {
		return "", fmt.Errorf("request sentinel token: %w", err)
	}

	var payload struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(resp.Body, &payload); err != nil {
		return "", fmt.Errorf("decode sentinel response: %w", err)
	}
	if payload.Token == "" {
		return "", errors.New("sentinel response token is empty")
	}

	tokenPayload := map[string]string{
		"p":    "",
		"t":    "",
		"c":    payload.Token,
		"id":   r.oaiDID,
		"flow": action,
	}
	tokenBody, err := json.Marshal(tokenPayload)
	if err != nil {
		return "", fmt.Errorf("encode sentinel token header: %w", err)
	}
	return string(tokenBody), nil
}

func (r *registrationFlow) loadWorkspaces() ([]workspace, error) {
	authURL, _ := url.Parse(authOriginURL)
	authCookie := cookieValue(r.client, authURL, "oai-client-auth-session")
	if authCookie == "" {
		return nil, errors.New("oai-client-auth-session cookie missing")
	}

	parts := strings.SplitN(authCookie, ".", 2)
	if parts[0] == "" {
		return nil, errors.New("invalid auth session cookie")
	}

	payload, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode auth session cookie: %s %w", parts[0], err)
	}

	var parsed struct {
		Workspaces []workspace `json:"workspaces"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return nil, fmt.Errorf("decode auth session payload: %w", err)
	}
	return parsed.Workspaces, nil
}

func (r *registrationFlow) selectWorkspace(ctx context.Context, workspaceID string) (string, error) {
	body := map[string]string{"workspace_id": workspaceID}
	headers := map[string]string{
		"Origin":  authOriginURL,
		"Referer": authOriginURL + "/sign-in-with-chatgpt/codex/consent",
	}
	var parsed struct {
		ContinueURL string `json:"continue_url"`
	}
	err := r.client.PostJSON(ctx, authOriginURL+"/api/accounts/workspace/select", headers, body, &parsed)
	if err != nil {
		return "", fmt.Errorf("post workspace select: %w", err)
	}
	if parsed.ContinueURL == "" {
		return "", errors.New("continue url is empty")
	}
	return parsed.ContinueURL, nil
}

func (r *registrationFlow) completeOAuth(ctx context.Context, continueURL string) (string, error) {
	currentURL := continueURL
	if currentURL == "" {
		return "", errors.New("continue url is empty")
	}
	if code := extractOAuthCode(currentURL); code != "" {
		return code, nil
	}

	for step := range defaultOAuthFollowRedirectMaxSteps {
		resp, err := r.client.GetNoRedirect(ctx, currentURL, nil)
		if err != nil {
			return "", fmt.Errorf("request oauth follow url: %w", err)
		}
		status := resp.StatusCode
		location := strings.TrimSpace(resp.Header.Get("Location"))
		finalURL := ""
		if resp.Request != nil && resp.Request.URL != nil {
			finalURL = resp.Request.URL.String()
		}

		if code := extractOAuthCode(location, finalURL); code != "" {
			resp.Body.Close()
			return code, nil
		}

		nextURL := ""
		if location != "" {
			resolved, err := resolveLocation(currentURL, location)
			if err != nil {
				resp.Body.Close()
				return "", fmt.Errorf("resolve redirect location: %w", err)
			}
			nextURL = resolved
			if code := extractOAuthCode(nextURL); code != "" {
				resp.Body.Close()
				return code, nil
			}
		}

		if isRedirect(status) && location != "" {
			resp.Body.Close()
			currentURL = nextURL
			continue
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
		resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("read oauth follow body: %w", err)
		}
		if code := extractOAuthCodeFromBody(string(body)); code != "" {
			return code, nil
		}

		slog.Warn("oauth follow step yielded no code", "email", r.email, "step", step+1, "status", status, "url", currentURL, "location", location, "final_url", finalURL)
		break
	}

	return "", errors.New("oauth code not found in redirect chain")
}

func (r *registrationFlow) exchangeToken(ctx context.Context, code string) (AuthTokens, error) {
	form := url.Values{
		"client_id":     {codexClientID},
		"code":          {code},
		"code_verifier": {r.codeVerifier},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {codexRedirectURI},
	}
	headers := map[string]string{"Origin": authOriginURL}
	var token struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	err := r.client.PostForm(ctx, authOriginURL+"/oauth/token", headers, form, &token)
	if err != nil {
		return AuthTokens{}, fmt.Errorf("post oauth token: %w", err)
	}
	if token.AccessToken == "" || token.RefreshToken == "" || token.IDToken == "" {
		return AuthTokens{}, errors.New("oauth token response missing required fields")
	}
	return AuthTokens{IDToken: token.IDToken, AccessToken: token.AccessToken, RefreshToken: token.RefreshToken}, nil
}

func (r *registrationFlow) saveCredentialFile(result RegisterResult) (string, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	payload := credentialFile{
		AuthMode:    "chatgpt",
		LastRefresh: now,
		CreatedAt:   now,
		Tokens: credentialJWT{
			IDToken:      result.Tokens.IDToken,
			AccessToken:  result.Tokens.AccessToken,
			RefreshToken: result.Tokens.RefreshToken,
			AccountID:    result.AccountID,
		},
	}

	if err := os.MkdirAll(r.cfg.DataDir, 0o755); err != nil {
		return "", fmt.Errorf("create credential data dir: %w", err)
	}

	filePath := filepath.Join(r.cfg.DataDir, result.Email+".json")
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode credential json: %w", err)
	}
	data = append(data, '\n')
	if err := writeCredentialFile(filePath, data); err != nil {
		return "", fmt.Errorf("write credential file: %w", err)
	}
	return filePath, nil
}
