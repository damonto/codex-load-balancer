package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/damonto/codex-load-balancer/plus"
)

func TestImportPurchaseTokensFromFile(t *testing.T) {
	dataDir := t.TempDir()
	store, err := plus.OpenPurchaseTokenStore(filepath.Join(dataDir, "clb.db"))
	if err != nil {
		t.Fatalf("OpenPurchaseTokenStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	tokensPath := filepath.Join(dataDir, purchaseTokenFileName)
	if err := os.WriteFile(tokensPath, []byte("# comment\nfetch-token-1\n\nfetch-token-2\nfetch-token-1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := importPurchaseTokensFromFile(context.Background(), dataDir, store); err != nil {
		t.Fatalf("importPurchaseTokensFromFile() error = %v", err)
	}

	if _, err := os.Stat(tokensPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tokens.txt stat error = %v, want not exist", err)
	}

	for _, want := range []string{"fetch-token-1", "fetch-token-2"} {
		lease, err := store.LeaseToken(context.Background())
		if err != nil {
			t.Fatalf("LeaseToken() error = %v", err)
		}
		if lease.FetchToken() != want {
			t.Fatalf("FetchToken() = %q, want %q", lease.FetchToken(), want)
		}
	}
}

func TestImportPurchaseTokensFromFileRestoresSnapshotOnFailure(t *testing.T) {
	dataDir := t.TempDir()
	store, err := plus.OpenPurchaseTokenStore(filepath.Join(dataDir, "clb.db"))
	if err != nil {
		t.Fatalf("OpenPurchaseTokenStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	tokensPath := filepath.Join(dataDir, purchaseTokenFileName)
	original := "fetch-token-restore\n"
	if err := os.WriteFile(tokensPath, []byte(original), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = importPurchaseTokensFromFile(ctx, dataDir, store)
	if err == nil {
		t.Fatal("importPurchaseTokensFromFile() error = nil, want context cancellation")
	}

	data, readErr := os.ReadFile(tokensPath)
	if readErr != nil {
		t.Fatalf("ReadFile() error = %v", readErr)
	}
	if string(data) != original {
		t.Fatalf("restored file = %q, want %q", string(data), original)
	}

	if _, leaseErr := store.LeaseToken(context.Background()); !errors.Is(leaseErr, plus.ErrPurchaseTokenQueueEmpty) {
		t.Fatalf("LeaseToken() error = %v, want ErrPurchaseTokenQueueEmpty", leaseErr)
	}
}
