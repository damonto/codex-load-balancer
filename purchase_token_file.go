package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/damonto/codex-load-balancer/plus"
)

const (
	defaultPurchaseTokenImportInterval = 10 * time.Second
	purchaseTokenFileName              = "tokens.txt"
	purchaseTokenImportSuffix          = ".importing"
)

func runPurchaseTokenFileWatcher(ctx context.Context, dataDir string, store *plus.PurchaseTokenStore) {
	ticker := time.NewTicker(defaultPurchaseTokenImportInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := importPurchaseTokensFromFile(ctx, dataDir, store); err != nil && ctx.Err() == nil {
				slog.Warn("purchase token import", "err", err)
			}
		}
	}
}

func importPurchaseTokensFromFile(ctx context.Context, dataDir string, store *plus.PurchaseTokenStore) error {
	if store == nil {
		return nil
	}

	path := filepath.Join(dataDir, purchaseTokenFileName)
	workingPath := path + purchaseTokenImportSuffix
	if err := os.Rename(path, workingPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("rename purchase token file: %w", err)
	}

	data, err := os.ReadFile(workingPath)
	if err != nil {
		if restoreErr := os.Rename(workingPath, path); restoreErr != nil {
			return errors.Join(fmt.Errorf("read purchase token file: %w", err), fmt.Errorf("restore purchase token file: %w", restoreErr))
		}
		return fmt.Errorf("read purchase token file: %w", err)
	}

	fetchTokens := parsePurchaseTokenLines(string(data))
	if len(fetchTokens) == 0 {
		if err := os.Remove(workingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove empty purchase token import file: %w", err)
		}
		return nil
	}

	result, err := store.ImportFetchTokens(ctx, fetchTokens)
	if err != nil {
		if restoreErr := appendPurchaseTokenSnapshot(path, data); restoreErr != nil {
			return errors.Join(fmt.Errorf("import purchase tokens: %w", err), fmt.Errorf("restore purchase token snapshot: %w", restoreErr))
		}
		_ = os.Remove(workingPath)
		return fmt.Errorf("import purchase tokens: %w", err)
	}

	if err := os.Remove(workingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove purchase token import file: %w", err)
	}

	if result.Inserted > 0 || result.Duplicates > 0 {
		slog.Info(
			"purchase tokens imported",
			"inserted", result.Inserted,
			"duplicates", result.Duplicates,
			"path", path,
		)
	}
	return nil
}

func parsePurchaseTokenLines(body string) []string {
	lines := strings.Split(body, "\n")
	fetchTokens := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fetchTokens = append(fetchTokens, line)
	}
	return fetchTokens
}

func appendPurchaseTokenSnapshot(path string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open purchase token file: %w", err)
	}
	defer file.Close()

	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("append purchase token file: %w", err)
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		if _, err := file.WriteString("\n"); err != nil {
			return fmt.Errorf("append purchase token newline: %w", err)
		}
	}
	return nil
}
