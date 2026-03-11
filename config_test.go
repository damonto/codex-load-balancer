package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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
			name: "required fields with defaults",
			body: `
api_key = "k"
data_dir = "/tmp/data"

[account]
registration_proxy_pool = ["http://proxy-default"]
`,
			want: defaultAppConfigForTest("k", "/tmp/data", []string{"http://proxy-default"}),
		},
		{
			name: "custom values",
			body: `
api_key = "admin"
data_dir = "/data"

[server]
port = 9090

[top_up]
min_tracked_accounts = 12
register_workers = 3
register_timeout_seconds = 420

[sync]
usage_sync_interval_seconds = 600
usage_sync_concurrency = 4

[telegram]
bot_token = "bot-token"
chat_id = "123456789"

[account]
registration_proxy_pool = ["http://proxy-a", "http://proxy-b"]
`,
			want: appConfig{
				apiKey:             "admin",
				dataDir:            "/data",
				port:               9090,
				minTrackedAccounts: 12,
				registerWorkers:    3,
				registerTimeout:    420 * time.Second,
				proxyPool:          []string{"http://proxy-a", "http://proxy-b"},
				telegramBotToken:   "bot-token",
				telegramChatID:     "123456789",
				syncInterval:       600 * time.Second,
				syncConcurrency:    4,
			},
		},
		{
			name: "unknown key",
			body: `
api_key = "k"
data_dir = "/tmp/data"
[account]
registration_proxy_pool = ["http://proxy-default"]
oops = 1
`,
			wantErr: "unknown config keys",
		},
		{
			name: "normalize non-positive values",
			body: `
api_key = "k"
data_dir = "/tmp/data"

[server]
port = 0

[top_up]
min_tracked_accounts = -1
register_workers = 0
register_timeout_seconds = 0

[sync]
usage_sync_interval_seconds = 0
usage_sync_concurrency = 0

[account]
registration_proxy_pool = ["http://proxy-default"]
`,
			want: defaultAppConfigForTest("k", "/tmp/data", []string{"http://proxy-default"}),
		},
		{
			name: "proxy pool missing",
			body: `
api_key = "k"
data_dir = "/tmp/data"
`,
			wantErr: "account.registration_proxy_pool is required",
		},
		{
			name: "proxy pool present but empty",
			body: `
api_key = "k"
data_dir = "/tmp/data"

[account]
registration_proxy_pool = ["  ", ""]
			`,
			wantErr: "account.registration_proxy_pool is empty",
		},
		{
			name: "telegram config must be paired",
			body: `
api_key = "k"
data_dir = "/tmp/data"

[telegram]
bot_token = "bot-token"

[account]
registration_proxy_pool = ["http://proxy-default"]
			`,
			wantErr: "telegram.bot_token and telegram.chat_id must be set together",
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

func defaultAppConfigForTest(apiKey string, dataDir string, proxyPool []string) appConfig {
	return appConfig{
		apiKey:             apiKey,
		dataDir:            dataDir,
		port:               defaultPort,
		minTrackedAccounts: 0,
		registerWorkers:    defaultRegisterWorkers,
		registerTimeout:    defaultRegisterTimeout,
		proxyPool:          proxyPool,
		syncInterval:       defaultUsageSyncInterval,
		syncConcurrency:    defaultUsageSyncConcurrency,
	}
}

func writeTestFile(path string, body string) error {
	return os.WriteFile(path, []byte(body+"\n"), 0o644)
}
