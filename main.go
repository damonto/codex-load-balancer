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

const usageSinkBufferSize = 2048

var BuildVersion string

type appConfig struct {
	apiKey           string
	dataDir          string
	port             int
	minValidAccounts int
	registerWorkers  int
}

type appRuntime struct {
	store     *TokenStore
	usageDB   *UsageDB
	usageSink *UsageSink
}

func main() {
	cfg := parseAppConfig()
	if err := run(cfg); err != nil {
		slog.Error("run", "err", err)
		os.Exit(1)
	}
}

func parseAppConfig() appConfig {
	cfg := appConfig{}
	flag.StringVar(&cfg.apiKey, "api-key", "", "API key for admin endpoints")
	flag.StringVar(&cfg.dataDir, "data-dir", "", "directory with auth.json files")
	flag.IntVar(&cfg.port, "port", defaultPort, "port to listen on")
	flag.IntVar(&cfg.minValidAccounts, "min-valid-accounts", 0, "minimum valid accounts required at startup (0 disables auto top-up)")
	flag.IntVar(&cfg.registerWorkers, "register-workers", defaultRegisterWorkers, "concurrent registration workers for startup/runtime top-up")
	flag.Parse()
	return cfg
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
		return errors.New("missing --api-key")
	}
	if cfg.dataDir == "" {
		return errors.New("missing --data-dir")
	}

	info, err := os.Stat(cfg.dataDir)
	if err != nil {
		return fmt.Errorf("stat --data-dir: %w", err)
	}
	if !info.IsDir() {
		return errors.New("--data-dir is not a directory")
	}
	return nil
}

func bootstrapRuntime(cfg appConfig) (*appRuntime, error) {
	store := NewTokenStore()
	if err := loadTokensFromDir(store, cfg.dataDir); err != nil {
		return nil, fmt.Errorf("initial token load: %w", err)
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
	go runUsageSyncer(ctx, store, cfg.dataDir, topUpOptions{
		RegisterWorkers: cfg.registerWorkers,
	})
	if cfg.minValidAccounts == 0 {
		return
	}

	go func() {
		err := topUpAccounts(ctx, store, cfg.dataDir, topUpOptions{
			TargetCount:     cfg.minValidAccounts,
			RegisterWorkers: cfg.registerWorkers,
		})
		if err != nil && ctx.Err() == nil {
			slog.Warn("startup account top-up", "err", err)
		}
	}()
}

func newHTTPServer(cfg appConfig, rt *appRuntime) (*http.Server, error) {
	upstreamURL, err := url.Parse(backendEndpoint("/codex"))
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

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("server stopped: %w", err)
		}
		return nil
	}

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
