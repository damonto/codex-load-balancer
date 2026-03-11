package main

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

func authHeaderValue(token string) string {
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		return token
	}
	return "Bearer " + token
}

func proxyAuthorized(r *http.Request, apiKey string) bool {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return false
	}

	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if value == "" {
		return false
	}
	scheme, token, ok := strings.Cut(value, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(apiKey)) == 1
}
