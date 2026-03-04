package main

import (
	"strings"
	"time"
)

const (
	defaultPort        = 8080
	backendAPIURL      = "https://chatgpt.com/backend-api"
	refreshTokenURL    = "https://auth.openai.com/oauth/token"
	refreshClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	refreshScope       = "openid profile email"
	refreshInterval    = 8 * 24 * time.Hour
	refreshDebounce    = 30 * time.Second // prevent thundering herd on concurrent 401s
	cooldownDuration   = time.Minute
	watchInterval      = 10 * time.Second
	syncInterval       = 5 * time.Minute
	syncConcurrency    = 8
	defaultLimitPoints = 100.0
	maxRequestBodySize = 10 * 1024 * 1024 // 10 MB
)

func backendEndpoint(path string) string {
	base := strings.TrimRight(backendAPIURL, "/")
	if strings.HasPrefix(path, "/") {
		return base + path
	}
	return base + "/" + path
}
