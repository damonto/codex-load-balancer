package plus

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type sentinelJSSolverInput struct {
	DX    string `json:"dx"`
	Proof string `json:"proof"`
}

func sentinelSolveTurnstileTokenWithJS(ctx context.Context, dx, proof string) (string, error) {
	if dx == "" {
		return "", errors.New("turnstile dx is empty")
	}
	if proof == "" {
		return "", errors.New("turnstile proof is empty")
	}

	return sentinelSolveTurnstileTokenWithJSSource(ctx, dx, proof, sentinelSDKScript)
}

func sentinelSolveTurnstileTokenWithJSSource(ctx context.Context, dx, proof, sdkSource string) (string, error) {
	if strings.TrimSpace(sdkSource) == "" {
		return "", errors.New("sdk source is empty")
	}

	workdir, err := os.MkdirTemp("", "sentinel-js-solver-*")
	if err != nil {
		return "", fmt.Errorf("create js solver dir: %w", err)
	}
	defer os.RemoveAll(workdir)

	runnerPath := filepath.Join(workdir, "runner.js")
	if err := os.WriteFile(runnerPath, []byte(sentinelSolverRunner), 0o600); err != nil {
		return "", fmt.Errorf("write js runner: %w", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "sdk.js"), []byte(sdkSource), 0o600); err != nil {
		return "", fmt.Errorf("write sdk.js: %w", err)
	}

	input, err := json.Marshal(sentinelJSSolverInput{
		DX:    dx,
		Proof: proof,
	})
	if err != nil {
		return "", fmt.Errorf("encode js solver input: %w", err)
	}

	cmd := exec.CommandContext(ctx, sentinelNodePath(), runnerPath)
	cmd.Dir = workdir
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = append(os.Environ(), sentinelNodeEnv(workdir)...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("run js solver: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return "", fmt.Errorf("run js solver: %w", err)
	}

	result := strings.TrimSpace(stdout.String())
	if result == "" {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("js solver returned empty result: %s", strings.TrimSpace(stderr.String()))
		}
		return "", errors.New("js solver returned empty result")
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
			return fmt.Errorf("js solver returned encoded error: %s", text)
		}
	}
	if len(result) < 128 {
		return fmt.Errorf("js solver returned suspiciously short token (%d bytes)", len(result))
	}
	return nil
}

func sentinelNodeEnv(workdir string) []string {
	return []string{
		"HOME=" + workdir,
		"XDG_CONFIG_HOME=" + workdir,
		"XDG_DATA_HOME=" + workdir,
	}
}

func sentinelNodePath() string {
	if value := os.Getenv("SENTINEL_NODE_PATH"); value != "" {
		return value
	}
	if path, err := exec.LookPath("node"); err == nil && !strings.Contains(path, string(filepath.Separator)+"shims"+string(filepath.Separator)+"node") {
		return path
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		pattern := filepath.Join(home, ".local", "share", "mise", "installs", "node", "*", "bin", "node")
		if matches, err := filepath.Glob(pattern); err == nil && len(matches) > 0 {
			sort.Strings(matches)
			return matches[len(matches)-1]
		}
	}
	return "node"
}
