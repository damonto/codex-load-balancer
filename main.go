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
	BuildVersion     string
	apiKey           string
	dataDir          string
	port             int
	minValidAccounts int
	registerWorkers  int
)

func main() {
	flag.StringVar(&apiKey, "api-key", "", "API key for admin endpoints")
	flag.StringVar(&dataDir, "data-dir", "", "directory with auth.json files")
	flag.IntVar(&port, "port", defaultPort, "port to listen on")
	flag.IntVar(&minValidAccounts, "min-valid-accounts", 0, "minimum valid accounts required at startup (0 disables auto top-up)")
	flag.IntVar(&registerWorkers, "register-workers", defaultRegisterWorkers, "concurrent registration workers for startup/runtime top-up")
	flag.Parse()

	if apiKey == "" {
		slog.Error("missing --api-key")
		os.Exit(1)
	}
	if dataDir == "" {
		slog.Error("missing --data-dir")
		os.Exit(1)
	}
	info, err := os.Stat(dataDir)
	if err != nil {
		slog.Error("stat --data-dir", "err", err)
		os.Exit(1)
	}
	if !info.IsDir() {
		slog.Error("--data-dir is not a directory")
		os.Exit(1)
	}
	usageDBPath := filepath.Join(dataDir, "usage.db")

	store := NewTokenStore()
	if err := loadTokensFromDir(store, dataDir); err != nil {
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

	go runTokenWatcher(ctx, store, dataDir)
	go runUsageSyncer(ctx, store, dataDir, topUpOptions{
		RegisterWorkers: registerWorkers,
	})
	if minValidAccounts > 0 {
		go func() {
			err := topUpAccounts(ctx, store, dataDir, topUpOptions{
				TargetCount:     minValidAccounts,
				RegisterWorkers: registerWorkers,
			})
			if err != nil && ctx.Err() == nil {
				slog.Warn("startup account top-up", "err", err)
			}
		}()
	}

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
