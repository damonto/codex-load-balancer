package plus

import (
	"errors"
	"fmt"
	randv2 "math/rand/v2"
	"net/url"
	"strings"
)

func normalizeOptions(opts RegisterOptions) (registrationConfig, error) {
	cfg := registrationConfig{}
	cfg.DataDir = strings.TrimSpace(opts.DataDir)
	if cfg.DataDir == "" {
		cfg.DataDir = defaultDataDir
	}
	cfg.OTPWait = opts.OTPWait
	if cfg.OTPWait <= 0 {
		cfg.OTPWait = defaultOTPWait
	}
	cfg.OTPPoll = opts.OTPPoll
	if cfg.OTPPoll <= 0 {
		cfg.OTPPoll = defaultOTPPoll
	}
	cfg.TelegramBotToken = strings.TrimSpace(opts.TelegramBotToken)
	cfg.TelegramChatID = strings.TrimSpace(opts.TelegramChatID)

	proxy := strings.TrimSpace(opts.Proxy)
	if proxy == "" {
		proxy = pickRandomProxy(opts.RegistrationProxies)
	}
	if proxy == "" {
		return registrationConfig{}, errors.New("proxy pool is empty")
	}
	proxy, err := injectProxySessionID(proxy)
	if err != nil {
		return registrationConfig{}, fmt.Errorf("inject proxy session id: %w", err)
	}
	if _, err := url.Parse(proxy); err != nil {
		return registrationConfig{}, fmt.Errorf("parse proxy: %w", err)
	}
	cfg.Proxy = proxy

	return cfg, nil
}

func injectProxySessionID(proxy string) (string, error) {
	proxy = strings.TrimSpace(proxy)
	if proxy == "" {
		return "", errors.New("proxy is empty")
	}

	sessionID, err := randomSessionID(12)
	if err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	return strings.ReplaceAll(proxy, "%s", sessionID), nil
}

func randomSessionID(length int) (string, error) {
	if length <= 0 {
		return "", errors.New("session id length must be positive")
	}
	return randomString(length, "abcdefghijklmnopqrstuvwxyz0123456789")
}

func pickRandomProxy(pool []string) string {
	cleaned := make([]string, 0, len(pool))
	for _, item := range pool {
		item = strings.TrimSpace(item)
		if item != "" {
			cleaned = append(cleaned, item)
		}
	}
	if len(cleaned) == 0 {
		return ""
	}
	return cleaned[randv2.N(len(cleaned))]
}

func newRegistrationFlow(cfg registrationConfig) (*registrationFlow, error) {
	client, err := newClient(cfg)
	if err != nil {
		return nil, err
	}

	return &registrationFlow{
		cfg:    cfg,
		client: client,
	}, nil
}

func (r *registrationFlow) resetAuthSession() error {
	client, err := r.client.Refresh()
	if err != nil {
		return fmt.Errorf("build fresh http client: %w", err)
	}
	r.client = client
	r.oaiDID = ""
	return nil
}
