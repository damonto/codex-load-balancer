package main

import (
	"path/filepath"
	"testing"

	"github.com/damonto/codex-load-balancer/plus"
)

func TestBootstrapRuntimeOpensPurchaseStoreWhenEnabled(t *testing.T) {
	cfg := appConfig{
		dataDir: filepath.Clean(t.TempDir()),
		purchaseConfig: plus.PurchaseConfig{
			Enabled:             true,
			RevenueCatBearerKey: "goog_test_key",
		},
	}

	rt, err := bootstrapRuntime(&cfg)
	if err != nil {
		t.Fatalf("bootstrapRuntime() error = %v", err)
	}
	t.Cleanup(func() {
		closeRuntime(rt)
	})

	if rt.purchaseStore == nil {
		t.Fatal("purchaseStore = nil, want initialized store")
	}
	if cfg.purchaseConfig.Store == nil {
		t.Fatal("cfg.purchaseConfig.Store = nil, want initialized store")
	}
}
