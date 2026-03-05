package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/damonto/codex-load-balancer/account"
)

const (
	defaultRegisterWorkers    = 5
	defaultTopUpRetryInterval = 5 * time.Second
	defaultRegisterTimeout    = 6 * time.Minute
)

var (
	registerCodexCredential = account.RegisterCodexCredential
	reloadTokensFromDir     = loadTokensFromDir
)

type topUpOptions struct {
	TargetCount     int
	RegisterTimeout time.Duration
	RegisterWorkers int
	ProxyPool       []string
}

func topUpAccounts(ctx context.Context, store *TokenStore, dataDir string, opts topUpOptions) error {
	if opts.TargetCount <= 0 {
		return nil
	}

	current := store.ValidAccountCount()
	if current >= opts.TargetCount {
		slog.Info("startup account top-up skipped", "valid_accounts", current, "target", opts.TargetCount)
		return nil
	}

	missing := opts.TargetCount - current
	registerWorkers := resolveRegisterWorkers(opts.RegisterWorkers)
	slog.Info("startup account top-up begin",
		"valid_accounts", current,
		"target", opts.TargetCount,
		"missing", missing,
		"workers", registerWorkers,
	)

	if err := topUpMissingAccounts(ctx, store, dataDir, missing, opts); err != nil {
		return fmt.Errorf("top up missing accounts: %w", err)
	}

	current = store.ValidAccountCount()
	if current < opts.TargetCount {
		return fmt.Errorf("valid accounts %d below target %d", current, opts.TargetCount)
	}

	slog.Info("startup account top-up complete", "valid_accounts", current, "target", opts.TargetCount)
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
		succeeded, err := registerBatch(ctx, store, dataDir, requested, trial, opts)
		if err != nil {
			return err
		}
		remaining -= succeeded

		slog.Info("account top-up trial complete",
			"trial", trial,
			"requested", requested,
			"succeeded", succeeded,
			"remaining", remaining,
			"valid_accounts", store.ValidAccountCount(),
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
	store *TokenStore,
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
	var reloadMu sync.Mutex
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
				result, err := registerCodexCredential(registerCtx, account.RegisterOptions{
					DataDir:             dataDir,
					RegistrationProxies: opts.ProxyPool,
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

				reloadMu.Lock()
				err = reloadTokensFromDir(store, dataDir)
				reloadMu.Unlock()
				if err != nil {
					slog.Warn("reload token directory after registration",
						"trial", trial,
						"attempt", attempt,
						"worker", workerID,
						"path", dataDir,
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
					"valid_accounts", store.ValidAccountCount(),
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
