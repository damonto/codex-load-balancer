package main

import (
	"context"
	"errors"
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

const usageSinkBufferSize = 2048

var BuildVersion string

type appRuntime struct {
	store     *TokenStore
	usageDB   *UsageDB
	usageSink *UsageSink
	server    *Server
}

func main() {
	slog.Info("starting codex load balancer", "build_version", BuildVersion)

	cfg, err := parseAppConfig(os.Args[1:], os.Stderr)
	if err != nil {
		if errors.Is(err, errHelpRequested) {
			os.Exit(0)
		}
		slog.Error("parse flags", "err", err)
		os.Exit(1)
	}
	if err := run(cfg); err != nil {
		slog.Error("run", "err", err)
		os.Exit(1)
	}
}

func run(cfg appConfig) error {
	if err := validateAppConfig(cfg); err != nil {
		return err
	}

	rt, err := bootstrapRuntime(&cfg)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	rt.usageSink.Run()
	defer closeRuntime(rt)
	defer stop()

	startBackgroundWorkers(ctx, cfg, rt.store)

	srv, err := newHTTPServer(cfg, rt)
	if err != nil {
		return err
	}

	if err := serve(ctx, srv); err != nil {
		return err
	}
	return nil
}

func validateAppConfig(cfg appConfig) error {
	if cfg.apiKey == "" {
		return errors.New("api-key is required")
	}
	if cfg.dataDir == "" {
		return errors.New("data-dir is required")
	}
	if cfg.port <= 0 {
		return errors.New("port must be positive")
	}
	if cfg.syncInterval <= 0 {
		return errors.New("sync-interval must be positive")
	}
	if cfg.syncConcurrency <= 0 {
		return errors.New("sync-concurrency must be positive")
	}

	info, err := os.Stat(cfg.dataDir)
	if err != nil {
		return fmt.Errorf("stat data-dir: %w", err)
	}
	if !info.IsDir() {
		return errors.New("data-dir is not a directory")
	}
	return nil
}

func bootstrapRuntime(cfg *appConfig) (*appRuntime, error) {
	store := NewTokenStore()
	if err := loadTokensFromDir(store, cfg.dataDir); err != nil {
		return nil, fmt.Errorf("initial token load: %w", err)
	}

	usageDBPath := filepath.Join(cfg.dataDir, "clb.db")
	usageDB, err := openUsageDB(usageDBPath)
	if err != nil {
		return nil, fmt.Errorf("open usage db %q: %w", usageDBPath, err)
	}

	return &appRuntime{
		store:     store,
		usageDB:   usageDB,
		usageSink: NewUsageSink(usageDB, usageSinkBufferSize),
	}, nil
}

func closeRuntime(rt *appRuntime) {
	if rt.server != nil {
		rt.server.beginShutdown()
		rt.server.waitWebSockets()
	}
	rt.usageSink.Stop()
	rt.usageSink.Wait()
	if err := rt.usageDB.Close(); err != nil {
		slog.Warn("close usage db", "err", err)
	}
}

func startBackgroundWorkers(ctx context.Context, cfg appConfig, store *TokenStore) {
	go runTokenWatcher(ctx, store, cfg.dataDir)
	go runUsageSyncer(ctx, store, usageEndpointURL, usageSyncOptions{
		Interval:    cfg.syncInterval,
		Concurrency: cfg.syncConcurrency,
	})
}

func newHTTPServer(cfg appConfig, rt *appRuntime) (*http.Server, error) {
	upstreamURL, err := url.Parse(codexEndpointURL)
	if err != nil {
		return nil, fmt.Errorf("parse upstream URL: %w", err)
	}

	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())

	server := &Server{
		store:          rt.store,
		client:         newUpstreamClient(),
		upstreamURL:    upstreamURL,
		apiKey:         cfg.apiKey,
		usageDB:        rt.usageDB,
		usageSink:      rt.usageSink,
		shutdownCtx:    shutdownCtx,
		shutdownCancel: shutdownCancel,
	}
	rt.server = server

	addr := fmt.Sprintf(":%d", cfg.port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           newMux(server),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	srv.RegisterOnShutdown(server.beginShutdown)
	return srv, nil
}

func newUpstreamClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{Transport: transport}
}

func newMux(server *Server) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/stats/assets/", handleMethodNotAllowed)
	mux.HandleFunc("/stats", handleMethodNotAllowed)
	mux.HandleFunc("/stats/overview", handleMethodNotAllowed)
	mux.HandleFunc("/stats/accounts/details", handleMethodNotAllowed)
	mux.HandleFunc("/stats/account", handleMethodNotAllowed)
	mux.HandleFunc("GET /stats/assets/", handleDashboardAsset)
	mux.HandleFunc("GET /stats", server.handleDashboard)
	mux.HandleFunc("GET /stats/overview", server.handleDashboardOverview)
	mux.HandleFunc("GET /stats/accounts/details", server.handleDashboardAccountsDetails)
	mux.HandleFunc("GET /stats/account", server.handleDashboardAccount)
	mux.HandleFunc("/", server.handleProxy)
	return mux
}

func handleMethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func serve(ctx context.Context, srv *http.Server) error {
	errCh := make(chan error, 1)
	go func() {
		slog.Info("codex load balancer listening", "addr", srv.Addr)
		errCh <- srv.ListenAndServe()
	}()

	// Wait for a stop signal or the server to exit on its own.
	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("server stopped: %w", err)
		}
		return nil // server exited cleanly before signal
	}

	// Signal received — graceful shutdown.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("server shutdown", "err", err)
	}

	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Warn("server stopped", "err", err)
	}
	return nil
}
