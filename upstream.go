package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

func (s *Server) forwardRequest(r *http.Request, body []byte, token TokenState, path string) (*http.Response, []byte, bool, error) {
	target := *s.upstreamURL
	target.Path = joinURLPath(s.upstreamURL.Path, path)
	target.RawQuery = r.URL.RawQuery

	return s.forwardRequestWithTarget(r, body, target, token.Token, token.AccountID)
}

func (s *Server) forwardWebSocketRequest(r *http.Request, token TokenState, path string) (*websocketUpstreamResponse, error) {
	target := *s.upstreamURL
	target.Path = joinURLPath(s.upstreamURL.Path, path)
	target.RawQuery = r.URL.RawQuery
	return s.forwardWebSocketRequestWithTarget(r, target, token.Token, token.AccountID)
}

func (s *Server) forwardRequestWithTarget(r *http.Request, body []byte, target url.URL, authToken string, accountID string) (*http.Response, []byte, bool, error) {
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, nil, false, fmt.Errorf("build upstream request: %w", err)
	}

	req.Header = cloneHeaders(r.Header)
	req.Header.Set("Authorization", authHeaderValue(authToken))
	if accountID != "" && req.Header.Get("ChatGPT-Account-ID") == "" {
		req.Header.Set("ChatGPT-Account-ID", accountID)
	}
	req.ContentLength = r.ContentLength
	req.TransferEncoding = append([]string(nil), r.TransferEncoding...)
	req.Host = target.Host

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, nil, false, fmt.Errorf("send upstream request: %w", err)
	}

	if isEventStream(resp) {
		return resp, nil, true, nil
	}

	respBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, nil, false, fmt.Errorf("read upstream response: %w", err)
	}
	return resp, respBody, false, nil
}

type websocketUpstreamResponse struct {
	resp *http.Response
	body []byte
	conn net.Conn
	br   *bufio.Reader
}

func (s *Server) forwardWebSocketRequestWithTarget(r *http.Request, target url.URL, authToken string, accountID string) (*websocketUpstreamResponse, error) {
	conn, err := dialTarget(r.Context(), target)
	if err != nil {
		return nil, fmt.Errorf("dial upstream websocket: %w", err)
	}

	req := r.Clone(r.Context())
	req.URL = &target
	req.RequestURI = ""
	req.Host = target.Host
	req.Header = cloneHeaders(r.Header)
	req.Body = nil
	req.ContentLength = 0
	req.TransferEncoding = nil
	req.Header.Set("Authorization", authHeaderValue(authToken))
	if accountID != "" && req.Header.Get("ChatGPT-Account-ID") == "" {
		req.Header.Set("ChatGPT-Account-ID", accountID)
	}

	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write upstream websocket request: %w", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read upstream websocket response: %w", err)
	}

	upstream := &websocketUpstreamResponse{resp: resp}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		conn.Close()
		if err != nil {
			return nil, fmt.Errorf("read upstream websocket response body: %w", err)
		}
		upstream.body = body
		return upstream, nil
	}

	upstream.conn = conn
	upstream.br = br
	return upstream, nil
}

func isEventStream(resp *http.Response) bool {
	if resp.StatusCode != http.StatusOK {
		return false
	}
	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if strings.HasPrefix(contentType, "text/event-stream") {
		return true
	}
	// Some Codex endpoints omit Content-Type but still deliver SSE over
	// chunked transfer encoding. Treat these as streams so we don't buffer
	// the entire response before forwarding and can capture usage inline.
	if contentType == "" {
		for _, enc := range resp.TransferEncoding {
			if strings.EqualFold(enc, "chunked") {
				return true
			}
		}
	}
	return false
}

func dialTarget(ctx context.Context, target url.URL) (net.Conn, error) {
	hostPort := target.Host
	if _, _, err := net.SplitHostPort(hostPort); err != nil {
		switch target.Scheme {
		case "https", "wss":
			hostPort = net.JoinHostPort(target.Hostname(), "443")
		default:
			hostPort = net.JoinHostPort(target.Hostname(), "80")
		}
	}

	dialer := &net.Dialer{}
	switch target.Scheme {
	case "https", "wss":
		tlsDialer := &tls.Dialer{
			NetDialer: dialer,
			Config: &tls.Config{
				ServerName: target.Hostname(),
			},
		}
		conn, err := tlsDialer.DialContext(ctx, "tcp", hostPort)
		if err != nil {
			return nil, err
		}
		return conn, nil
	default:
		return dialer.DialContext(ctx, "tcp", hostPort)
	}
}
