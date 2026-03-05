package main

import (
	"net/http"
	"net/url"
	"time"
)

const (
	defaultCooldownDuration = time.Minute
	defaultMaxRequestBody   = 10 * 1024 * 1024 // 10 MB
)

type Server struct {
	store       *TokenStore
	client      *http.Client
	upstreamURL *url.URL
	apiKey      string
	usageDB     *UsageDB
	usageSink   *UsageSink
}
