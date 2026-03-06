package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

type websocketSmokeConfig struct {
	URL          string
	StatsURL     string
	APIKey       string
	Model        string
	Prompt       string
	Instructions string
	SessionID    string
	Timeout      time.Duration
	CheckStats   bool
}

type websocketSmokeClient struct {
	conn net.Conn
	rw   *bufio.ReadWriter
}

type websocketSmokeResult struct {
	ResponseID string
	OutputText string
	Usage      TokenUsage
}

func TestWebsocketSmoke(t *testing.T) {
	if strings.TrimSpace(os.Getenv("WS_SMOKE_ENABLE")) != "1" {
		t.Skip("set WS_SMOKE_ENABLE=1 to run the live websocket smoke test")
	}

	cfg := loadWebsocketSmokeConfig(t)

	beforeTotal := int64(0)
	if cfg.CheckStats {
		overview, err := fetchDashboardOverview(cfg)
		if err != nil {
			t.Fatalf("fetch dashboard overview before run: %v", err)
		}
		beforeTotal = overview.Total.TotalTokens
		t.Logf("dashboard total before = %d", beforeTotal)
	}

	client, err := openWebsocketSmokeClient(cfg)
	if err != nil {
		t.Fatalf("open websocket: %v", err)
	}
	defer client.close()

	requestBody, err := buildWebsocketSmokeRequest(cfg)
	if err != nil {
		t.Fatalf("build websocket request: %v", err)
	}
	if err := client.writeText(requestBody); err != nil {
		t.Fatalf("write websocket request: %v", err)
	}

	result, err := client.readUntilCompleted(cfg.Timeout)
	if err != nil {
		t.Fatalf("read websocket response: %v", err)
	}
	if result.Usage.TotalTokens() <= 0 {
		t.Fatalf("response.completed usage = %+v, want positive total tokens", result.Usage)
	}

	t.Logf("response_id=%s", result.ResponseID)
	t.Logf("assistant_output=%q", result.OutputText)
	t.Logf("usage=%+v total=%d", result.Usage, result.Usage.TotalTokens())

	if !cfg.CheckStats {
		return
	}

	if err := waitForDashboardUsageDelta(cfg, beforeTotal, result.Usage.TotalTokens()); err != nil {
		t.Fatal(err)
	}
}

func loadWebsocketSmokeConfig(t *testing.T) websocketSmokeConfig {
	t.Helper()

	apiKey := strings.TrimSpace(os.Getenv("WS_SMOKE_API_KEY"))
	if apiKey == "" {
		t.Fatal("WS_SMOKE_API_KEY is required")
	}

	urlValue := envOrDefault("WS_SMOKE_URL", "ws://127.0.0.1:8080/v1/responses")
	statsURL := strings.TrimSpace(os.Getenv("WS_SMOKE_STATS_URL"))
	if statsURL == "" {
		derived, err := deriveStatsURL(urlValue)
		if err != nil {
			t.Fatalf("derive stats url: %v", err)
		}
		statsURL = derived
	}

	return websocketSmokeConfig{
		URL:          urlValue,
		StatsURL:     statsURL,
		APIKey:       apiKey,
		Model:        envOrDefault("WS_SMOKE_MODEL", "gpt-5"),
		Prompt:       envOrDefault("WS_SMOKE_PROMPT", "Reply with exactly: websocket-ok"),
		Instructions: strings.TrimSpace(os.Getenv("WS_SMOKE_INSTRUCTIONS")),
		SessionID:    envOrDefault("WS_SMOKE_SESSION_ID", fmt.Sprintf("ws-smoke-%d", time.Now().Unix())),
		Timeout:      envDurationOrDefault(t, "WS_SMOKE_TIMEOUT_SECONDS", 45*time.Second),
		CheckStats:   envBoolOrDefault("WS_SMOKE_CHECK_STATS", true),
	}
}

