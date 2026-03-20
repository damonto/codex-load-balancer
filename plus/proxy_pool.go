package plus

import (
	"errors"
	"fmt"
	randv2 "math/rand/v2"
	"net/url"
	"strings"
)

// RegistrationProxyPool keeps proxy templates together so each selection can
// return a fresh proxy URL with a new session identifier.
type RegistrationProxyPool []string

func (p RegistrationProxyPool) cleaned() RegistrationProxyPool {
	cleaned := make(RegistrationProxyPool, 0, len(p))
	for _, item := range p {
		item = strings.TrimSpace(item)
		if item != "" {
			cleaned = append(cleaned, item)
		}
	}
	return cleaned
}

// Random picks one proxy template and returns a ready-to-use proxy URL.
func (p RegistrationProxyPool) Random() (string, error) {
	cleaned := p.cleaned()
	if len(cleaned) == 0 {
		return "", errors.New("proxy pool is empty")
	}
	return buildRegistrationProxyURL(cleaned[randv2.N(len(cleaned))])
}

func buildRegistrationProxyURL(proxy string) (string, error) {
	proxy = strings.TrimSpace(proxy)
	if proxy == "" {
		return "", errors.New("proxy is empty")
	}

	proxy, err := injectProxySessionID(proxy)
	if err != nil {
		return "", fmt.Errorf("inject proxy session id: %w", err)
	}
	if _, err := url.Parse(proxy); err != nil {
		return "", fmt.Errorf("parse proxy: %w", err)
	}
	return proxy, nil
}

func injectProxySessionID(proxy string) (string, error) {
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
