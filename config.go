package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/damonto/codex-load-balancer/plus"
	toml "github.com/pelletier/go-toml/v2"
)

const defaultConfigPath = "config.toml"
const defaultPort = 8080

type appConfig struct {
	apiKey             string
	dataDir            string
	port               int
	minTrackedAccounts int
	registerWorkers    int
	registerTimeout    time.Duration
	proxyPool          plus.RegistrationProxyPool
	syncInterval       time.Duration
	syncConcurrency    int
}

type fileConfig struct {
	APIKey  string            `toml:"api_key"`
	DataDir string            `toml:"data_dir"`
	Server  fileServerConfig  `toml:"server"`
	TopUp   fileTopUpConfig   `toml:"top_up"`
	Sync    fileSyncConfig    `toml:"sync"`
	Account fileAccountConfig `toml:"account"`
}

type fileServerConfig struct {
	Port int `toml:"port"`
}

type fileTopUpConfig struct {
	MinTrackedAccounts     int `toml:"min_tracked_accounts"`
	RegisterWorkers        int `toml:"register_workers"`
	RegisterTimeoutSeconds int `toml:"register_timeout_seconds"`
}

type fileSyncConfig struct {
	UsageSyncIntervalSeconds int `toml:"usage_sync_interval_seconds"`
	UsageSyncConcurrency     int `toml:"usage_sync_concurrency"`
}

type fileAccountConfig struct {
	RegistrationProxyPool []string `toml:"registration_proxy_pool"`
}

func loadAppConfigFile(path string) (appConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return appConfig{}, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	var fc fileConfig
	if err := toml.NewDecoder(f).DisallowUnknownFields().Decode(&fc); err != nil {
		return appConfig{}, fmt.Errorf("decode config file %q: %w", path, err)
	}
	cfg := appConfig{
		apiKey:             fc.APIKey,
		dataDir:            fc.DataDir,
		port:               fc.Server.Port,
		minTrackedAccounts: fc.TopUp.MinTrackedAccounts,
		registerWorkers:    fc.TopUp.RegisterWorkers,
		registerTimeout:    secondsOrDefault(fc.TopUp.RegisterTimeoutSeconds, defaultRegisterTimeout),
		syncInterval:       secondsOrDefault(fc.Sync.UsageSyncIntervalSeconds, defaultUsageSyncInterval),
		syncConcurrency:    fc.Sync.UsageSyncConcurrency,
	}
	if cfg.port <= 0 {
		cfg.port = defaultPort
	}
	if cfg.registerWorkers <= 0 {
		cfg.registerWorkers = defaultRegisterWorkers
	}
	if cfg.minTrackedAccounts < 0 {
		cfg.minTrackedAccounts = 0
	}
	if cfg.syncConcurrency <= 0 {
		cfg.syncConcurrency = defaultUsageSyncConcurrency
	}
	cfg.proxyPool, err = normalizeProxyPool(fc.Account.RegistrationProxyPool)
	if err != nil {
		return appConfig{}, err
	}
	return cfg, nil
}

func secondsOrDefault(seconds int, fallback time.Duration) time.Duration {
	if seconds <= 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}

func normalizeProxyPool(pool []string) (plus.RegistrationProxyPool, error) {
	if len(pool) == 0 {
		return nil, errors.New("account.registration_proxy_pool is required")
	}
	clean := make([]string, 0, len(pool))
	for _, item := range pool {
		item = strings.TrimSpace(item)
		if item != "" {
			clean = append(clean, item)
		}
	}
	if len(clean) == 0 {
		return nil, errors.New("account.registration_proxy_pool is empty")
	}
	return plus.RegistrationProxyPool(clean), nil
}
