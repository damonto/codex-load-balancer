package main

// These product endpoints are stable integration points, so keeping them
// internal avoids a wider config surface that the service does not need.
const (
	backendAPIBaseURL = "https://chatgpt.com/backend-api"
	usageEndpointURL  = backendAPIBaseURL + "/wham/usage"
	codexEndpointURL  = backendAPIBaseURL + "/codex"
	authBaseURL       = "https://auth.openai.com"
	refreshTokenURL   = authBaseURL + "/oauth/token"
)
