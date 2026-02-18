package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var (
	BuildVersion string
	apiKey       string
	tokenDir     string
	port         int
)

func main() {
	flag.StringVar(&apiKey, "api-key", "", "API key for admin endpoints")
	flag.StringVar(&tokenDir, "token-dir", "", "directory with auth.json files")
	flag.IntVar(&port, "port", defaultPort, "port to listen on")
	flag.Parse()

	if apiKey == "" {
		slog.Error("missing --api-key")
		os.Exit(1)
	}
	if tokenDir == "" {
		slog.Error("missing --token-dir")
		os.Exit(1)
	}
	info, err := os.Stat(tokenDir)
	if err != nil {
		slog.Error("stat --token-dir", "err", err)
		os.Exit(1)
	}
	if !info.IsDir() {
		slog.Error("--token-dir is not a directory")
		os.Exit(1)
	}

	store := NewTokenStore()
	if err := loadTokensFromDir(store, tokenDir); err != nil {
		slog.Error("initial token load", "err", err)
		os.Exit(1)
	}

	fallback := NewFallbackManager(tokenDir)
	if changed, err := fallback.Reload(); err != nil {
		slog.Warn("fallback load", "err", err)
	} else if changed {
		slog.Info("fallback loaded", "accounts", fallback.AccountCount())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go runTokenWatcher(ctx, store, tokenDir)
	go runUsageSyncer(ctx, store)
	go runFallbackWatcher(ctx, fallback)
	go runFallbackCheckinScheduler(ctx, fallback)

	upstreamURL, _ := url.Parse(backendEndpoint("/codex"))
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	client := &http.Client{Transport: transport}

	server := &Server{
		store:        store,
		client:       client,
		upstreamBase: upstreamURL,
		apiKey:       apiKey,
		fallback:     fallback,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/admin/stats", server.handleStats)
	mux.HandleFunc("/", server.handleProxy)

	addr := fmt.Sprintf(":%d", port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("codex load balancer listening", "addr", addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server stopped", "err", err)
			os.Exit(1)
		}
		return
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("server shutdown", "err", err)
	}

	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Warn("server stopped", "err", err)
	}
}
