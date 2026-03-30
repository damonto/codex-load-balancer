package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/damonto/codex-load-balancer/plus"
)

func TestLoadAppConfigFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		want    appConfig
		wantErr string
	}{
		{
			name: "explicit minimal config with top up disabled",
			body: `
api_key = "k"
data_dir = "/tmp/data"

[server]
port = 8080

[top_up]
enabled = false
min_tracked_accounts = 0
register_workers = 1
register_timeout_seconds = 60

[sync]
usage_sync_interval_seconds = 300
usage_sync_concurrency = 8

[account]
registration_proxy_pool = ["http://proxy-default"]

[account.purchase]
enabled = false
`,
			want: appConfig{
				apiKey:             "k",
				dataDir:            "/tmp/data",
				port:               8080,
				topUpEnabled:       false,
				minTrackedAccounts: 0,
				registerWorkers:    1,
				registerTimeout:    60 * time.Second,
				proxyPool:          plus.RegistrationProxyPool{"http://proxy-default"},
				purchaseConfig: plus.PurchaseConfig{
					Enabled: false,
				},
				syncInterval:    300 * time.Second,
				syncConcurrency: 8,
			},
		},
		{
			name: "custom values",
			body: `
api_key = "admin"
data_dir = "/data"

[server]
port = 9090

[top_up]
enabled = false
min_tracked_accounts = 12
register_workers = 3
register_timeout_seconds = 420

[sync]
usage_sync_interval_seconds = 600
usage_sync_concurrency = 4

[account]
registration_proxy_pool = ["http://proxy-a", "http://proxy-b"]

[account.payment_card]
bins = ["625817", "624441"]
topup_enabled = true

[account.purchase]
enabled = false
plan_name = "chatgptplusplan"
currency = "KRW"
promo_campaign_id = "plus-1-month-free"
checkout_ui_mode = "custom"

[account.purchase.billing]
name = "Minjun Kim"
country = "KR"
address_line1 = "1 Teheran-ro, Gangnam-gu"
address_state = "Seoul"
postal_code = "06141"
`,
			want: appConfig{
				apiKey:             "admin",
				dataDir:            "/data",
				port:               9090,
				topUpEnabled:       false,
				minTrackedAccounts: 12,
				registerWorkers:    3,
				registerTimeout:    420 * time.Second,
				proxyPool:          plus.RegistrationProxyPool{"http://proxy-a", "http://proxy-b"},
				purchaseConfig: plus.PurchaseConfig{
					Enabled:         false,
					PlanName:        "chatgptplusplan",
					Currency:        "KRW",
					PromoCampaignID: "plus-1-month-free",
					CheckoutUIMode:  "custom",
					Billing: plus.PurchaseBillingConfig{
						Name:         "Minjun Kim",
						Country:      "KR",
						AddressLine1: "1 Teheran-ro, Gangnam-gu",
						AddressState: "Seoul",
						PostalCode:   "06141",
					},
					PaymentCard: plus.PaymentCardConfig{
						BINs:         []string{"625817", "624441"},
						TopUpEnabled: true,
					},
				},
				syncInterval:    600 * time.Second,
				syncConcurrency: 4,
			},
		},
		{
			name: "unknown key",
			body: `
api_key = "k"
data_dir = "/tmp/data"

[server]
port = 8080

[top_up]
enabled = false
min_tracked_accounts = 0
register_workers = 1
register_timeout_seconds = 60

[sync]
usage_sync_interval_seconds = 300
usage_sync_concurrency = 8

[account]
registration_proxy_pool = ["http://proxy-default"]

[account.purchase]
enabled = false
oops = 1
`,
			wantErr: "unknown config keys",
		},
		{
			name: "reject non-positive values",
			body: `
api_key = "k"
data_dir = "/tmp/data"

[server]
port = 0

[top_up]
enabled = false
min_tracked_accounts = -1
register_workers = 0
register_timeout_seconds = 0

[sync]
usage_sync_interval_seconds = 0
usage_sync_concurrency = 0

[account]
registration_proxy_pool = ["http://proxy-default"]

[account.purchase]
enabled = false
`,
			wantErr: "server.port must be positive",
		},
		{
			name: "require top up enabled field",
			body: `
api_key = "k"
data_dir = "/tmp/data"

[server]
port = 8080

[sync]
usage_sync_interval_seconds = 300
usage_sync_concurrency = 8

[account]
registration_proxy_pool = ["http://proxy-default"]
`,
			wantErr: "top_up.enabled is required",
		},
		{
			name: "proxy pool missing",
			body: `
api_key = "k"
data_dir = "/tmp/data"

[server]
port = 8080

[top_up]
enabled = false
min_tracked_accounts = 0
register_workers = 1
register_timeout_seconds = 60

[sync]
usage_sync_interval_seconds = 300
usage_sync_concurrency = 8

[account.purchase]
enabled = false
`,
			wantErr: "account.registration_proxy_pool is required",
		},
		{
			name: "require purchase enabled field",
			body: `
api_key = "k"
data_dir = "/tmp/data"

[server]
port = 8080

[top_up]
enabled = false
min_tracked_accounts = 0
register_workers = 1
register_timeout_seconds = 60

[sync]
usage_sync_interval_seconds = 300
usage_sync_concurrency = 8

[account]
registration_proxy_pool = ["http://proxy-default"]
`,
			wantErr: "account.purchase.enabled is required",
		},
		{
			name: "require payment card topup enabled field when purchase is enabled",
			body: `
api_key = "k"
data_dir = "/tmp/data"

[server]
port = 8080

[top_up]
enabled = true
min_tracked_accounts = 0
register_workers = 1
register_timeout_seconds = 60

[sync]
usage_sync_interval_seconds = 300
usage_sync_concurrency = 8

[account]
registration_proxy_pool = ["http://proxy-default"]

[account.payment_card]
bins = ["625817", "624441"]

[account.purchase]
enabled = true
`,
			wantErr: "account.payment_card.topup_enabled is required",
		},
		{
			name: "top up enabled requires purchase config",
			body: `
api_key = "k"
data_dir = "/tmp/data"

[server]
port = 8080

[top_up]
enabled = true
min_tracked_accounts = 0
register_workers = 1
register_timeout_seconds = 60

[sync]
usage_sync_interval_seconds = 300
usage_sync_concurrency = 8

[account]
registration_proxy_pool = ["http://proxy-default"]

[account.payment_card]
bins = ["625817", "624441"]
topup_enabled = true

[account.purchase]
enabled = true
`,
			wantErr: "validate account purchase config",
		},
		{
			name: "top up enabled allows purchase disabled",
			body: `
api_key = "k"
data_dir = "/tmp/data"

[server]
port = 8080

[top_up]
enabled = true
min_tracked_accounts = 0
register_workers = 1
register_timeout_seconds = 60

[sync]
usage_sync_interval_seconds = 300
usage_sync_concurrency = 8

[account]
registration_proxy_pool = ["http://proxy-default"]

[account.purchase]
enabled = false
`,
			want: appConfig{
				apiKey:             "k",
				dataDir:            "/tmp/data",
				port:               8080,
				topUpEnabled:       true,
				minTrackedAccounts: 0,
				registerWorkers:    1,
				registerTimeout:    60 * time.Second,
				proxyPool:          plus.RegistrationProxyPool{"http://proxy-default"},
				purchaseConfig: plus.PurchaseConfig{
					Enabled: false,
				},
				syncInterval:    300 * time.Second,
				syncConcurrency: 8,
			},
		},
		{
			name: "proxy pool present but empty",
			body: `
api_key = "k"
data_dir = "/tmp/data"

[server]
port = 8080

[top_up]
enabled = false
min_tracked_accounts = 0
register_workers = 1
register_timeout_seconds = 60

[sync]
usage_sync_interval_seconds = 300
usage_sync_concurrency = 8

[account]
registration_proxy_pool = ["  ", ""]

[account.purchase]
enabled = false
			`,
			wantErr: "account.registration_proxy_pool is empty",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			configPath := filepath.Join(t.TempDir(), "config.toml")
			if err := writeTestFile(configPath, strings.TrimSpace(tt.body)); err != nil {
				t.Fatalf("write config: %v", err)
			}

			got, err := loadAppConfigFile(configPath)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("loadAppConfigFile() error = nil, want contains %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("loadAppConfigFile() error = %v, want contains %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("loadAppConfigFile() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("loadAppConfigFile() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func writeTestFile(path string, body string) error {
	return os.WriteFile(path, []byte(body+"\n"), 0o644)
}
