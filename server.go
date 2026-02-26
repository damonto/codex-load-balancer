package main

import (
	"net/http"
	"net/url"
)

type Server struct {
	store       *TokenStore
	client      *http.Client
	upstreamURL *url.URL
	apiKey      string
	usageDB     *UsageDB
	usageSink   *UsageSink
}
