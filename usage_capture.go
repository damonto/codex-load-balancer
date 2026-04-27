package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
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
var sseResponseFailedEvent = []byte("response.failed")
var sseErrorEvent = []byte("error")
var sseDonePayload = []byte("[DONE]")

const (
	websocketOpcodeContinuation = 0x0
	websocketOpcodeText         = 0x1
	websocketOpcodeBinary       = 0x2
	websocketOpcodeClose        = 0x8
	websocketOpcodePing         = 0x9
	websocketOpcodePong         = 0xA
)

func (u TokenUsage) TotalTokens() int64 {
	return u.InputTokens + u.CachedTokens + u.OutputTokens
}

func (s *Server) recordTokenUsage(token TokenState, path string, statusCode int, stream bool, usage TokenUsage) {
	if s == nil || s.usageSink == nil {
		return
	}
	accountKey := accountKeyFromToken(token)
	if accountKey == "" {
		return
	}

	rec := UsageRecord{
		AccountKey:      accountKey,
		TokenID:         token.ID,
		Path:            path,
		StatusCode:      statusCode,
		IsStream:        stream,
		InputTokens:     usage.InputTokens,
		CachedTokens:    usage.CachedTokens,
		OutputTokens:    usage.OutputTokens,
		ReasoningTokens: usage.ReasoningTokens,
		CreatedAt:       time.Now().UTC(),
	}
	if err := s.usageSink.Record(rec); err != nil && !errors.Is(err, errUsageSinkStopped) {
		slog.Warn("queue usage record", "account", rec.AccountKey, "token", rec.TokenID, "err", err)
	}
}

func accountKeyFromToken(token TokenState) string {
	return accountKey(token.AccountID)
}

func accountKeyFromRef(ref TokenRef) string {
	return accountKey(ref.AccountID)
}

