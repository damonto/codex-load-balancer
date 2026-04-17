package main

import (
	"context"
	"net/http"
	"net/url"
	"sync"
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

	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
	websocketWG    sync.WaitGroup
}

func (s *Server) beginShutdown() {
	if s == nil || s.shutdownCancel == nil {
		return
	}
	s.shutdownCancel()
}

func (s *Server) waitWebSockets() {
	if s == nil {
		return
	}
	s.websocketWG.Wait()
}

func (s *Server) shutdownContext() context.Context {
	if s == nil || s.shutdownCtx == nil {
		return context.Background()
	}
	return s.shutdownCtx
}
