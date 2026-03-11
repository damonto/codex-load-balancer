package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
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
	proxyPool          []string
	telegramBotToken   string
	telegramChatID     string
	syncInterval       time.Duration
	syncConcurrency    int
}

type fileConfig struct {
	APIKey   string             `toml:"api_key"`
	DataDir  string             `toml:"data_dir"`
	Server   fileServerConfig   `toml:"server"`
	TopUp    fileTopUpConfig    `toml:"top_up"`
	Sync     fileSyncConfig     `toml:"sync"`
	Account  fileAccountConfig  `toml:"account"`
	Telegram fileTelegramConfig `toml:"telegram"`
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

type fileTelegramConfig struct {
	BotToken string `toml:"bot_token"`
	ChatID   string `toml:"chat_id"`
}

func loadAppConfigFile(path string) (appConfig, error) {
	var fc fileConfig
	meta, err := toml.DecodeFile(path, &fc)
	if err != nil {
		return appConfig{}, fmt.Errorf("decode config file %q: %w", path, err)
	}

	undecoded := meta.Undecoded()
	if len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}
		slices.Sort(keys)
		return appConfig{}, fmt.Errorf("unknown config keys: %s", strings.Join(keys, ", "))
	}

	cfg := appConfig{
		apiKey:             strings.TrimSpace(fc.APIKey),
		dataDir:            strings.TrimSpace(fc.DataDir),
		port:               fc.Server.Port,
		minTrackedAccounts: fc.TopUp.MinTrackedAccounts,
		registerWorkers:    fc.TopUp.RegisterWorkers,
		registerTimeout:    secondsOrDefault(fc.TopUp.RegisterTimeoutSeconds, defaultRegisterTimeout),
		telegramBotToken:   strings.TrimSpace(fc.Telegram.BotToken),
		telegramChatID:     strings.TrimSpace(fc.Telegram.ChatID),
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
	if (cfg.telegramBotToken == "") != (cfg.telegramChatID == "") {
		return appConfig{}, errors.New("telegram.bot_token and telegram.chat_id must be set together")
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

func normalizeProxyPool(pool []string) ([]string, error) {
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
	return clean, nil
}