func accountKey(accountID string) string {
	return accountID
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

func extractCompletedTokenUsageFromJSON(data []byte) (TokenUsage, bool) {
	usage, ok, err := extractCompletedTokenUsageFromReader(bytes.NewReader(data))
	if err != nil {
		return TokenUsage{}, false
	}
	return usage, ok
}

func extractCompletedTokenUsageFromReader(r io.Reader) (TokenUsage, bool, error) {
	var envelope struct {
		Type     string                `json:"type"`
		Usage    *usageFields          `json:"usage"`
		Response *usageResponseWrapper `json:"response"`
	}
	if err := json.NewDecoder(r).Decode(&envelope); err != nil {
		return TokenUsage{}, false, err
	}
	if envelope.Type != "" && envelope.Type != "response.completed" {
		return TokenUsage{}, false, nil
	}
	usage, ok := usageFromEnvelope(usageEnvelope{
		Usage:    envelope.Usage,
		Response: envelope.Response,
	})
	return usage, ok, nil
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
	limitFound   bool
	onLimit      func()
}

func newSSEUsageCapture() *sseUsageCapture {
	return &sseUsageCapture{}
}

func newSSEUsageCaptureWithLimit(onLimit func()) *sseUsageCapture {
	return &sseUsageCapture{onLimit: onLimit}
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
		payload = bytes.TrimSpace(bytes.TrimPrefix(payload, sseDataPrefix))
		if len(payload) == 0 || bytes.Equal(payload, sseDonePayload) {
			continue
		}
		if c.shouldInspectLimitPayload(payload) && isLimitErrorBody(payload) {
			c.markLimit()
		}
		if !bytes.Equal(c.currentEvent, sseResponseCompletedEvent) {
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

func (c *sseUsageCapture) LimitError() bool {
	return c != nil && c.limitFound
}

func (c *sseUsageCapture) shouldInspectLimitPayload(payload []byte) bool {
	return bytes.Equal(c.currentEvent, sseResponseFailedEvent) ||
		bytes.Equal(c.currentEvent, sseErrorEvent)
}

func (c *sseUsageCapture) markLimit() {
	if c.limitFound {
		return
	}
	c.limitFound = true
	if c.onLimit != nil {
		c.onLimit()
	}
}

type websocketUsageCapture struct {
	header     [14]byte
	headerLen  int
	headerNeed int

	frameActive           bool
	frameFin              bool
	frameOpcode           byte
	framePayloadRemaining uint64
	frameMask             [4]byte
	frameMaskOffset       uint64
	frameMasked           bool

	messageOpcode byte
	messageText   *websocketTextUsageCapture
	messageLimit  *websocketTextLimitCapture
	onUsage       func(TokenUsage)
	onLimit       func()
	usage         TokenUsage
	found         bool
	limitFound    bool
}

func newWebsocketUsageCapture(onUsage func(TokenUsage), onLimit func()) *websocketUsageCapture {
	return &websocketUsageCapture{onUsage: onUsage, onLimit: onLimit}
}

func (c *websocketUsageCapture) Write(p []byte) (int, error) {
	written := len(p)
	if c == nil {
		return written, nil
	}
	for len(p) > 0 {
		if !c.frameActive {
			consumed := c.consumeHeader(p)
			p = p[consumed:]
			if !c.frameActive {
				continue
			}
			if c.framePayloadRemaining == 0 {
				c.finishFrame()
				continue
			}
		}

		chunkSize := len(p)
		if uint64(chunkSize) > c.framePayloadRemaining {
			chunkSize = int(c.framePayloadRemaining)
		}
		c.consumePayload(p[:chunkSize])
		p = p[chunkSize:]
		c.framePayloadRemaining -= uint64(chunkSize)
		if c.framePayloadRemaining == 0 {
			c.finishFrame()
		}
	}
	return written, nil
}

func (c *websocketUsageCapture) Usage() (TokenUsage, bool) {
	if c == nil || !c.found {
		return TokenUsage{}, false
	}
	return c.usage, true
}

func (c *websocketUsageCapture) consumeHeader(p []byte) int {
	consumed := 0
	for len(p) > 0 && !c.frameActive {
		c.header[c.headerLen] = p[0]
		c.headerLen++
		p = p[1:]
		consumed++
		if c.headerLen == 2 {
			c.headerNeed = websocketFrameHeaderLen(c.header[:2])
		}
		if c.headerNeed > 0 && c.headerLen == c.headerNeed {
			c.beginFrame()
		}
	}
	return consumed
}

func (c *websocketUsageCapture) beginFrame() {
	header, err := parseWebsocketFrameHeader(c.header[:c.headerNeed])
	if err != nil {
		c.headerLen = 0
		c.headerNeed = 0
		return
	}

	c.frameActive = true
	c.frameFin = header.first&0x80 != 0
	c.frameOpcode = header.first & 0x0F
	c.framePayloadRemaining = header.payloadLen
	c.frameMasked = header.masked
	c.frameMaskOffset = 0
	c.frameMask = header.mask
	c.headerLen = 0
	c.headerNeed = 0

	switch c.frameOpcode {
	case websocketOpcodeText:
		c.resetUnexpectedMessage()
		c.messageOpcode = websocketOpcodeText
		c.messageText = newStreamingWebsocketTextUsageCapture()
		c.messageLimit = newWebsocketTextLimitCapture()
	case websocketOpcodeBinary:
		c.resetUnexpectedMessage()
		c.messageOpcode = websocketOpcodeBinary
	}
}

func (c *websocketUsageCapture) consumePayload(payload []byte) {
	if len(payload) == 0 {
		return
	}
	decoded := payload
	if c.frameMasked {
		decoded = append([]byte(nil), payload...)
		applyWebsocketMaskFromOffset(decoded, c.frameMask[:], c.frameMaskOffset)
		c.frameMaskOffset += uint64(len(decoded))
	}

	switch c.frameOpcode {
	case websocketOpcodeText:
		c.writeTextPayload(decoded)
	case websocketOpcodeContinuation:
		if c.messageOpcode == websocketOpcodeText {
			c.writeTextPayload(decoded)
		}
	}
}

func (c *websocketUsageCapture) finishFrame() {
	opcode := c.frameOpcode
	fin := c.frameFin
	c.frameActive = false
	c.frameFin = false
	c.frameOpcode = 0
	c.framePayloadRemaining = 0
	c.frameMaskOffset = 0
	c.frameMasked = false

	switch opcode {
	case websocketOpcodeText:
		if fin {
			c.finishTextMessage()
		}
	case websocketOpcodeBinary:
		if fin {
			c.resetMessage()
		}
	case websocketOpcodeContinuation:
		if !fin {
			return
		}
		if c.messageOpcode == websocketOpcodeText {
			c.finishTextMessage()
			return
		}
		if c.messageOpcode == websocketOpcodeBinary {
			c.resetMessage()
		}
	}
}

func (c *websocketUsageCapture) writeTextPayload(payload []byte) {
	if c.messageText == nil {
		return
	}
	if err := c.messageText.Write(payload); err != nil {
		c.discardTextMessage()
		return
	}
	if c.messageLimit != nil {
		c.messageLimit.Write(payload)
	}
}

func (c *websocketUsageCapture) finishTextMessage() {
	defer c.resetMessage()
	if c.messageText == nil && c.messageLimit == nil {
		return
	}
	if c.messageLimit != nil && c.messageLimit.LimitError() {
		c.markLimit()
	}
	var usage TokenUsage
	var ok bool
	var err error
	if c.messageText != nil {
		usage, ok, err = c.messageText.Finish()
	}
	c.messageText = nil
	c.messageLimit = nil
	if err != nil || !ok {
		return
	}
	c.usage = usage
	c.found = true
	if c.onUsage != nil {
		c.onUsage(usage)
	}
}

func (c *websocketUsageCapture) LimitError() bool {
	return c != nil && c.limitFound
}

func (c *websocketUsageCapture) markLimit() {
	if c.limitFound {
		return
	}
	c.limitFound = true
	if c.onLimit != nil {
		c.onLimit()
	}
}

func (c *websocketUsageCapture) discardTextMessage() {
	if c.messageText != nil {
		c.messageText.Abort()
		c.messageText = nil
	}
	c.messageLimit = nil
}

func (c *websocketUsageCapture) resetMessage() {
	c.discardTextMessage()
	c.messageOpcode = 0
}

func (c *websocketUsageCapture) resetUnexpectedMessage() {
	if c.messageOpcode != 0 {
		c.resetMessage()
	}
}

type websocketTextUsageCapture struct {
	writer   *io.PipeWriter
	resultCh chan websocketTextUsageResult
}

type websocketTextUsageResult struct {
	usage TokenUsage
	ok    bool
	err   error
}

func newStreamingWebsocketTextUsageCapture() *websocketTextUsageCapture {
	reader, writer := io.Pipe()
	resultCh := make(chan websocketTextUsageResult, 1)
	go func() {
		usage, ok, err := extractCompletedTokenUsageFromReader(reader)
		resultCh <- websocketTextUsageResult{usage: usage, ok: ok, err: err}
	}()
	return &websocketTextUsageCapture{writer: writer, resultCh: resultCh}
}

func (c *websocketTextUsageCapture) Write(payload []byte) error {
	if c == nil || c.writer == nil || len(payload) == 0 {
		return nil
	}
	_, err := c.writer.Write(payload)
	return err
}

func (c *websocketTextUsageCapture) Finish() (TokenUsage, bool, error) {
	if c == nil || c.writer == nil {
		return TokenUsage{}, false, nil
	}
	if err := c.writer.Close(); err != nil {
		return TokenUsage{}, false, err
	}
	result := <-c.resultCh
	return result.usage, result.ok, result.err
}

func (c *websocketTextUsageCapture) Abort() {
	if c == nil || c.writer == nil {
		return
	}
	_ = c.writer.CloseWithError(io.ErrClosedPipe)
	<-c.resultCh
}

type websocketTextLimitCapture struct {
	buffer   bytes.Buffer
	tooLarge bool
}

func newWebsocketTextLimitCapture() *websocketTextLimitCapture {
	return &websocketTextLimitCapture{}
}

func (c *websocketTextLimitCapture) Write(payload []byte) {
	if c == nil || c.tooLarge || len(payload) == 0 {
		return
	}
	if c.buffer.Len()+len(payload) > defaultMaxRequestBody {
		c.tooLarge = true
		c.buffer.Reset()
		return
	}
	_, _ = c.buffer.Write(payload)
}

func (c *websocketTextLimitCapture) LimitError() bool {
	if c == nil || c.tooLarge {
		return false
	}
	return isLimitErrorBody(c.buffer.Bytes())
}

func applyWebsocketMask(payload []byte, mask []byte) {
	applyWebsocketMaskFromOffset(payload, mask, 0)
}

func applyWebsocketMaskFromOffset(payload []byte, mask []byte, offset uint64) {
	for i := range payload {
		payload[i] ^= mask[(offset+uint64(i))%uint64(len(mask))]
	}
}
