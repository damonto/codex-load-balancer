package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/damonto/codex-load-balancer/plus"
)

const (
	defaultTopUpRetryInterval = 5 * time.Second
)

var (
	registerCodexCredential = plus.RegisterCodexCredential
	topUpMu                 sync.Mutex
)

type topUpOptions struct {
	Enabled         bool
	TargetCount     int
	RegisterTimeout time.Duration
	RegisterWorkers int
	ProxyPool       plus.RegistrationProxyPool
	PurchaseConfig  plus.PurchaseConfig
}

func topUpAccounts(ctx context.Context, store *TokenStore, dataDir string, opts topUpOptions) error {
	if !opts.Enabled {
		return nil
	}
	if opts.TargetCount <= 0 {
		return nil
	}
	if opts.RegisterWorkers <= 0 {
		return errors.New("register workers must be positive")
	}
	if opts.RegisterTimeout <= 0 {
		return errors.New("register timeout must be positive")
	}

	topUpMu.Lock()
	defer topUpMu.Unlock()

	validAccounts := store.ValidAccountCount()
	if validAccounts >= opts.TargetCount {
		slog.Info("account top-up skipped",
			"active_accounts", validAccounts,
			"target", opts.TargetCount,
		)
		return nil
	}

	missing := opts.TargetCount - validAccounts
	slog.Info("account top-up begin",
		"active_accounts", validAccounts,
		"target", opts.TargetCount,
		"missing", missing,
		"workers", opts.RegisterWorkers,
	)

	if err := topUpMissingAccounts(ctx, store, dataDir, missing, opts); err != nil {
		return fmt.Errorf("top up missing accounts: %w", err)
	}

	validAccounts = store.ValidAccountCount()
	if validAccounts < opts.TargetCount {
		return fmt.Errorf("active accounts %d below target %d", validAccounts, opts.TargetCount)
	}

	slog.Info("account top-up complete",
		"active_accounts", validAccounts,
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
			"active_accounts", store.ValidAccountCount(),
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
	workerCount := min(opts.RegisterWorkers, attempts)
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

				registerCtx, cancel := context.WithTimeout(ctx, opts.RegisterTimeout)
				result, err := registerCodexCredential(registerCtx, plus.RegisterOptions{
					DataDir:               dataDir,
					RegistrationProxyPool: opts.ProxyPool,
					Purchase:              opts.PurchaseConfig,
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

				if err := storeRegisteredCredential(store, result); err != nil {
					slog.Warn("registered credential not loaded",
						"trial", trial,
						"attempt", attempt,
						"worker", workerID,
						"email", result.Email,
						"file", result.FilePath,
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

func storeRegisteredCredential(store *TokenStore, result plus.RegisterResult) error {
	if store == nil {
		return errors.New("token store is nil")
	}

	state, modTime, err := tokenStateFromRegisterResult(result)
	if err != nil {
		return err
	}
	store.UpsertToken(state, modTime)
	return nil
}

func tokenStateFromRegisterResult(result plus.RegisterResult) (TokenState, time.Time, error) {
	filePath := strings.TrimSpace(result.FilePath)
	if filePath == "" {
		return TokenState{}, time.Time{}, errors.New("register result file path is empty")
	}

	modTime := time.Now().UTC()
	accessToken := strings.TrimSpace(result.Tokens.AccessToken)
	accountID := strings.TrimSpace(result.AccountID)
	refreshToken := strings.TrimSpace(result.Tokens.RefreshToken)
	lastRefresh := time.Time{}
	email := strings.TrimSpace(result.Session.User.Email)
	planType := strings.TrimSpace(result.Session.Account.PlanType)

	info, err := os.Stat(filePath)
	switch {
	case err == nil:
		modTime = info.ModTime()
		parsedAccessToken, parsedAccountID, parsedRefreshToken, parsedLastRefresh, err := parseAuthFile(filePath)
		if err != nil {
			return TokenState{}, time.Time{}, fmt.Errorf("parse registered credential: %w", err)
		}
		accessToken = parsedAccessToken
		if parsedAccountID != "" {
			accountID = parsedAccountID
		}
		if parsedRefreshToken != "" {
			refreshToken = parsedRefreshToken
		}
		if !parsedLastRefresh.IsZero() {
			lastRefresh = parsedLastRefresh
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return TokenState{}, time.Time{}, fmt.Errorf("stat registered credential: %w", err)
	}

	if accessToken == "" {
		return TokenState{}, time.Time{}, errors.New("register result access token is empty")
	}
	if accountID == "" {
		return TokenState{}, time.Time{}, errors.New("register result account id is empty")
	}
	if email == "" {
		email = strings.TrimSpace(result.Email)
	}

	return TokenState{
		ID:           filepath.Base(filePath),
		Path:         filePath,
		Token:        accessToken,
		AccountID:    accountID,
		Email:        email,
		PlanType:     planType,
		RefreshToken: refreshToken,
		LastRefresh:  lastRefresh,
	}, modTime, nil
}