func envOrDefault(key string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envBoolOrDefault(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func envDurationOrDefault(t *testing.T, key string, fallback time.Duration) time.Duration {
	t.Helper()

	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	seconds, err := time.ParseDuration(value + "s")
	if err != nil {
		t.Fatalf("parse %s: %v", key, err)
	}
	return seconds
}

func deriveStatsURL(rawWSURL string) (string, error) {
	parsed, err := url.Parse(rawWSURL)
	if err != nil {
		return "", fmt.Errorf("parse websocket url: %w", err)
	}
	switch parsed.Scheme {
	case "ws":
		parsed.Scheme = "http"
	case "wss":
		parsed.Scheme = "https"
	case "http", "https":
	default:
		return "", fmt.Errorf("unsupported websocket scheme %q", parsed.Scheme)
	}
	parsed.Path = "/stats/overview"
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func fetchDashboardOverview(cfg websocketSmokeConfig) (dashboardOverviewResponse, error) {
	client := &http.Client{Timeout: min(cfg.Timeout, 15*time.Second)}
	resp, err := client.Get(cfg.StatsURL)
	if err != nil {
		return dashboardOverviewResponse{}, fmt.Errorf("get %s: %w", cfg.StatsURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return dashboardOverviewResponse{}, fmt.Errorf("get %s status %d: %s", cfg.StatsURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var overview dashboardOverviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&overview); err != nil {
		return dashboardOverviewResponse{}, fmt.Errorf("decode dashboard overview: %w", err)
	}
	return overview, nil
}

func waitForDashboardUsageDelta(cfg websocketSmokeConfig, beforeTotal int64, minIncrease int64) error {
	deadline := time.Now().Add(cfg.Timeout)
	for time.Now().Before(deadline) {
		overview, err := fetchDashboardOverview(cfg)
		if err == nil {
			delta := overview.Total.TotalTokens - beforeTotal
			if delta >= minIncrease {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	overview, err := fetchDashboardOverview(cfg)
	if err != nil {
		return fmt.Errorf("fetch dashboard overview after run: %w", err)
	}
	delta := overview.Total.TotalTokens - beforeTotal
	return fmt.Errorf("dashboard total delta = %d, want at least %d", delta, minIncrease)
}

func openWebsocketSmokeClient(cfg websocketSmokeConfig) (*websocketSmokeClient, error) {
	parsed, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse websocket url: %w", err)
	}

	conn, err := dialWebsocketTarget(parsed, cfg.Timeout)
	if err != nil {
		return nil, err
	}

	key, err := websocketHandshakeKey()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("generate websocket key: %w", err)
	}

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	req, err := http.NewRequest(http.MethodGet, parsed.String(), nil)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("build websocket request: %w", err)
	}
	req.Host = parsed.Host
	req.Header.Set("Authorization", authHeaderValue(cfg.APIKey))
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", key)
	req.Header.Set("session_id", cfg.SessionID)

	if err := req.Write(rw); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write websocket handshake: %w", err)
	}
	if err := rw.Flush(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("flush websocket handshake: %w", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(cfg.Timeout)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("set handshake read deadline: %w", err)
	}
	resp, err := http.ReadResponse(rw.Reader, req)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read websocket handshake response: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		conn.Close()
		return nil, fmt.Errorf("websocket handshake status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if !headerHasToken(resp.Header.Get("Connection"), "upgrade") {
		conn.Close()
		return nil, fmt.Errorf("websocket handshake missing Connection: Upgrade")
	}
	if !strings.EqualFold(strings.TrimSpace(resp.Header.Get("Upgrade")), "websocket") {
		conn.Close()
		return nil, fmt.Errorf("websocket handshake missing Upgrade: websocket")
	}
	if got := strings.TrimSpace(resp.Header.Get("Sec-WebSocket-Accept")); got != websocketAcceptValue(key) {
		conn.Close()
		return nil, fmt.Errorf("websocket accept mismatch: got %q", got)
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("clear handshake read deadline: %w", err)
	}

	return &websocketSmokeClient{conn: conn, rw: rw}, nil
}

func dialWebsocketTarget(target *url.URL, timeout time.Duration) (net.Conn, error) {
	hostPort := target.Host
	if _, _, err := net.SplitHostPort(hostPort); err != nil {
		switch target.Scheme {
		case "wss", "https":
			hostPort = net.JoinHostPort(target.Hostname(), "443")
		default:
			hostPort = net.JoinHostPort(target.Hostname(), "80")
		}
	}

	dialer := &net.Dialer{Timeout: timeout}
	switch target.Scheme {
	case "wss", "https":
		return tls.DialWithDialer(dialer, "tcp", hostPort, &tls.Config{ServerName: target.Hostname()})
	case "ws", "http":
		return dialer.Dial("tcp", hostPort)
	default:
		return nil, fmt.Errorf("unsupported websocket scheme %q", target.Scheme)
	}
}

func websocketHandshakeKey() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func websocketAcceptValue(key string) string {
	hash := sha1.Sum([]byte(key + websocketGUID))
	return base64.StdEncoding.EncodeToString(hash[:])
}

func buildWebsocketSmokeRequest(cfg websocketSmokeConfig) ([]byte, error) {
	request := map[string]any{
		"type":         "response.create",
		"model":        cfg.Model,
		"instructions": cfg.Instructions,
		"input": []map[string]any{{
			"type": "message",
			"role": "user",
			"content": []map[string]any{{
				"type": "input_text",
				"text": cfg.Prompt,
			}},
		}},
		"tools":               []any{},
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
		"store":               false,
		"stream":              true,
		"include":             []string{},
	}
	return json.Marshal(request)
}

func (c *websocketSmokeClient) close() {
	if c == nil || c.conn == nil {
		return
	}
	_ = c.writeControl(websocketOpcodeClose, nil)
	_ = c.conn.Close()
}

func (c *websocketSmokeClient) writeText(payload []byte) error {
	return c.writeFrame(websocketOpcodeText, payload, true)
}

func (c *websocketSmokeClient) writeControl(opcode byte, payload []byte) error {
	return c.writeFrame(opcode, payload, true)
}

func (c *websocketSmokeClient) writeFrame(opcode byte, payload []byte, masked bool) error {
	if c == nil || c.rw == nil {
		return fmt.Errorf("websocket client is nil")
	}

	frame, err := buildWebsocketFrame(opcode, true, masked, payload)
	if err != nil {
		return err
	}
	if _, err := c.rw.Write(frame); err != nil {
		return fmt.Errorf("write websocket frame: %w", err)
	}
	if err := c.rw.Flush(); err != nil {
		return fmt.Errorf("flush websocket frame: %w", err)
	}
	return nil
}

func buildWebsocketFrame(opcode byte, fin bool, masked bool, payload []byte) ([]byte, error) {
	var frame bytes.Buffer
	first := opcode
	if fin {
		first |= 0x80
	}
	if err := frame.WriteByte(first); err != nil {
		return nil, fmt.Errorf("write websocket first byte: %w", err)
	}

	second := byte(0)
	if masked {
		second |= 0x80
	}
	switch n := len(payload); {
	case n < 126:
		second |= byte(n)
		if err := frame.WriteByte(second); err != nil {
			return nil, fmt.Errorf("write websocket second byte: %w", err)
		}
	case n <= 0xFFFF:
		second |= 126
		if err := frame.WriteByte(second); err != nil {
			return nil, fmt.Errorf("write websocket second byte: %w", err)
		}
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(n))
		if _, err := frame.Write(ext[:]); err != nil {
			return nil, fmt.Errorf("write websocket length16: %w", err)
		}
	default:
		second |= 127
		if err := frame.WriteByte(second); err != nil {
			return nil, fmt.Errorf("write websocket second byte: %w", err)
		}
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		if _, err := frame.Write(ext[:]); err != nil {
			return nil, fmt.Errorf("write websocket length64: %w", err)
		}
	}

	if !masked {
		if _, err := frame.Write(payload); err != nil {
			return nil, fmt.Errorf("write websocket payload: %w", err)
		}
		return frame.Bytes(), nil
	}

	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return nil, fmt.Errorf("generate websocket mask: %w", err)
	}
	if _, err := frame.Write(mask); err != nil {
		return nil, fmt.Errorf("write websocket mask: %w", err)
	}
	maskedPayload := append([]byte(nil), payload...)
	applyWebsocketMask(maskedPayload, mask)
	if _, err := frame.Write(maskedPayload); err != nil {
		return nil, fmt.Errorf("write websocket masked payload: %w", err)
	}
	return frame.Bytes(), nil
}

func (c *websocketSmokeClient) readUntilCompleted(timeout time.Duration) (websocketSmokeResult, error) {
	deadline := time.Now().Add(timeout)
	var result websocketSmokeResult
	var fragments bytes.Buffer
	var fragmentedOpcode byte

	for {
		if err := c.conn.SetReadDeadline(deadline); err != nil {
			return websocketSmokeResult{}, fmt.Errorf("set websocket read deadline: %w", err)
		}
		frame, err := readWebsocketFrame(c.rw.Reader)
		if err != nil {
			return websocketSmokeResult{}, err
		}

		switch frame.opcode {
		case websocketOpcodePing:
			if err := c.writeControl(websocketOpcodePong, frame.payload); err != nil {
				return websocketSmokeResult{}, fmt.Errorf("write pong: %w", err)
			}
		case websocketOpcodePong:
			continue
		case websocketOpcodeClose:
			return websocketSmokeResult{}, fmt.Errorf("websocket closed before response.completed")
		case websocketOpcodeBinary:
			if frame.fin {
				continue
			}
			fragmentedOpcode = websocketOpcodeBinary
			fragments.Reset()
		case websocketOpcodeText:
			if frame.fin {
				completed, err := applyWebsocketEvent(frame.payload, &result)
				if err != nil {
					return websocketSmokeResult{}, err
				}
				if completed {
					return result, nil
				}
				continue
			}
			fragmentedOpcode = websocketOpcodeText
			fragments.Reset()
			if _, err := fragments.Write(frame.payload); err != nil {
				return websocketSmokeResult{}, fmt.Errorf("buffer websocket fragments: %w", err)
			}
		case websocketOpcodeContinuation:
			switch fragmentedOpcode {
			case websocketOpcodeText:
				if _, err := fragments.Write(frame.payload); err != nil {
					return websocketSmokeResult{}, fmt.Errorf("buffer websocket continuation: %w", err)
				}
				if !frame.fin {
					continue
				}
				completed, err := applyWebsocketEvent(fragments.Bytes(), &result)
				fragments.Reset()
				fragmentedOpcode = 0
				if err != nil {
					return websocketSmokeResult{}, err
				}
				if completed {
					return result, nil
				}
			case websocketOpcodeBinary:
				if frame.fin {
					fragmentedOpcode = 0
				}
			default:
				return websocketSmokeResult{}, fmt.Errorf("unexpected websocket continuation frame")
			}
		default:
			return websocketSmokeResult{}, fmt.Errorf("unexpected websocket opcode %d", frame.opcode)
		}
	}
}

type smokeWebsocketFrame struct {
	opcode  byte
	fin     bool
	payload []byte
}

func readWebsocketFrame(r *bufio.Reader) (smokeWebsocketFrame, error) {
	first, err := r.ReadByte()
	if err != nil {
		return smokeWebsocketFrame{}, fmt.Errorf("read websocket first byte: %w", err)
	}
	second, err := r.ReadByte()
	if err != nil {
		return smokeWebsocketFrame{}, fmt.Errorf("read websocket second byte: %w", err)
	}

	opcode := first & 0x0F
	fin := first&0x80 != 0
	masked := second&0x80 != 0
	payloadLen := uint64(second & 0x7F)

	switch payloadLen {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return smokeWebsocketFrame{}, fmt.Errorf("read websocket length16: %w", err)
		}
		payloadLen = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return smokeWebsocketFrame{}, fmt.Errorf("read websocket length64: %w", err)
		}
		payloadLen = binary.BigEndian.Uint64(ext[:])
	}

	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return smokeWebsocketFrame{}, fmt.Errorf("read websocket mask: %w", err)
		}
	}

	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(r, payload); err != nil {
		return smokeWebsocketFrame{}, fmt.Errorf("read websocket payload: %w", err)
	}
	if masked {
		applyWebsocketMask(payload, mask[:])
	}

	return smokeWebsocketFrame{opcode: opcode, fin: fin, payload: payload}, nil
}

