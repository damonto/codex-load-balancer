package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/damonto/codex-load-balancer/plus"
)

const (
	defaultRegisterWorkers    = 5
	defaultTopUpRetryInterval = 5 * time.Second
	defaultRegisterTimeout    = 6 * time.Minute
)

var (
	registerCodexCredential = plus.RegisterCodexCredential
	topUpMu                 sync.Mutex
)

type topUpOptions struct {
	TargetCount      int
	RegisterTimeout  time.Duration
	RegisterWorkers  int
	ProxyPool        []string
	TelegramBotToken string
	TelegramChatID   string
}

func topUpAccounts(ctx context.Context, store *TokenStore, dataDir string, opts topUpOptions) error {
	if opts.TargetCount <= 0 {
		return nil
	}

	topUpMu.Lock()
	defer topUpMu.Unlock()

	validAccounts := store.ValidAccountCount()
	pendingAccounts, err := countPendingPurchases(dataDir)
	if err != nil {
		return fmt.Errorf("count pending purchases: %w", err)
	}
	trackedAccounts := validAccounts + pendingAccounts
	if trackedAccounts >= opts.TargetCount {
		slog.Info("account top-up skipped",
			"valid_accounts", validAccounts,
			"pending_accounts", pendingAccounts,
			"tracked_accounts", trackedAccounts,
			"target", opts.TargetCount,
		)
		return nil
	}

	missing := opts.TargetCount - trackedAccounts
	registerWorkers := resolveRegisterWorkers(opts.RegisterWorkers)
	slog.Info("account top-up begin",
		"valid_accounts", validAccounts,
		"pending_accounts", pendingAccounts,
		"tracked_accounts", trackedAccounts,
		"target", opts.TargetCount,
		"missing", missing,
		"workers", registerWorkers,
	)

	if err := topUpMissingAccounts(ctx, store, dataDir, missing, opts); err != nil {
		return fmt.Errorf("top up missing accounts: %w", err)
	}

	validAccounts = store.ValidAccountCount()
	pendingAccounts, err = countPendingPurchases(dataDir)
	if err != nil {
		return fmt.Errorf("count pending purchases: %w", err)
	}
	trackedAccounts = validAccounts + pendingAccounts
	if trackedAccounts < opts.TargetCount {
		return fmt.Errorf("tracked accounts %d below target %d", trackedAccounts, opts.TargetCount)
	}

	slog.Info("account top-up complete",
		"valid_accounts", validAccounts,
		"pending_accounts", pendingAccounts,
		"tracked_accounts", trackedAccounts,
		"target", opts.TargetCount,
	)
	return nil
}

func topUpMissingAccounts(ctx context.Context, store *TokenStore, dataDir string, missing int, opts topUpOptions) error {
	if missing <= 0 {
		return nil
	}

	retryInterval := defaultTopUpRetryInterval
	remaining := missing
	for trial := 1; remaining > 0; trial++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("account top-up canceled: %w", err)
		}

		requested := remaining
		succeeded, err := registerBatch(ctx, dataDir, requested, trial, opts)
		if err != nil {
			return err
		}
		remaining -= succeeded

		pendingAccounts, countErr := countPendingPurchases(dataDir)
		if countErr != nil {
			slog.Warn("count pending purchases after registration",
				"trial", trial,
				"err", countErr,
			)
		}

		slog.Info("account top-up trial complete",
			"trial", trial,
			"requested", requested,
			"succeeded", succeeded,
			"remaining", remaining,
			"valid_accounts", store.ValidAccountCount(),
			"pending_accounts", pendingAccounts,
		)

		if remaining > 0 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("account top-up canceled: %w", ctx.Err())
			case <-time.After(retryInterval):
			}
		}
	}
	return nil
}

func registerBatch(
	ctx context.Context,
	dataDir string,
	attempts int,
	trial int,
	opts topUpOptions,
) (int, error) {
	if attempts <= 0 {
		return 0, nil
	}

	registerTimeout := opts.RegisterTimeout
	if registerTimeout <= 0 {
		registerTimeout = defaultRegisterTimeout
	}
	registerWorkers := resolveRegisterWorkers(opts.RegisterWorkers)

	workerCount := min(registerWorkers, attempts)
	jobs := make(chan int)
	var wg sync.WaitGroup
	var succeeded atomic.Int32

	for workerID := range workerCount {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for attempt := range jobs {
				if err := ctx.Err(); err != nil {
					return
				}

				registerCtx, cancel := context.WithTimeout(ctx, registerTimeout)
				result, err := registerCodexCredential(registerCtx, plus.RegisterOptions{
					DataDir:             dataDir,
					RegistrationProxies: opts.ProxyPool,
					TelegramBotToken:    opts.TelegramBotToken,
					TelegramChatID:      opts.TelegramChatID,
				})
				cancel()
				if err != nil {
					slog.Warn("account registration failed",
						"trial", trial,
						"attempt", attempt,
						"worker", workerID,
						"err", err,
					)
					continue
				}

				succeeded.Add(1)

				slog.Info("account registration succeeded",
					"trial", trial,
					"attempt", attempt,
					"worker", workerID,
					"email", result.Email,
					"account_id", result.AccountID,
					"file", result.FilePath,
				)
			}
		}(workerID + 1)
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return 0, fmt.Errorf("account top-up canceled: %w", ctx.Err())
		case jobs <- attempt:
		}
	}
	close(jobs)
	wg.Wait()
	return int(succeeded.Load()), nil
}

func resolveRegisterWorkers(workers int) int {
	if workers <= 0 {
		return defaultRegisterWorkers
	}
	return workers
}
