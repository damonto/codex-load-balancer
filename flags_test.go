package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseAppConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		want        appConfig
		wantErr     error
		wantErrText string
		wantUsage   string
	}{
		{
			name: "parses explicit flags",
			args: []string{
				"--api-key", "secret",
				"--data-dir", filepath.Join(string(os.PathSeparator), "tmp", "data"),
				"--port", "9090",
				"--sync-interval", "10m",
				"--sync-concurrency", "4",
			},
			want: appConfig{
				apiKey:          "secret",
				dataDir:         filepath.Join(string(os.PathSeparator), "tmp", "data"),
				port:            9090,
				syncInterval:    10 * time.Minute,
				syncConcurrency: 4,
			},
		},
		{
			name: "supports aliases",
			args: []string{
				"--api-key", "secret",
				"--data-key", filepath.Join(string(os.PathSeparator), "tmp", "tokens"),
				"--server-port", "8088",
			},
			want: appConfig{
				apiKey:          "secret",
				dataDir:         filepath.Join(string(os.PathSeparator), "tmp", "tokens"),
				port:            8088,
				syncInterval:    defaultUsageSyncInterval,
				syncConcurrency: defaultUsageSyncConcurrency,
			},
		},
		{
			name:        "rejects unexpected args",
			args:        []string{"--api-key", "secret", "--data-dir", "/tmp/data", "extra"},
			wantErrText: "unexpected arguments: extra",
		},
		{
			name:      "returns help sentinel",
			args:      []string{"--help"},
			wantErr:   errHelpRequested,
			wantUsage: "Usage: codex-load-balancer [flags]",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var output bytes.Buffer
			got, err := parseAppConfig(tt.args, &output)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("parseAppConfig() error = %v, want %v", err, tt.wantErr)
				}
			} else if tt.wantErrText != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("parseAppConfig() error = %v, want contains %q", err, tt.wantErrText)
				}
			} else if err != nil {
				t.Fatalf("parseAppConfig() error = %v", err)
			}

			if tt.wantUsage != "" && !strings.Contains(output.String(), tt.wantUsage) {
				t.Fatalf("usage output = %q, want contains %q", output.String(), tt.wantUsage)
			}
			if tt.wantErr != nil || tt.wantErrText != "" {
				return
			}

			if got != tt.want {
				t.Fatalf("parseAppConfig() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestValidateAppConfig(t *testing.T) {
	t.Parallel()

	validDir := filepath.Clean(t.TempDir())
	filePath := filepath.Join(validDir, "not-a-dir")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	tests := []struct {
		name    string
		cfg     appConfig
		wantErr string
	}{
		{
			name: "requires api key",
			cfg: appConfig{
				dataDir:         validDir,
				port:            defaultPort,
				syncInterval:    defaultUsageSyncInterval,
				syncConcurrency: defaultUsageSyncConcurrency,
			},
			wantErr: "api-key is required",
		},
		{
			name: "requires data dir",
			cfg: appConfig{
				apiKey:          "secret",
				port:            defaultPort,
				syncInterval:    defaultUsageSyncInterval,
				syncConcurrency: defaultUsageSyncConcurrency,
			},
			wantErr: "data-dir is required",
		},
		{
			name: "requires positive port",
			cfg: appConfig{
				apiKey:          "secret",
				dataDir:         validDir,
				syncInterval:    defaultUsageSyncInterval,
				syncConcurrency: defaultUsageSyncConcurrency,
			},
			wantErr: "port must be positive",
		},
		{
			name: "requires positive sync interval",
			cfg: appConfig{
				apiKey:          "secret",
				dataDir:         validDir,
				port:            defaultPort,
				syncConcurrency: defaultUsageSyncConcurrency,
			},
			wantErr: "sync-interval must be positive",
		},
		{
			name: "requires positive sync concurrency",
			cfg: appConfig{
				apiKey:       "secret",
				dataDir:      validDir,
				port:         defaultPort,
				syncInterval: defaultUsageSyncInterval,
			},
			wantErr: "sync-concurrency must be positive",
		},
		{
			name: "rejects missing directory",
			cfg: appConfig{
				apiKey:          "secret",
				dataDir:         filepath.Join(validDir, "missing"),
				port:            defaultPort,
				syncInterval:    defaultUsageSyncInterval,
				syncConcurrency: defaultUsageSyncConcurrency,
			},
			wantErr: "stat data-dir:",
		},
		{
			name: "rejects non directory path",
			cfg: appConfig{
				apiKey:          "secret",
				dataDir:         filePath,
				port:            defaultPort,
				syncInterval:    defaultUsageSyncInterval,
				syncConcurrency: defaultUsageSyncConcurrency,
			},
			wantErr: "data-dir is not a directory",
		},
		{
			name: "accepts valid config",
			cfg: appConfig{
				apiKey:          "secret",
				dataDir:         validDir,
				port:            defaultPort,
				syncInterval:    defaultUsageSyncInterval,
				syncConcurrency: defaultUsageSyncConcurrency,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateAppConfig(tt.cfg)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateAppConfig() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateAppConfig() error = %v, want contains %q", err, tt.wantErr)
			}
		})
	}
}
