package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
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
	s.usageSink.Record(UsageRecord{
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
	})
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
	onUsage       func(TokenUsage)
	usage         TokenUsage
	found         bool
}

func newWebsocketUsageCapture(onUsage func(TokenUsage)) *websocketUsageCapture {
	return &websocketUsageCapture{onUsage: onUsage}
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

func websocketFrameHeaderLen(header []byte) int {
	if len(header) < 2 {
		return 2
	}
	need := 2
	switch header[1] & 0x7F {
	case 126:
		need += 2
	case 127:
		need += 8
	}
	if header[1]&0x80 != 0 {
		need += 4
	}
	return need
}

func (c *websocketUsageCapture) beginFrame() {
	first := c.header[0]
	second := c.header[1]

	payloadLen := uint64(second & 0x7F)
	offset := 2
	switch payloadLen {
	case 126:
		payloadLen = uint64(binary.BigEndian.Uint16(c.header[offset : offset+2]))
		offset += 2
	case 127:
		payloadLen = binary.BigEndian.Uint64(c.header[offset : offset+8])
		offset += 8
	}

	c.frameActive = true
	c.frameFin = first&0x80 != 0
	c.frameOpcode = first & 0x0F
	c.framePayloadRemaining = payloadLen
	c.frameMasked = second&0x80 != 0
	c.frameMaskOffset = 0
	if c.frameMasked {
		copy(c.frameMask[:], c.header[offset:offset+4])
	} else {
		clear(c.frameMask[:])
	}
	c.headerLen = 0
	c.headerNeed = 0

	switch c.frameOpcode {
	case websocketOpcodeText:
		c.resetUnexpectedMessage()
		c.messageOpcode = websocketOpcodeText
		c.messageText = newStreamingWebsocketTextUsageCapture()
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
	}
}

func (c *websocketUsageCapture) finishTextMessage() {
	defer c.resetMessage()
	if c.messageText == nil {
		return
	}
	usage, ok, err := c.messageText.Finish()
	c.messageText = nil
	if err != nil || !ok {
		return
	}
	c.usage = usage
	c.found = true
	if c.onUsage != nil {
		c.onUsage(usage)
	}
}

func (c *websocketUsageCapture) discardTextMessage() {
	if c.messageText == nil {
		return
	}
	c.messageText.Abort()
	c.messageText = nil
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

func applyWebsocketMask(payload []byte, mask []byte) {
	applyWebsocketMaskFromOffset(payload, mask, 0)
}

func applyWebsocketMaskFromOffset(payload []byte, mask []byte, offset uint64) {
	for i := range payload {
		payload[i] ^= mask[(offset+uint64(i))%uint64(len(mask))]
	}
}
