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

type appConfig struct {
	apiKey             string
	dataDir            string
	port               int
	topUpEnabled       bool
	minTrackedAccounts int
	registerWorkers    int
	registerTimeout    time.Duration
	proxyPool          plus.RegistrationProxyPool
	purchaseConfig     plus.PurchaseConfig
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
	Port *int `toml:"port"`
}

type fileTopUpConfig struct {
	Enabled                *bool `toml:"enabled"`
	MinTrackedAccounts     *int  `toml:"min_tracked_accounts"`
	RegisterWorkers        *int  `toml:"register_workers"`
	RegisterTimeoutSeconds *int  `toml:"register_timeout_seconds"`
}

type fileSyncConfig struct {
	UsageSyncIntervalSeconds *int `toml:"usage_sync_interval_seconds"`
	UsageSyncConcurrency     *int `toml:"usage_sync_concurrency"`
}

type fileAccountConfig struct {
	RegistrationProxyPool []string              `toml:"registration_proxy_pool"`
	PaymentCard           filePaymentCardConfig `toml:"payment_card"`
	Purchase              filePurchaseConfig    `toml:"purchase"`
}

type filePaymentCardConfig struct {
	BINs []string `toml:"bins"`
}

type filePurchaseConfig struct {
	Enabled         *bool               `toml:"enabled"`
	PlanName        string              `toml:"plan_name"`
	Currency        string              `toml:"currency"`
	PromoCampaignID string              `toml:"promo_campaign_id"`
	CheckoutUIMode  string              `toml:"checkout_ui_mode"`
	Billing         filePurchaseBilling `toml:"billing"`
}

type filePurchaseBilling struct {
	Name         string `toml:"name"`
	Country      string `toml:"country"`
	AddressLine1 string `toml:"address_line1"`
	AddressCity  string `toml:"address_city"`
	AddressState string `toml:"address_state"`
	PostalCode   string `toml:"postal_code"`
}

func loadAppConfigFile(path string) (appConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return appConfig{}, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	var fc fileConfig
	if err := toml.NewDecoder(f).DisallowUnknownFields().Decode(&fc); err != nil {
		if strings.Contains(err.Error(), "strict mode:") {
			return appConfig{}, fmt.Errorf("decode config file %q: unknown config keys: %w", path, err)
		}
		return appConfig{}, fmt.Errorf("decode config file %q: %w", path, err)
	}
	if fc.Server.Port == nil {
		return appConfig{}, errors.New("server.port is required")
	}
	if fc.TopUp.Enabled == nil {
		return appConfig{}, errors.New("top_up.enabled is required")
	}
	if fc.TopUp.MinTrackedAccounts == nil {
		return appConfig{}, errors.New("top_up.min_tracked_accounts is required")
	}
	if fc.TopUp.RegisterWorkers == nil {
		return appConfig{}, errors.New("top_up.register_workers is required")
	}
	if fc.TopUp.RegisterTimeoutSeconds == nil {
		return appConfig{}, errors.New("top_up.register_timeout_seconds is required")
	}
	if fc.Sync.UsageSyncIntervalSeconds == nil {
		return appConfig{}, errors.New("sync.usage_sync_interval_seconds is required")
	}
	if fc.Sync.UsageSyncConcurrency == nil {
		return appConfig{}, errors.New("sync.usage_sync_concurrency is required")
	}
	if fc.Account.Purchase.Enabled == nil {
		return appConfig{}, errors.New("account.purchase.enabled is required")
	}

	cfg := appConfig{
		apiKey:             fc.APIKey,
		dataDir:            fc.DataDir,
		port:               *fc.Server.Port,
		registerWorkers:    *fc.TopUp.RegisterWorkers,
		topUpEnabled:       *fc.TopUp.Enabled,
		minTrackedAccounts: *fc.TopUp.MinTrackedAccounts,
		registerTimeout:    time.Duration(*fc.TopUp.RegisterTimeoutSeconds) * time.Second,
		purchaseConfig: plus.PurchaseConfig{
			Enabled:         *fc.Account.Purchase.Enabled,
			PlanName:        fc.Account.Purchase.PlanName,
			Currency:        fc.Account.Purchase.Currency,
			PromoCampaignID: fc.Account.Purchase.PromoCampaignID,
			CheckoutUIMode:  fc.Account.Purchase.CheckoutUIMode,
				Billing: plus.PurchaseBillingConfig{
					Name:         fc.Account.Purchase.Billing.Name,
					Country:      fc.Account.Purchase.Billing.Country,
					AddressLine1: fc.Account.Purchase.Billing.AddressLine1,
					AddressCity:  fc.Account.Purchase.Billing.AddressCity,
				AddressState: fc.Account.Purchase.Billing.AddressState,
				PostalCode:   fc.Account.Purchase.Billing.PostalCode,
			},
			PaymentCard: plus.PaymentCardConfig{
				BINs: fc.Account.PaymentCard.BINs,
			},
		},
		syncInterval:    time.Duration(*fc.Sync.UsageSyncIntervalSeconds) * time.Second,
		syncConcurrency: *fc.Sync.UsageSyncConcurrency,
	}
	if cfg.port <= 0 {
		return appConfig{}, errors.New("server.port must be positive")
	}
	if cfg.minTrackedAccounts < 0 {
		return appConfig{}, errors.New("top_up.min_tracked_accounts must be non-negative")
	}
	if cfg.registerWorkers <= 0 {
		return appConfig{}, errors.New("top_up.register_workers must be positive")
	}
	if cfg.registerTimeout <= 0 {
		return appConfig{}, errors.New("top_up.register_timeout_seconds must be positive")
	}
	if cfg.syncInterval <= 0 {
		return appConfig{}, errors.New("sync.usage_sync_interval_seconds must be positive")
	}
	if cfg.syncConcurrency <= 0 {
		return appConfig{}, errors.New("sync.usage_sync_concurrency must be positive")
	}
	cfg.proxyPool, err = normalizeProxyPool(fc.Account.RegistrationProxyPool)
	if err != nil {
		return appConfig{}, err
	}
	return cfg, nil
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
