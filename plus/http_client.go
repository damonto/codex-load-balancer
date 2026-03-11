package plus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

type client struct {
	cfg registrationConfig
	raw tls_client.HttpClient
	mu  sync.Mutex
}

type responseError struct {
	StatusCode int
	Body       string
}

func (e responseError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("status %d", e.StatusCode)
	}
	return fmt.Sprintf("status %d: %s", e.StatusCode, e.Body)
}

func newClient(cfg registrationConfig) (*client, error) {
	jar := tls_client.NewCookieJar()
	timeoutSeconds := int(defaultHTTPTimeout / time.Second)

	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(timeoutSeconds),
		tls_client.WithClientProfile(profiles.Chrome_144),
		tls_client.WithCookieJar(jar),
		tls_client.WithProxyUrl(cfg.Proxy),
		tls_client.WithRandomTLSExtensionOrder(),
	}

	rawClient, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if err != nil {
		return nil, fmt.Errorf("create tls client: %w", err)
	}
	rawClient.SetFollowRedirect(true)
	return &client{cfg: cfg, raw: rawClient}, nil
}

func (c *client) Refresh() (*client, error) {
	return newClient(c.cfg)
}

func (c *client) Get(ctx context.Context, target string, headers map[string]string) (*http.Response, error) {
	return c.do(ctx, http.MethodGet, target, headers, nil, "", true)
}

func (c *client) GetOK(ctx context.Context, target string, headers map[string]string) error {
	resp, err := c.Get(ctx, target, headers)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return expectStatus(resp, http.StatusOK)
}

func (c *client) GetFinalURL(ctx context.Context, target string, headers map[string]string) (string, error) {
	resp, err := c.Get(ctx, target, headers)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if err := expectStatus(resp, http.StatusOK); err != nil {
		return "", err
	}
	if resp.Request == nil || resp.Request.URL == nil {
		return "", errors.New("response request url is empty")
	}
	return resp.Request.URL.String(), nil
}

func (c *client) GetJSON(ctx context.Context, target string, headers map[string]string, out any) error {
	resp, err := c.Get(ctx, target, headers)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := expectStatus(resp, http.StatusOK); err != nil {
		return err
	}
	return decodeJSON(resp.Body, out)
}

func (c *client) GetNoRedirect(ctx context.Context, target string, headers map[string]string) (*http.Response, error) {
	return c.do(ctx, http.MethodGet, target, headers, nil, "", false)
}

func (c *client) Post(ctx context.Context, target string, headers map[string]string, body io.Reader, contentType string) (*http.Response, error) {
	return c.do(ctx, http.MethodPost, target, headers, body, contentType, true)
}

func (c *client) PostJSONOK(ctx context.Context, target string, headers map[string]string, body any) error {
	resp, err := c.postJSON(ctx, target, headers, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return expectStatus(resp, http.StatusOK)
}

func (c *client) PostJSON(ctx context.Context, target string, headers map[string]string, body any, out any) error {
	resp, err := c.postJSON(ctx, target, headers, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := expectStatus(resp, http.StatusOK); err != nil {
		return err
	}
	return decodeJSON(resp.Body, out)
}

func (c *client) PostJSONOptional(ctx context.Context, target string, headers map[string]string, body any, out any) (bool, error) {
	resp, err := c.postJSON(ctx, target, headers, body)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if err := expectStatus(resp, http.StatusOK); err != nil {
		return false, err
	}
	return decodeOptionalJSON(resp.Body, out)
}

func (c *client) postJSON(ctx context.Context, target string, headers map[string]string, body any) (*http.Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode json request body: %w", err)
	}
	return c.do(ctx, http.MethodPost, target, headers, bytes.NewReader(payload), "application/json", true)
}

func (c *client) PostForm(ctx context.Context, target string, headers map[string]string, values url.Values, out any) error {
	resp, err := c.do(ctx, http.MethodPost, target, headers, bytes.NewBufferString(values.Encode()), "application/x-www-form-urlencoded", true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := expectStatus(resp, http.StatusOK); err != nil {
		return err
	}
	return decodeJSON(resp.Body, out)
}

func (c *client) do(ctx context.Context, method, target string, headers map[string]string, body io.Reader, contentType string, followRedirect bool) (*http.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("request canceled: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", chromeUserAgent)
	req.Header.Set("Accept", "*/*")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	c.mu.Lock()
	previousFollowRedirect := c.raw.GetFollowRedirect()
	c.raw.SetFollowRedirect(followRedirect)
	resp, err := c.raw.Do(req)
	c.raw.SetFollowRedirect(previousFollowRedirect)
	c.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, target, err)
	}
	return resp, nil
}

func expectStatus(resp *http.Response, want int) error {
	if resp.StatusCode == want {
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	return responseError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
}

func decodeJSON(reader io.Reader, out any) error {
	if err := json.NewDecoder(reader).Decode(out); err != nil {
		return fmt.Errorf("decode json response: %w", err)
	}
	return nil
}

func decodeOptionalJSON(reader io.Reader, out any) (bool, error) {
	if err := json.NewDecoder(reader).Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, fmt.Errorf("decode json response: %w", err)
	}
	return true, nil
}
