package main

import "time"

const (
	defaultPort        = 8080
	usageBaseURL       = "https://chatgpt.com/backend-api"
	upstreamBaseURL    = "https://chatgpt.com/backend-api/codex"
	refreshTokenURL    = "https://auth.openai.com/oauth/token"
	refreshClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	refreshScope       = "openid profile email"
	refreshInterval    = 8 * 24 * time.Hour
	cooldownDuration   = time.Minute
	watchInterval      = 10 * time.Second
	syncInterval       = time.Minute
	defaultLimitPoints = 100.0
	fallbackFileName   = "fallback.json"
)

var sessionHeaders = []string{
	"session_id",
}
