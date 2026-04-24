package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultPort                 = 8080
	defaultUsageSyncInterval    = 5 * time.Minute
	defaultUsageSyncConcurrency = 8
)

var errHelpRequested = errors.New("help requested")

type appConfig struct {
	apiKey          string
	dataDir         string
	port            int
	syncInterval    time.Duration
	syncConcurrency int
}

func parseAppConfig(args []string, output io.Writer) (appConfig, error) {
	cfg := appConfig{
		port:            defaultPort,
		syncInterval:    defaultUsageSyncInterval,
		syncConcurrency: defaultUsageSyncConcurrency,
	}

	fs := flag.NewFlagSet("codex-load-balancer", flag.ContinueOnError)
	fs.SetOutput(output)
	fs.Usage = func() {
		fmt.Fprintf(output, "Usage: %s [flags]\n", fs.Name())
		fs.PrintDefaults()
	}
	fs.StringVar(&cfg.apiKey, "api-key", "", "API key for protected proxy endpoints")
	fs.StringVar(&cfg.dataDir, "data-dir", "", "directory containing active *.json auth files")
	fs.IntVar(&cfg.port, "port", defaultPort, "listen port")
	fs.DurationVar(&cfg.syncInterval, "sync-interval", defaultUsageSyncInterval, "usage sync interval")
	fs.IntVar(&cfg.syncConcurrency, "sync-concurrency", defaultUsageSyncConcurrency, "usage sync worker count")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return appConfig{}, errHelpRequested
		}
		return appConfig{}, err
	}
	if fs.NArg() != 0 {
		return appConfig{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}

	cfg.apiKey = strings.TrimSpace(cfg.apiKey)
	cfg.dataDir = strings.TrimSpace(cfg.dataDir)
	if cfg.dataDir != "" {
		cfg.dataDir = filepath.Clean(cfg.dataDir)
	}
	return cfg, nil
}
