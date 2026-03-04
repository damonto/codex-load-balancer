package account

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
		return fmt.Errorf("generate code_verifier: %w", err)
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
	u := authBaseURL + "/oauth/authorize?" + params.Encode()

	resp, err := doRequest(ctx, r.client, http.MethodGet, u, nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("request codex oauth authorize: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("codex oauth authorize status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	landingURL := resp.Request.URL.String()
	if !isExpectedCodexOAuthLandingURL(landingURL) {
		slog.Warn("codex oauth authorize landed on unexpected url, continuing", "email", r.email, "landing_url", landingURL)
	}
	if !isCodexLoginPageURL(landingURL) {
		slog.Info("codex oauth not on login page, priming log-in before submit email", "email", r.email, "landing_url", landingURL)
		if err := r.primeLoginStep(ctx); err != nil {
			return "", fmt.Errorf("prime login after oauth authorize: %w", err)
		}
		landingURL = authBaseURL + "/log-in"
	}

	authURL, _ := url.Parse(authBaseURL)
	r.oaiDID = strings.TrimSpace(cookieValue(r.client, authURL, "oai-did"))
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
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("encode authorize continue request body: %w", err)
	}
	headers := map[string]string{
		"Accept":                "application/json",
		"Content-Type":          "application/json",
		"Origin":                authBaseURL,
		"Referer":               authBaseURL + "/log-in",
		"openai-sentinel-token": sentinelToken,
	}
	resp, err := doJSONRequest(ctx, r.client, http.MethodPost, authBaseURL+"/api/accounts/authorize/continue", headers, bodyJSON)
	if err != nil {
		return "", fmt.Errorf("post authorize continue: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		msg := strings.TrimSpace(string(raw))
		if resp.StatusCode == http.StatusBadRequest && strings.Contains(msg, "invalid_auth_step") {
			return "", fmt.Errorf("authorize continue status %d: %s: %w", resp.StatusCode, msg, errInvalidAuthStep)
		}
		return "", fmt.Errorf("authorize continue status %d: %s", resp.StatusCode, msg)
	}

	pageType, err := parseAuthPageType(resp.Body)
	if err != nil {
		return "", fmt.Errorf("decode authorize continue response: %w", err)
	}
	return pageType, nil
}

var (
	errInvalidAuthStep       = errors.New("invalid auth step")
	errLoginChallengeMissing = errors.New("login challenge missing in session")
)

func parseAuthPageType(reader io.Reader) (authPageType, error) {
	var parsed authPageResponse
	if err := json.NewDecoder(reader).Decode(&parsed); err != nil {
		return "", err
	}

	pageType := authPageType(strings.TrimSpace(string(parsed.Page.Type)))
	if pageType == "" {
		return "", errors.New("response page.type is empty")
	}
	return pageType, nil
}

func (r *registrationFlow) verifyPasswordForCodex(ctx context.Context) (authPageType, error) {
	sentinelToken, err := r.buildSentinelTokenHeader(ctx, sentinelActionPasswordVerify)
	if err != nil {
		return "", fmt.Errorf("build sentinel token: %w", err)
	}

	body := map[string]string{"password": r.password}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("encode password verify request body: %w", err)
	}
	headers := map[string]string{
		"Accept":                "application/json",
		"Content-Type":          "application/json",
		"Origin":                authBaseURL,
		"Referer":               authBaseURL + "/log-in/password",
		"openai-sentinel-token": sentinelToken,
	}
	resp, err := doJSONRequest(ctx, r.client, http.MethodPost, authBaseURL+"/api/accounts/password/verify", headers, bodyJSON)
	if err != nil {
		return "", fmt.Errorf("post password verify: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		msg := strings.TrimSpace(string(raw))
		if strings.Contains(msg, "login_challenge_not_found_in_session") {
			return "", fmt.Errorf("password verify status %d: %s: %w", resp.StatusCode, msg, errLoginChallengeMissing)
		}
		return "", fmt.Errorf("password verify status %d: %s", resp.StatusCode, msg)
	}

	pageType, err := parseAuthPageType(resp.Body)
	if err != nil {
		return "", fmt.Errorf("decode password verify response: %w", err)
	}
	return pageType, nil
}

func (r *registrationFlow) primeLoginStep(ctx context.Context) error {
	resp, err := doRequest(ctx, r.client, http.MethodGet, authBaseURL+"/log-in", nil, nil, "")
	if err != nil {
		return fmt.Errorf("open log-in page: %w", err)
	}
	resp.Body.Close()

	authURL, _ := url.Parse(authBaseURL)
	if did := strings.TrimSpace(cookieValue(r.client, authURL, "oai-did")); did != "" {
		r.oaiDID = did
	}
	if strings.TrimSpace(r.oaiDID) == "" {
		return errors.New("oai-did cookie missing after log-in page")
	}
	return nil
}

func (r *registrationFlow) buildSentinelTokenHeader(ctx context.Context, action string) (string, error) {
	action = strings.TrimSpace(action)
	if action == "" {
		return "", errors.New("sentinel action is empty")
	}
	if strings.TrimSpace(r.oaiDID) == "" {
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
	resp, err := doRequest(
		ctx,
		r.client,
		http.MethodPost,
		"https://sentinel.openai.com/backend-api/sentinel/req",
		headers,
		bytes.NewReader(reqBody),
		"text/plain;charset=UTF-8",
	)
	if err != nil {
		return "", fmt.Errorf("request sentinel token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("sentinel status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var payload struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode sentinel response: %w", err)
	}
	if strings.TrimSpace(payload.Token) == "" {
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
	authURL, _ := url.Parse(authBaseURL)
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
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("encode workspace select request body: %w", err)
	}
	headers := map[string]string{
		"Content-Type": "application/json",
		"Origin":       authBaseURL,
		"Referer":      authBaseURL + "/sign-in-with-chatgpt/codex/consent",
	}
	resp, err := doJSONRequest(ctx, r.client, http.MethodPost, authBaseURL+"/api/accounts/workspace/select", headers, bodyJSON)
	if err != nil {
		return "", fmt.Errorf("post workspace select: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("workspace select status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed struct {
		ContinueURL string `json:"continue_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode workspace select response: %w", err)
	}
	if parsed.ContinueURL == "" {
		return "", errors.New("continue_url missing")
	}
	return parsed.ContinueURL, nil
}

func (r *registrationFlow) completeOAuth(ctx context.Context, continueURL string) (string, error) {
	currentURL := strings.TrimSpace(continueURL)
	if currentURL == "" {
		return "", errors.New("continue url is empty")
	}
	if code := extractOAuthCode(currentURL); code != "" {
		return code, nil
	}

	for step := range defaultOAuthFollowRedirectMaxSteps {
		resp, err := doRequest(ctx, r.noRedirectClient, http.MethodGet, currentURL, nil, nil, "")
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
	headers := map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Origin": authBaseURL}
	resp, err := doRequest(ctx, r.client, http.MethodPost, authBaseURL+"/oauth/token", headers, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return AuthTokens{}, fmt.Errorf("post oauth token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return AuthTokens{}, fmt.Errorf("oauth token status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var token struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return AuthTokens{}, fmt.Errorf("decode oauth token response: %w", err)
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
		Tokens: credentialJWT{
			IDToken:      result.Tokens.IDToken,
			AccessToken:  result.Tokens.AccessToken,
			RefreshToken: result.Tokens.RefreshToken,
			AccountID:    result.AccountID,
		},
	}

	if err := os.MkdirAll(r.cfg.DataDir, 0o755); err != nil {
		return "", fmt.Errorf("create data dir: %w", err)
	}

	path := filepath.Join(r.cfg.DataDir, result.Email+".json")
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode credential json: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write credential file: %w", err)
	}
	return path, nil
}
