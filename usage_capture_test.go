package main

import (
	"bytes"
	"encoding/binary"
	"slices"
	"testing"
	"time"
)

func TestExtractTokenUsageFromJSON(t *testing.T) {
	tests := []struct {
		name string
		body string
		want TokenUsage
		ok   bool
	}{
		{
			name: "top level usage with cached_input_tokens",
			body: `{"usage":{"input_tokens":120,"cached_input_tokens":20,"output_tokens":80}}`,
			want: TokenUsage{InputTokens: 100, CachedTokens: 20, OutputTokens: 80},
			ok:   true,
		},
		{
			name: "top level usage with output_tokens_details reasoning",
			body: `{"usage":{"input_tokens":120,"cached_input_tokens":20,"output_tokens":80,"output_tokens_details":{"reasoning_tokens":50}}}`,
			want: TokenUsage{InputTokens: 100, CachedTokens: 20, OutputTokens: 80, ReasoningTokens: 50},
			ok:   true,
		},
		{
			name: "nested response usage with input details",
			body: `{"type":"response.completed","response":{"usage":{"input_tokens":55,"input_tokens_details":{"cached_tokens":15},"output_tokens":30}}}`,
			want: TokenUsage{InputTokens: 40, CachedTokens: 15, OutputTokens: 30},
			ok:   true,
		},
		{
			name: "nested response usage with reasoning tokens",
			body: `{"type":"response.completed","response":{"usage":{"input_tokens":55,"output_tokens":30,"output_tokens_details":{"reasoning_tokens":10}}}}`,
			want: TokenUsage{InputTokens: 55, OutputTokens: 30, ReasoningTokens: 10},
			ok:   true,
		},
		{
			name: "missing usage",
			body: `{"id":"resp_123"}`,
			ok:   false,
		},
		{
			name: "invalid json",
			body: `{"usage":`,
			ok:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := extractTokenUsageFromJSON([]byte(tt.body))
			if ok != tt.ok {
				t.Fatalf("extractTokenUsageFromJSON() ok = %v, want %v", ok, tt.ok)
			}
			if !tt.ok {
				return
			}
			if got != tt.want {
				t.Fatalf("extractTokenUsageFromJSON() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestExtractTokenUsageFromBodySSERequiresResponseCompletedEvent(t *testing.T) {
	tests := []struct {
		name string
		body string
		want TokenUsage
		ok   bool
	}{
		{
			name: "captures usage for response completed event",
			body: "event: response.completed\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":37,\"output_tokens\":11,\"output_tokens_details\":{\"reasoning_tokens\":0}}}}\n\n",
			want: TokenUsage{InputTokens: 37, OutputTokens: 11, ReasoningTokens: 0},
			ok:   true,
		},
		{
			name: "ignores usage for non completed event",
			body: "event: response.output_text.delta\n" +
				"data: {\"type\":\"response.output_text.delta\",\"response\":{\"usage\":{\"input_tokens\":37,\"output_tokens\":11}}}\n\n",
			ok: false,
		},
		{
			name: "ignores data without event field",
			body: "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":37,\"output_tokens\":11}}}\n\n",
			ok:   false,
		},
		{
			name: "event reset by empty line",
			body: "event: response.completed\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":37,\"output_tokens\":11}}}\n\n",
			ok: false,
		},
		{
			name: "captures last completed event usage",
			body: "event: response.output_text.delta\n" +
				"data: {\"type\":\"response.output_text.delta\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n" +
				"event: response.completed\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":3}}}\n\n",
			want: TokenUsage{InputTokens: 5, OutputTokens: 3},
			ok:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := extractTokenUsageFromBody([]byte(tt.body))
			if ok != tt.ok {
				t.Fatalf("extractTokenUsageFromBody() ok = %v, want %v", ok, tt.ok)
			}
			if !tt.ok {
				return
			}
			if got != tt.want {
				t.Fatalf("extractTokenUsageFromBody() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestWebsocketUsageCapture(t *testing.T) {
	completed := []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":55,"input_tokens_details":{"cached_tokens":15},"output_tokens":30,"output_tokens_details":{"reasoning_tokens":10}}}}`)
	nonCompleted := []byte(`{"type":"response.output_text.delta","response":{"usage":{"input_tokens":1,"output_tokens":1}}}`)

	firstHalf := completed[:len(completed)/2]
	secondHalf := completed[len(completed)/2:]
	largeCompleted := []byte(`{"type":"response.completed","response":{"padding":"` + string(bytes.Repeat([]byte("x"), 2<<20)) + `","usage":{"input_tokens":81,"cached_input_tokens":1,"output_tokens":19}}}`)

	tests := []struct {
		name         string
		chunks       [][]byte
		want         TokenUsage
		wantReported []TokenUsage
		ok           bool
	}{
		{
			name: "single frame split across writes",
			chunks: [][]byte{
				websocketFrame(t, websocketOpcodeText, true, false, completed)[:5],
				websocketFrame(t, websocketOpcodeText, true, false, completed)[5:17],
				websocketFrame(t, websocketOpcodeText, true, false, completed)[17:],
			},
			want:         TokenUsage{InputTokens: 40, CachedTokens: 15, OutputTokens: 30, ReasoningTokens: 10},
			wantReported: []TokenUsage{{InputTokens: 40, CachedTokens: 15, OutputTokens: 30, ReasoningTokens: 10}},
			ok:           true,
		},
		{
			name: "fragmented text with control frame",
			chunks: [][]byte{bytes.Join([][]byte{
				websocketFrame(t, websocketOpcodeText, false, false, firstHalf),
				websocketFrame(t, websocketOpcodePing, true, false, []byte("keepalive")),
				websocketFrame(t, websocketOpcodeContinuation, true, false, secondHalf),
			}, nil)},
			want:         TokenUsage{InputTokens: 40, CachedTokens: 15, OutputTokens: 30, ReasoningTokens: 10},
			wantReported: []TokenUsage{{InputTokens: 40, CachedTokens: 15, OutputTokens: 30, ReasoningTokens: 10}},
			ok:           true,
		},
		{
			name:         "masked frame",
			chunks:       [][]byte{websocketFrame(t, websocketOpcodeText, true, true, completed)},
			want:         TokenUsage{InputTokens: 40, CachedTokens: 15, OutputTokens: 30, ReasoningTokens: 10},
			wantReported: []TokenUsage{{InputTokens: 40, CachedTokens: 15, OutputTokens: 30, ReasoningTokens: 10}},
			ok:           true,
		},
		{
			name:         "large completed frame stays observable",
			chunks:       [][]byte{websocketFrame(t, websocketOpcodeText, true, false, largeCompleted)},
			want:         TokenUsage{InputTokens: 80, CachedTokens: 1, OutputTokens: 19},
			wantReported: []TokenUsage{{InputTokens: 80, CachedTokens: 1, OutputTokens: 19}},
			ok:           true,
		},
		{
			name:   "ignores non completed event",
			chunks: [][]byte{websocketFrame(t, websocketOpcodeText, true, false, nonCompleted)},
			ok:     false,
		},
		{
			name:   "ignores binary frame",
			chunks: [][]byte{websocketFrame(t, websocketOpcodeBinary, true, false, completed)},
			ok:     false,
		},
		{
			name: "captures last completed event",
			chunks: [][]byte{bytes.Join([][]byte{
				websocketFrame(t, websocketOpcodeText, true, false, nonCompleted),
				websocketFrame(t, websocketOpcodeText, true, false, completed),
			}, nil)},
			want:         TokenUsage{InputTokens: 40, CachedTokens: 15, OutputTokens: 30, ReasoningTokens: 10},
			wantReported: []TokenUsage{{InputTokens: 40, CachedTokens: 15, OutputTokens: 30, ReasoningTokens: 10}},
			ok:           true,
		},
		{
			name: "reports each completed event",
			chunks: [][]byte{bytes.Join([][]byte{
				websocketFrame(t, websocketOpcodeText, true, false, []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":20,"output_tokens":5}}}`)),
				websocketFrame(t, websocketOpcodeText, true, false, []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":33,"cached_input_tokens":3,"output_tokens":9}}}`)),
			}, nil)},
			want: TokenUsage{InputTokens: 30, CachedTokens: 3, OutputTokens: 9},
			wantReported: []TokenUsage{
				{InputTokens: 20, OutputTokens: 5},
				{InputTokens: 30, CachedTokens: 3, OutputTokens: 9},
			},
			ok: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reported := make([]TokenUsage, 0, len(tt.wantReported))
			capture := newWebsocketUsageCapture(func(usage TokenUsage) {
				reported = append(reported, usage)
			})
			for _, chunk := range tt.chunks {
				if _, err := capture.Write(chunk); err != nil {
					t.Fatalf("Write() error = %v", err)
				}
			}

			got, ok := capture.Usage()
			if ok != tt.ok {
				t.Fatalf("Usage() ok = %v, want %v", ok, tt.ok)
			}
			if !tt.ok {
				if len(reported) != 0 {
					t.Fatalf("reported usages = %+v, want none", reported)
				}
				return
			}
			if got != tt.want {
				t.Fatalf("Usage() = %+v, want %+v", got, tt.want)
			}
			if !slices.Equal(reported, tt.wantReported) {
				t.Fatalf("reported usages = %+v, want %+v", reported, tt.wantReported)
			}
		})
	}
}

func websocketFrame(t *testing.T, opcode byte, fin bool, masked bool, payload []byte) []byte {
	t.Helper()

	var frame bytes.Buffer
	first := opcode
	if fin {
		first |= 0x80
	}
	if err := frame.WriteByte(first); err != nil {
		t.Fatalf("WriteByte(first): %v", err)
	}

	second := byte(0)
	if masked {
		second |= 0x80
	}
	switch n := len(payload); {
	case n < 126:
		second |= byte(n)
		if err := frame.WriteByte(second); err != nil {
			t.Fatalf("WriteByte(second): %v", err)
		}
	case n <= 0xFFFF:
		second |= 126
		if err := frame.WriteByte(second); err != nil {
			t.Fatalf("WriteByte(second): %v", err)
		}
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(n))
		if _, err := frame.Write(ext[:]); err != nil {
			t.Fatalf("Write(ext16): %v", err)
		}
	default:
		second |= 127
		if err := frame.WriteByte(second); err != nil {
			t.Fatalf("WriteByte(second): %v", err)
		}
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		if _, err := frame.Write(ext[:]); err != nil {
			t.Fatalf("Write(ext64): %v", err)
		}
	}

	if masked {
		mask := []byte{1, 2, 3, 4}
		if _, err := frame.Write(mask); err != nil {
			t.Fatalf("Write(mask): %v", err)
		}
		maskedPayload := append([]byte(nil), payload...)
		for i := range maskedPayload {
			maskedPayload[i] ^= mask[i%len(mask)]
		}
		if _, err := frame.Write(maskedPayload); err != nil {
			t.Fatalf("Write(masked payload): %v", err)
		}
		return frame.Bytes()
	}

	if _, err := frame.Write(payload); err != nil {
		t.Fatalf("Write(payload): %v", err)
	}
	return frame.Bytes()
}

func TestWeekStartUTC(t *testing.T) {
	tests := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{
			name: "monday stays monday",
			now:  time.Date(2026, 2, 23, 15, 4, 5, 0, time.UTC),
			want: time.Date(2026, 2, 23, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "thursday maps to monday",
			now:  time.Date(2026, 2, 26, 9, 30, 0, 0, time.UTC),
			want: time.Date(2026, 2, 23, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "sunday maps to previous monday",
			now:  time.Date(2026, 3, 1, 23, 0, 0, 0, time.UTC),
			want: time.Date(2026, 2, 23, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := weekStartUTC(tt.now)
			if !got.Equal(tt.want) {
				t.Fatalf("weekStartUTC() = %s, want %s", got, tt.want)
			}
		})
	}
}
