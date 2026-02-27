package main

import (
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
