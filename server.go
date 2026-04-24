package main

import (
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

	shutdownOnce sync.Once
	shutdownDone chan struct{}
	websocketWG  sync.WaitGroup
}

func (s *Server) beginShutdown() {
	if s == nil || s.shutdownDone == nil {
		return
	}
	s.shutdownOnce.Do(func() {
		close(s.shutdownDone)
	})
}

func (s *Server) waitWebSockets() {
	if s == nil {
		return
	}
	s.websocketWG.Wait()
}

func (s *Server) shutdownSignal() <-chan struct{} {
	if s == nil || s.shutdownDone == nil {
		return nil
	}
	return s.shutdownDone
}
