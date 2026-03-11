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
	"strings"
	"syscall"
	"time"
)

const usageSinkBufferSize = 2048

var BuildVersion string

type appRuntime struct {
	store     *TokenStore
	usageDB   *UsageDB
	usageSink *UsageSink
}

func main() {
	slog.Info("starting codex load balancer", "build_version", BuildVersion)

	configPath := parseConfigPath()
	cfg, err := loadAppConfigFile(configPath)
	if err != nil {
		slog.Error("load config", "err", err)
		os.Exit(1)
	}
	if err := run(cfg); err != nil {
		slog.Error("run", "err", err)
		os.Exit(1)
	}
}

func parseConfigPath() string {
	var path string
	flag.StringVar(&path, "config", defaultConfigPath, "path to TOML config file")
	flag.Parse()
	path = strings.TrimSpace(path)
	if path == "" {
		return defaultConfigPath
	}
	return path
}

func run(cfg appConfig) error {
	if err := validateAppConfig(cfg); err != nil {
		return err
	}

	rt, err := bootstrapRuntime(cfg)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	rt.usageSink.Run(ctx)
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
		return errors.New("api_key is required")
	}
	if cfg.dataDir == "" {
		return errors.New("data_dir is required")
	}

	info, err := os.Stat(cfg.dataDir)
	if err != nil {
		return fmt.Errorf("stat data_dir: %w", err)
	}
	if !info.IsDir() {
		return errors.New("data_dir is not a directory")
	}
	return nil
}

func bootstrapRuntime(cfg appConfig) (*appRuntime, error) {
	store := NewTokenStore()
	if err := loadTokensFromDir(store, cfg.dataDir); err != nil {
		return nil, fmt.Errorf("initial token load: %w", err)
	}
	if err := ensurePendingPurchaseDir(cfg.dataDir); err != nil {
		return nil, err
	}

	usageDBPath := filepath.Join(cfg.dataDir, "usage.db")
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
	rt.usageSink.Wait()
	if err := rt.usageDB.Close(); err != nil {
		slog.Warn("close usage db", "err", err)
	}
}

func startBackgroundWorkers(ctx context.Context, cfg appConfig, store *TokenStore) {
	go runTokenWatcher(ctx, store, cfg.dataDir)
	topUpOpts := topUpOptions{
		TargetCount:      cfg.minTrackedAccounts,
		RegisterWorkers:  cfg.registerWorkers,
		RegisterTimeout:  cfg.registerTimeout,
		ProxyPool:        cfg.proxyPool,
		TelegramBotToken: cfg.telegramBotToken,
		TelegramChatID:   cfg.telegramChatID,
	}
	usageURL := backendEndpoint(defaultBackendAPIURL, "/wham/usage")
	go runUsageSyncer(ctx, store, cfg.dataDir, usageURL, usageSyncOptions{
		Interval:    cfg.syncInterval,
		Concurrency: cfg.syncConcurrency,
	}, topUpOpts)
	go runPendingPurchaseSyncer(ctx, store, cfg.dataDir, usageURL, usageSyncOptions{
		Interval:    cfg.syncInterval,
		Concurrency: cfg.syncConcurrency,
	})
	if cfg.minTrackedAccounts == 0 {
		return
	}

	go func() {
		if err := topUpAccounts(ctx, store, cfg.dataDir, topUpOpts); err != nil && ctx.Err() == nil {
			slog.Warn("startup account top-up", "err", err)
		}
	}()
}

func newHTTPServer(cfg appConfig, rt *appRuntime) (*http.Server, error) {
	upstreamURL, err := url.Parse(backendEndpoint(defaultBackendAPIURL, "/codex"))
	if err != nil {
		return nil, fmt.Errorf("parse upstream URL: %w", err)
	}

	server := &Server{
		store:       rt.store,
		client:      newUpstreamClient(),
		upstreamURL: upstreamURL,
		apiKey:      cfg.apiKey,
		usageDB:     rt.usageDB,
		usageSink:   rt.usageSink,
	}

	addr := fmt.Sprintf(":%d", cfg.port)
	return &http.Server{
		Addr:              addr,
		Handler:           newMux(server),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       90 * time.Second,
	}, nil
}

func newUpstreamClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{Transport: transport}
}

func newMux(server *Server) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/stats/assets/", handleDashboardAsset)
	mux.HandleFunc("/stats", server.handleDashboard)
	mux.HandleFunc("/stats/overview", server.handleDashboardOverview)
	mux.HandleFunc("/stats/account", server.handleDashboardAccount)
	mux.HandleFunc("/", server.handleProxy)
	return mux
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
