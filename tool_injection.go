package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

const imageGenerationToolType = "image_generation"

type responseToolInjection struct {
	toolType          string
	spec              json.RawMessage
	skipPlanTypes     []string
	skipModelSuffixes []string
}

type responseToolInjectionContext struct {
	planType string
}

// Hosted tools do not share one schema, so keep each spec as its API JSON.
var defaultResponseToolInjections = []responseToolInjection{
	{
		toolType:          imageGenerationToolType,
		spec:              json.RawMessage(`{"type":"image_generation","output_format":"png"}`),
		skipPlanTypes:     []string{"free"},
		skipModelSuffixes: []string{"spark"},
	},
}

func shouldInjectResponseTools(path string) bool {
	return hasAPIPathPrefix(path, "/responses")
}

func injectResponseTools(body []byte, ctx responseToolInjectionContext) ([]byte, bool, error) {
	return injectTools(body, defaultResponseToolInjections, ctx)
}

func injectTools(body []byte, additions []responseToolInjection, ctx responseToolInjectionContext) ([]byte, bool, error) {
	if len(body) == 0 || len(additions) == 0 {
		return body, false, nil
	}

	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		return body, false, nil
	}
	if !canInjectResponseTools(request) {
		return body, false, nil
	}
	additions = filterResponseToolInjections(request, additions, ctx)
	if len(additions) == 0 {
		return body, false, nil
	}

	rawTools, ok := request["tools"]
	if !ok || string(rawTools) == "null" {
		updatedTools, err := marshalToolInjections(additions)
		if err != nil {
			return body, false, err
		}
		request["tools"] = updatedTools

		updated, err := json.Marshal(request)
		if err != nil {
			return body, false, fmt.Errorf("marshal response tool request: %w", err)
		}
		return updated, true, nil
	}

	var tools []json.RawMessage
	if err := json.Unmarshal(rawTools, &tools); err != nil {
		return body, false, nil
	}

	updatedTools, changed := appendMissingTools(tools, additions)
	if !changed {
		return body, false, nil
	}
	marshaledTools, err := json.Marshal(updatedTools)
	if err != nil {
		return body, false, fmt.Errorf("marshal response tools: %w", err)
	}
	request["tools"] = marshaledTools

	updated, err := json.Marshal(request)
	if err != nil {
		return body, false, fmt.Errorf("marshal response tool request: %w", err)
	}
	return updated, true, nil
}

func canInjectResponseTools(request map[string]json.RawMessage) bool {
	if rawType, ok := request["type"]; ok {
		var requestType string
		if err := json.Unmarshal(rawType, &requestType); err != nil {
			return false
		}
		return requestType == "response.create"
	}
	if _, ok := request["tools"]; ok {
		return true
	}
	if _, ok := request["model"]; ok {
		return true
	}
	if _, ok := request["input"]; ok {
		return true
	}
	return false
}

func filterResponseToolInjections(request map[string]json.RawMessage, additions []responseToolInjection, ctx responseToolInjectionContext) []responseToolInjection {
	model := responseRequestModel(request)
	filtered := make([]responseToolInjection, 0, len(additions))
	for _, addition := range additions {
		if addition.skipsPlan(ctx.planType) || addition.skipsModel(model) {
			continue
		}
		filtered = append(filtered, addition)
	}
	return filtered
}

func responseRequestModel(request map[string]json.RawMessage) string {
	rawModel, ok := request["model"]
	if !ok {
		return ""
	}
	var model string
	if err := json.Unmarshal(rawModel, &model); err != nil {
		return ""
	}
	return model
}

func responseToolInjectionContextForToken(token TokenState) responseToolInjectionContext {
	return responseToolInjectionContext{planType: token.PlanType}
}

func (i responseToolInjection) skipsPlan(planType string) bool {
	planType = strings.TrimSpace(planType)
	if planType == "" {
		return false
	}
	return slices.ContainsFunc(i.skipPlanTypes, func(skip string) bool {
		return strings.EqualFold(strings.TrimSpace(skip), planType)
	})
}

func (i responseToolInjection) skipsModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return false
	}
	return slices.ContainsFunc(i.skipModelSuffixes, func(suffix string) bool {
		return strings.HasSuffix(model, strings.ToLower(strings.TrimSpace(suffix)))
	})
}

func marshalToolInjections(additions []responseToolInjection) ([]byte, error) {
	tools := make([]json.RawMessage, 0, len(additions))
	for _, addition := range additions {
		tools = append(tools, addition.spec)
	}
	encoded, err := json.Marshal(tools)
	if err != nil {
		return nil, fmt.Errorf("marshal response tools: %w", err)
	}
	return encoded, nil
}

func appendMissingTools(tools []json.RawMessage, additions []responseToolInjection) ([]json.RawMessage, bool) {
	seen := make(map[string]bool, len(tools)+len(additions))
	for _, rawTool := range tools {
		if toolType, ok := responseToolType(rawTool); ok {
			seen[toolType] = true
		}
	}

	changed := false
	for _, addition := range additions {
		if seen[addition.toolType] {
			continue
		}
		tools = append(tools, addition.spec)
		seen[addition.toolType] = true
		changed = true
	}
	return tools, changed
}

func responseToolType(rawTool json.RawMessage) (string, bool) {
	var tool struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(rawTool, &tool); err != nil {
		return "", false
	}
	if tool.Type == "" {
		return "", false
	}
	return tool.Type, true
}
