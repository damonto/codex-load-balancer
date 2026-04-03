package plus

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
)

func sentinelSolveTurnstileToken(ctx context.Context, dx, proof string) (string, error) {
	solver, err := newTurnstileSolver(ctx, dx, proof)
	if err != nil {
		return "", fmt.Errorf("build turnstile solver: %w", err)
	}

	result, err := solver.solve()
	if err != nil {
		return "", fmt.Errorf("solve turnstile token: %w", err)
	}
	if err := sentinelValidateTurnstileToken(result); err != nil {
		return "", err
	}
	return result, nil
}

func sentinelValidateTurnstileToken(result string) error {
	if decoded, err := base64.StdEncoding.DecodeString(result); err == nil {
		text := strings.TrimSpace(string(decoded))
		lower := strings.ToLower(text)
		if strings.Contains(lower, "syntaxerror") || strings.Contains(lower, "error:") || strings.Contains(lower, "not valid json") {
			return fmt.Errorf("turnstile solver returned encoded error: %s", text)
		}
	}
	if len(result) < 128 {
		return fmt.Errorf("turnstile solver returned suspiciously short token (%d bytes)", len(result))
	}
	return nil
}
