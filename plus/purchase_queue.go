package plus

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	purchaseTokenStatusAvailable = "available"
	purchaseTokenStatusLeased    = "leased"
	purchaseTokenStatusConsumed  = "consumed"
	purchaseTokenStatusDead      = "dead"

	defaultPurchaseTokenLeaseDuration = 15 * time.Minute
)

var ErrPurchaseTokenQueueEmpty = errors.New("purchase token queue is empty")

type PurchaseTokenStore struct {
	db            *sql.DB
	leaseDuration time.Duration
	now           func() time.Time
	ownsDB        bool
}

type PurchaseTokenImportResult struct {
	Inserted   int
	Duplicates int
}

type PurchaseTokenLease struct {
	store      *PurchaseTokenStore
	id         int64
	fetchToken string
	finalized  bool
}

func OpenPurchaseTokenStore(path string) (*PurchaseTokenStore, error) {
	dsn := fmt.Sprintf("file:%s?mode=rwc&_journal_mode=WAL&_synchronous=NORMAL", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open purchase db: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping purchase db: %w", err)
	}

	store := &PurchaseTokenStore{
		db:            db,
		leaseDuration: defaultPurchaseTokenLeaseDuration,
		now:           time.Now,
		ownsDB:        true,
	}
	if err := store.initSchema(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func NewPurchaseTokenStore(db *sql.DB) (*PurchaseTokenStore, error) {
	if db == nil {
		return nil, errors.New("purchase db is nil")
	}

	store := &PurchaseTokenStore{
		db:            db,
		leaseDuration: defaultPurchaseTokenLeaseDuration,
		now:           time.Now,
	}
	if err := store.initSchema(context.Background()); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *PurchaseTokenStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	if !s.ownsDB {
		return nil
	}
	return s.db.Close()
}

func (s *PurchaseTokenStore) ImportFetchTokens(ctx context.Context, fetchTokens []string) (PurchaseTokenImportResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PurchaseTokenImportResult{}, fmt.Errorf("begin purchase token import: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
INSERT OR IGNORE INTO purchase_tokens (fetch_token, status)
VALUES (?, ?)
`)
	if err != nil {
		return PurchaseTokenImportResult{}, fmt.Errorf("prepare purchase token import: %w", err)
	}
	defer stmt.Close()

	var result PurchaseTokenImportResult
	for _, fetchToken := range fetchTokens {
		fetchToken = strings.TrimSpace(fetchToken)
		if fetchToken == "" {
			continue
		}

		execResult, err := stmt.ExecContext(ctx, fetchToken, purchaseTokenStatusAvailable)
		if err != nil {
			return PurchaseTokenImportResult{}, fmt.Errorf("insert purchase token: %w", err)
		}
		rowsAffected, err := execResult.RowsAffected()
		if err != nil {
			return PurchaseTokenImportResult{}, fmt.Errorf("load purchase token import rows: %w", err)
		}
		if rowsAffected == 0 {
			result.Duplicates++
			continue
		}
		result.Inserted++
	}

	if err := tx.Commit(); err != nil {
		return PurchaseTokenImportResult{}, fmt.Errorf("commit purchase token import: %w", err)
	}
	return result, nil
}

func (s *PurchaseTokenStore) LeaseToken(ctx context.Context) (*PurchaseTokenLease, error) {
	now := s.now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin purchase token lease: %w", err)
	}
	defer tx.Rollback()

	if err := s.reclaimExpiredLeasesTx(ctx, tx, now); err != nil {
		return nil, err
	}

	var (
		id         int64
		fetchToken string
	)
	err = tx.QueryRowContext(ctx, `
SELECT id, fetch_token
FROM purchase_tokens
WHERE status = ?
ORDER BY created_at_unix, id
LIMIT 1
`, purchaseTokenStatusAvailable).Scan(&id, &fetchToken)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPurchaseTokenQueueEmpty
		}
		return nil, fmt.Errorf("select purchase token: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
UPDATE purchase_tokens
SET
	status = ?,
	leased_at_unix = ?,
	lease_expires_at_unix = ?,
	consumed_at_unix = NULL,
	account_id = NULL,
	last_error = '',
	response_status_code = NULL
WHERE id = ?
`, purchaseTokenStatusLeased, now.Unix(), now.Add(s.leaseDuration).Unix(), id); err != nil {
		return nil, fmt.Errorf("lease purchase token %d: %w", id, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit purchase token lease: %w", err)
	}

	return &PurchaseTokenLease{
		store:      s,
		id:         id,
		fetchToken: fetchToken,
	}, nil
}

func (l *PurchaseTokenLease) ID() int64 {
	if l == nil {
		return 0
	}
	return l.id
}

func (l *PurchaseTokenLease) FetchToken() string {
	if l == nil {
		return ""
	}
	return l.fetchToken
}

func (l *PurchaseTokenLease) Release(ctx context.Context) error {
	if l == nil || l.finalized {
		return nil
	}
	if err := l.store.updateLeaseStatus(ctx, l.id, purchaseTokenStatusAvailable, leaseStatusUpdate{}); err != nil {
		return err
	}
	l.finalized = true
	return nil
}

func (l *PurchaseTokenLease) MarkConsumed(ctx context.Context, accountID string, statusCode int) error {
	if l == nil || l.finalized {
		return nil
	}
	if err := l.store.updateLeaseStatus(ctx, l.id, purchaseTokenStatusConsumed, leaseStatusUpdate{
		Attempted:  true,
		AccountID:  strings.TrimSpace(accountID),
		StatusCode: &statusCode,
		ConsumedAt: l.store.now().UTC(),
	}); err != nil {
		return err
	}
	l.finalized = true
	return nil
}

func (l *PurchaseTokenLease) MarkRetryable(ctx context.Context, accountID string, statusCode int, lastErr string) error {
	if l == nil || l.finalized {
		return nil
	}
	if err := l.store.updateLeaseStatus(ctx, l.id, purchaseTokenStatusAvailable, leaseStatusUpdate{
		Attempted:  true,
		AccountID:  strings.TrimSpace(accountID),
		StatusCode: &statusCode,
		LastError:  strings.TrimSpace(lastErr),
	}); err != nil {
		return err
	}
	l.finalized = true
	return nil
}

func (l *PurchaseTokenLease) MarkDead(ctx context.Context, accountID string, statusCode *int, lastErr string) error {
	if l == nil || l.finalized {
		return nil
	}
	if err := l.store.updateLeaseStatus(ctx, l.id, purchaseTokenStatusDead, leaseStatusUpdate{
		Attempted:  true,
		AccountID:  strings.TrimSpace(accountID),
		StatusCode: statusCode,
		LastError:  strings.TrimSpace(lastErr),
	}); err != nil {
		return err
	}
	l.finalized = true
	return nil
}

type leaseStatusUpdate struct {
	Attempted  bool
	AccountID  string
	StatusCode *int
	LastError  string
	ConsumedAt time.Time
}

func (s *PurchaseTokenStore) updateLeaseStatus(ctx context.Context, id int64, status string, update leaseStatusUpdate) error {
	accountID := strings.TrimSpace(update.AccountID)
	lastErr := strings.TrimSpace(update.LastError)

	var (
		statusCode any
		consumedAt any
	)
	if update.StatusCode != nil {
		statusCode = *update.StatusCode
	}
	if !update.ConsumedAt.IsZero() {
		consumedAt = update.ConsumedAt.UTC().Unix()
	}

	attemptIncrement := 0
	if update.Attempted {
		attemptIncrement = 1
	}

	result, err := s.db.ExecContext(ctx, `
UPDATE purchase_tokens
SET
	status = ?,
	attempt_count = attempt_count + ?,
	leased_at_unix = NULL,
	lease_expires_at_unix = NULL,
	consumed_at_unix = ?,
	account_id = NULLIF(?, ''),
	last_error = ?,
	response_status_code = ?
WHERE id = ? AND status = ?
`, status, attemptIncrement, consumedAt, accountID, lastErr, statusCode, id, purchaseTokenStatusLeased)
	if err != nil {
		return fmt.Errorf("update purchase token %d status to %s: %w", id, status, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("load purchase token %d update rows: %w", id, err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("purchase token %d is not leased", id)
	}
	return nil
}

func (s *PurchaseTokenStore) initSchema(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS purchase_tokens (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	fetch_token TEXT NOT NULL UNIQUE,
	status TEXT NOT NULL,
	attempt_count INTEGER NOT NULL DEFAULT 0,
	leased_at_unix INTEGER,
	lease_expires_at_unix INTEGER,
	consumed_at_unix INTEGER,
	account_id TEXT,
	last_error TEXT NOT NULL DEFAULT '',
	response_status_code INTEGER,
	created_at_unix INTEGER NOT NULL DEFAULT (CAST(strftime('%s', 'now') AS INTEGER))
);
`); err != nil {
		return fmt.Errorf("create purchase_tokens: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
CREATE INDEX IF NOT EXISTS idx_purchase_tokens_status_created
ON purchase_tokens(status, created_at_unix, id);
`); err != nil {
		return fmt.Errorf("create idx_purchase_tokens_status_created: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
CREATE INDEX IF NOT EXISTS idx_purchase_tokens_lease_expiry
ON purchase_tokens(status, lease_expires_at_unix);
`); err != nil {
		return fmt.Errorf("create idx_purchase_tokens_lease_expiry: %w", err)
	}
	return nil
}

func (s *PurchaseTokenStore) reclaimExpiredLeasesTx(ctx context.Context, tx *sql.Tx, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
UPDATE purchase_tokens
SET
	status = ?,
	leased_at_unix = NULL,
	lease_expires_at_unix = NULL
WHERE status = ? AND lease_expires_at_unix IS NOT NULL AND lease_expires_at_unix <= ?
`, purchaseTokenStatusAvailable, purchaseTokenStatusLeased, now.Unix()); err != nil {
		return fmt.Errorf("reclaim expired purchase token leases: %w", err)
	}
	return nil
}
