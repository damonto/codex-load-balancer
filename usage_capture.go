package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"
)

type TokenUsage struct {
	InputTokens     int64 `json:"input_tokens"`
	CachedTokens    int64 `json:"cached_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

var sseDataPrefix = []byte("data:")
var sseEventPrefix = []byte("event:")
var sseResponseCompletedEvent = []byte("response.completed")
var sseDonePayload = []byte("[DONE]")

func (u TokenUsage) TotalTokens() int64 {
	return u.InputTokens + u.CachedTokens + u.OutputTokens
}

func (s *Server) recordTokenUsage(token TokenState, path string, statusCode int, stream bool, usage TokenUsage) {
	if s == nil || s.usageSink == nil {
		return
	}
	s.usageSink.Record(UsageRecord{
		AccountKey:      accountKeyFromToken(token),
		TokenID:         token.ID,
		Path:            path,
		StatusCode:      statusCode,
		IsStream:        stream,
		InputTokens:     usage.InputTokens,
		CachedTokens:    usage.CachedTokens,
		OutputTokens:    usage.OutputTokens,
		ReasoningTokens: usage.ReasoningTokens,
		CreatedAt:       time.Now().UTC(),
	})
}

func accountKeyFromToken(token TokenState) string {
	return accountKey(token.AccountID, token.ID)
}

func accountKeyFromRef(ref TokenRef) string {
	return accountKey(ref.AccountID, ref.ID)
}

func accountKey(accountID string, fallbackID string) string {
	if value := strings.TrimSpace(accountID); value != "" {
		return value
	}
	return fallbackID
}

func extractTokenUsageFromBody(body []byte) (TokenUsage, bool) {
	if usage, ok := extractTokenUsageFromJSON(body); ok {
		return usage, ok
	}
	capture := newSSEUsageCapture()
	if _, err := capture.Write(body); err != nil {
		return TokenUsage{}, false
	}
	return capture.Usage()
}

func extractTokenUsageFromJSON(data []byte) (TokenUsage, bool) {
	var envelope usageEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return TokenUsage{}, false
	}

	usage, found := usageFromEnvelope(envelope)
	if !found {
		return TokenUsage{}, false
	}
	return usage, true
}

type usageEnvelope struct {
	Usage    *usageFields          `json:"usage"`
	Response *usageResponseWrapper `json:"response"`
}

type usageResponseWrapper struct {
	Usage *usageFields `json:"usage"`
}

type usageFields struct {
	InputTokens         *int64              `json:"input_tokens"`
	CachedInputTokens   *int64              `json:"cached_input_tokens"`
	OutputTokens        *int64              `json:"output_tokens"`
	InputTokensDetails  *usageInputDetails  `json:"input_tokens_details"`
	OutputTokensDetails *usageOutputDetails `json:"output_tokens_details"`
}

type usageInputDetails struct {
	CachedTokens *int64 `json:"cached_tokens"`
}

type usageOutputDetails struct {
	ReasoningTokens *int64 `json:"reasoning_tokens"`
}

func usageFromEnvelope(envelope usageEnvelope) (TokenUsage, bool) {
	usage := envelope.Usage
	if usage == nil && envelope.Response != nil {
		usage = envelope.Response.Usage
	}
	if usage == nil {
		return TokenUsage{}, false
	}

	hasUsageField := usage.InputTokens != nil || usage.CachedInputTokens != nil || usage.OutputTokens != nil ||
		(usage.InputTokensDetails != nil && usage.InputTokensDetails.CachedTokens != nil)
	if !hasUsageField {
		return TokenUsage{}, false
	}

	inputTotal := derefInt64(usage.InputTokens)
	cached := derefInt64(usage.CachedInputTokens)
	if cached == 0 && usage.InputTokensDetails != nil {
		cached = derefInt64(usage.InputTokensDetails.CachedTokens)
	}
	if cached < 0 {
		cached = 0
	}

	input := inputTotal - cached
	if input < 0 {
		input = inputTotal
	}
	if input < 0 {
		input = 0
	}

	output := max(derefInt64(usage.OutputTokens), 0)
	reasoning := int64(0)
	if usage.OutputTokensDetails != nil {
		reasoning = derefInt64(usage.OutputTokensDetails.ReasoningTokens)
	}
	if reasoning < 0 {
		reasoning = 0
	}

	return TokenUsage{
		InputTokens:     input,
		CachedTokens:    cached,
		OutputTokens:    output,
		ReasoningTokens: reasoning,
	}, true
}

func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

type sseUsageCapture struct {
	buffer       bytes.Buffer
	currentEvent []byte
	usage        TokenUsage
	found        bool
}

func newSSEUsageCapture() *sseUsageCapture {
	return &sseUsageCapture{}
}

func (c *sseUsageCapture) Write(p []byte) (int, error) {
	if _, err := c.buffer.Write(p); err != nil {
		return 0, err
	}

	for {
		line, err := c.buffer.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				if len(line) > 0 {
					// bytes.Buffer.Write never returns an error.
					_, _ = c.buffer.Write(line)
				}
				break
			}
			return len(p), err
		}

		payload := bytes.TrimSpace(line)
		if len(payload) == 0 {
			c.currentEvent = nil
			continue
		}
		if bytes.HasPrefix(payload, sseEventPrefix) {
			c.currentEvent = bytes.TrimSpace(bytes.TrimPrefix(payload, sseEventPrefix))
			continue
		}
		if !bytes.HasPrefix(payload, sseDataPrefix) {
			continue
		}
		if !bytes.Equal(c.currentEvent, sseResponseCompletedEvent) {
			continue
		}
		payload = bytes.TrimSpace(bytes.TrimPrefix(payload, sseDataPrefix))
		if len(payload) == 0 || bytes.Equal(payload, sseDonePayload) {
			continue
		}
		usage, ok := extractTokenUsageFromJSON(payload)
		if !ok {
			continue
		}
		c.usage = usage
		c.found = true
	}

	return len(p), nil
}

func (c *sseUsageCapture) Usage() (TokenUsage, bool) {
	if c == nil || !c.found {
		return TokenUsage{}, false
	}
	return c.usage, true
}
