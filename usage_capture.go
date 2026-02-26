package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"
)

type TokenUsage struct {
	InputTokens    int64 `json:"input_tokens"`
	CachedTokens   int64 `json:"cached_tokens"`
	OutputTokens   int64 `json:"output_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

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
	if value := strings.TrimSpace(token.AccountID); value != "" {
		return value
	}
	return token.ID
}

func accountKeyFromRef(ref TokenRef) string {
	if value := strings.TrimSpace(ref.AccountID); value != "" {
		return value
	}
	return ref.ID
}

func extractTokenUsageFromBody(body []byte) (TokenUsage, bool) {
	if usage, ok := extractTokenUsageFromJSON(body); ok {
		return usage, ok
	}
	capture := newSSEUsageCapture()
	_, _ = capture.Write(body)
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

	output := derefInt64(usage.OutputTokens)
	if output < 0 {
		output = 0
	}

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
	buffer bytes.Buffer
	usage  TokenUsage
	found  bool
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
				c.buffer.Write(line)
				break
			}
			return len(p), nil
		}

		payload := strings.TrimSpace(string(line))
		if !strings.HasPrefix(payload, "data:") {
			continue
		}
		payload = strings.TrimSpace(strings.TrimPrefix(payload, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		usage, ok := extractTokenUsageFromJSON([]byte(payload))
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
