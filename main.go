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
	"path/filepath"
	"syscall"
	"time"
)

var (
	BuildVersion string
	apiKey       string
	tokenDir     string
	port         int
	usageDBPath  string
)

func main() {
	flag.StringVar(&apiKey, "api-key", "", "API key for admin endpoints")
	flag.StringVar(&tokenDir, "token-dir", "", "directory with auth.json files")
	flag.IntVar(&port, "port", defaultPort, "port to listen on")
	flag.StringVar(&usageDBPath, "usage-db", "", "sqlite file path for token usage stats (default: <token-dir>/usage.db)")
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
	if usageDBPath == "" {
		usageDBPath = filepath.Join(tokenDir, "usage.db")
	}

	store := NewTokenStore()
	if err := loadTokensFromDir(store, tokenDir); err != nil {
		slog.Error("initial token load", "err", err)
		os.Exit(1)
	}

	usageDB, err := openUsageDB(usageDBPath)
	if err != nil {
		slog.Error("open usage db", "path", usageDBPath, "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	usageSink := NewUsageSink(usageDB, 2048)
	usageSink.Run(ctx)
	defer func() {
		if err := usageDB.Close(); err != nil {
			slog.Warn("close usage db", "err", err)
		}
	}()
	defer usageSink.Wait()
	defer stop()

	go runTokenWatcher(ctx, store, tokenDir)
	go runUsageSyncer(ctx, store)

	upstreamURL, err := url.Parse(backendEndpoint("/codex"))
	if err != nil {
		slog.Error("parse upstream URL", "err", err)
		os.Exit(1)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	client := &http.Client{Transport: transport}

	server := &Server{
		store:       store,
		client:      client,
		upstreamURL: upstreamURL,
		apiKey:      apiKey,
		usageDB:     usageDB,
		usageSink:   usageSink,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/stats/assets/", handleDashboardAsset)
	mux.HandleFunc("/stats", server.handleDashboard)
	mux.HandleFunc("/stats/overview", server.handleDashboardOverview)
	mux.HandleFunc("/stats/account", server.handleDashboardAccount)
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
