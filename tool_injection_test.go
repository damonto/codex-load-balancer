package main

import (
	"encoding/json"
	"testing"
)

func TestInjectResponseTools(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		planType    string
		wantChanged bool
		wantTool    bool
	}{
		{
			name:        "empty tools array",
			body:        `{"model":"gpt-5.4","tools":[]}`,
			wantChanged: true,
			wantTool:    true,
		},
		{
			name:        "missing tools on response request",
			body:        `{"model":"gpt-5.4","input":"draw"}`,
			wantChanged: true,
			wantTool:    true,
		},
		{
			name:        "existing image generation tool",
			body:        `{"model":"gpt-5.4","tools":[{"type":"image_generation","output_format":"png"}]}`,
			wantChanged: false,
			wantTool:    true,
		},
		{
			name:        "free plan skips image generation tool",
			body:        `{"model":"gpt-5.4","tools":[]}`,
			planType:    "free",
			wantChanged: false,
			wantTool:    false,
		},
		{
			name:        "spark model skips image generation tool",
			body:        `{"model":"gpt-5.4-spark","tools":[]}`,
			wantChanged: false,
			wantTool:    false,
		},
		{
			name:        "non create websocket event",
			body:        `{"type":"response.cancel","response_id":"resp_1"}`,
			wantChanged: false,
			wantTool:    false,
		},
		{
			name:        "invalid json",
			body:        `{"model":`,
			wantChanged: false,
			wantTool:    false,
		},
		{
			name:        "malformed tools field",
			body:        `{"model":"gpt-5.4","tools":{}}`,
			wantChanged: false,
			wantTool:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed, err := injectResponseTools([]byte(tt.body), responseToolInjectionContext{
				planType: tt.planType,
			})
			if err != nil {
				t.Fatalf("injectResponseTools() error = %v", err)
			}
			if changed != tt.wantChanged {
				t.Fatalf("changed = %v, want %v", changed, tt.wantChanged)
			}
			if hasToolType(got, imageGenerationToolType) != tt.wantTool {
				t.Fatalf("hasToolType() = %v, want %v; body=%s", hasToolType(got, imageGenerationToolType), tt.wantTool, string(got))
			}
		})
	}
}

func TestInjectToolsAddsOnlyMissingTypes(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		additions []responseToolInjection
		wantCount map[string]int
	}{
		{
			name: "adds missing tool and keeps existing tool once",
			body: `{"model":"gpt-5.4","tools":[{"type":"first_tool","enabled":true}]}`,
			additions: []responseToolInjection{
				{
					toolType: "first_tool",
					spec:     json.RawMessage(`{"type":"first_tool","enabled":true}`),
				},
				{
					toolType: "second_tool",
					spec:     json.RawMessage(`{"type":"second_tool","mode":"auto"}`),
				},
			},
			wantCount: map[string]int{
				"first_tool":  1,
				"second_tool": 1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed, err := injectTools([]byte(tt.body), tt.additions, responseToolInjectionContext{})
			if err != nil {
				t.Fatalf("injectTools() error = %v", err)
			}
			if !changed {
				t.Fatal("changed = false, want true")
			}
			for toolType, want := range tt.wantCount {
				if gotCount := countToolType(got, toolType); gotCount != want {
					t.Fatalf("%s count = %d, want %d; body=%s", toolType, gotCount, want, string(got))
				}
			}
		})
	}
}

func hasToolType(body []byte, toolType string) bool {
	var request struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return false
	}
	return toolsContainType(request.Tools, toolType)
}

func toolsContainType(tools []json.RawMessage, toolType string) bool {
	for _, rawTool := range tools {
		gotType, ok := responseToolType(rawTool)
		if ok && gotType == toolType {
			return true
		}
	}
	return false
}

func countToolType(body []byte, toolType string) int {
	var request struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return 0
	}
	count := 0
	for _, rawTool := range request.Tools {
		gotType, ok := responseToolType(rawTool)
		if ok && gotType == toolType {
			count++
		}
	}
	return count
}