func applyWebsocketEvent(payload []byte, result *websocketSmokeResult) (bool, error) {
	var envelope struct {
		Type     string `json:"type"`
		Delta    string `json:"delta"`
		Response *struct {
			ID    string `json:"id"`
			Error *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"response"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return false, fmt.Errorf("decode websocket event: %w", err)
	}

	if envelope.Response != nil && envelope.Response.ID != "" {
		result.ResponseID = envelope.Response.ID
	}

	switch envelope.Type {
	case "response.output_text.delta":
		result.OutputText += envelope.Delta
		return false, nil
	case "response.failed":
		if envelope.Response != nil && envelope.Response.Error != nil {
			return false, fmt.Errorf("response.failed %s: %s", envelope.Response.Error.Code, envelope.Response.Error.Message)
		}
		if envelope.Error != nil {
			return false, fmt.Errorf("response.failed %s: %s", envelope.Error.Code, envelope.Error.Message)
		}
		return false, fmt.Errorf("response.failed without error details")
	case "error":
		if envelope.Error != nil {
			return false, fmt.Errorf("websocket error %s: %s", envelope.Error.Code, envelope.Error.Message)
		}
		return false, fmt.Errorf("websocket error without details")
	case "response.completed":
		usage, ok := extractCompletedTokenUsageFromJSON(payload)
		if !ok {
			return false, fmt.Errorf("response.completed missing usage")
		}
		result.Usage = usage
		return true, nil
	default:
		return false, nil
	}
}
