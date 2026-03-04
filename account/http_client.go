package account

import (
	"bytes"
	"context"
	"fmt"
	"io"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
)

func doRequest(ctx context.Context, client tls_client.HttpClient, method, target string, headers map[string]string, body io.Reader, contentType string) (*http.Response, error) {
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
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, target, err)
	}
	return resp, nil
}

func doJSONRequest(ctx context.Context, client tls_client.HttpClient, method, target string, headers map[string]string, body []byte) (*http.Response, error) {
	return doRequest(ctx, client, method, target, headers, bytes.NewReader(body), "application/json")
}
