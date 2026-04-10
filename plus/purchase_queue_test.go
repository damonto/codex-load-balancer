package plus

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestPurchaseTokenStoreLeaseOldestAvailable(t *testing.T) {
	store := openPurchaseTokenStoreForTest(t)
	insertPurchaseTokenForTest(t, store, "fetch-token-b", purchaseTokenStatusAvailable, 20)
	insertPurchaseTokenForTest(t, store, "fetch-token-a", purchaseTokenStatusAvailable, 10)

	lease, err := store.LeaseToken(context.Background())
	if err != nil {
		t.Fatalf("LeaseToken() error = %v", err)
	}
	if lease.FetchToken() != "fetch-token-a" {
		t.Fatalf("FetchToken() = %q, want fetch-token-a", lease.FetchToken())
	}
}

func TestPurchaseTokenLeaseRelease(t *testing.T) {
	store := openPurchaseTokenStoreForTest(t)
	insertPurchaseTokenForTest(t, store, "fetch-token-1", purchaseTokenStatusAvailable, 1)

	lease, err := store.LeaseToken(context.Background())
	if err != nil {
		t.Fatalf("LeaseToken() error = %v", err)
	}
	if err := lease.Release(context.Background()); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	row := loadPurchaseTokenRowForTest(t, store, lease.ID())
	if row.status != purchaseTokenStatusAvailable {
		t.Fatalf("status = %q, want %q", row.status, purchaseTokenStatusAvailable)
	}
	if row.attemptCount != 0 {
		t.Fatalf("attempt_count = %d, want 0", row.attemptCount)
	}
}

func TestPurchaseTokenLeaseMarkConsumed(t *testing.T) {
	store := openPurchaseTokenStoreForTest(t)
	insertPurchaseTokenForTest(t, store, "fetch-token-1", purchaseTokenStatusAvailable, 1)

	lease, err := store.LeaseToken(context.Background())
	if err != nil {
		t.Fatalf("LeaseToken() error = %v", err)
	}
	if err := lease.MarkConsumed(context.Background(), "account-1", 200); err != nil {
		t.Fatalf("MarkConsumed() error = %v", err)
	}

	row := loadPurchaseTokenRowForTest(t, store, lease.ID())
	if row.status != purchaseTokenStatusConsumed {
		t.Fatalf("status = %q, want %q", row.status, purchaseTokenStatusConsumed)
	}
	if row.attemptCount != 1 {
		t.Fatalf("attempt_count = %d, want 1", row.attemptCount)
	}
}

func TestPurchaseTokenLeaseMarkRetryable(t *testing.T) {
	store := openPurchaseTokenStoreForTest(t)
	insertPurchaseTokenForTest(t, store, "fetch-token-1", purchaseTokenStatusAvailable, 1)

	lease, err := store.LeaseToken(context.Background())
	if err != nil {
		t.Fatalf("LeaseToken() error = %v", err)
	}
	if err := lease.MarkRetryable(context.Background(), "account-1", 503, "temporarily unavailable"); err != nil {
		t.Fatalf("MarkRetryable() error = %v", err)
	}

	row := loadPurchaseTokenRowForTest(t, store, lease.ID())
	if row.status != purchaseTokenStatusAvailable {
		t.Fatalf("status = %q, want %q", row.status, purchaseTokenStatusAvailable)
	}
	if row.lastError != "temporarily unavailable" {
		t.Fatalf("last_error = %q, want temporarily unavailable", row.lastError)
	}
}

func TestPurchaseTokenLeaseMarkDead(t *testing.T) {
	store := openPurchaseTokenStoreForTest(t)
	insertPurchaseTokenForTest(t, store, "fetch-token-1", purchaseTokenStatusAvailable, 1)

	lease, err := store.LeaseToken(context.Background())
	if err != nil {
		t.Fatalf("LeaseToken() error = %v", err)
	}
	statusCode := 400
	if err := lease.MarkDead(context.Background(), "account-1", &statusCode, "bad request"); err != nil {
		t.Fatalf("MarkDead() error = %v", err)
	}

	row := loadPurchaseTokenRowForTest(t, store, lease.ID())
	if row.status != purchaseTokenStatusDead {
		t.Fatalf("status = %q, want %q", row.status, purchaseTokenStatusDead)
	}
	if row.lastError != "bad request" {
		t.Fatalf("last_error = %q, want bad request", row.lastError)
	}
}

func TestPurchaseTokenStoreReclaimsExpiredLease(t *testing.T) {
	store := openPurchaseTokenStoreForTest(t)
	store.now = func() time.Time {
		return time.Unix(100, 0).UTC()
	}
	insertLeasedPurchaseTokenForTest(t, store, "expired-token", 1, 50)
	insertPurchaseTokenForTest(t, store, "fresh-token", purchaseTokenStatusAvailable, 2)

	lease, err := store.LeaseToken(context.Background())
	if err != nil {
		t.Fatalf("LeaseToken() error = %v", err)
	}
	if lease.FetchToken() != "expired-token" {
		t.Fatalf("FetchToken() = %q, want expired-token", lease.FetchToken())
	}
}

func TestPurchaseTokenStoreConcurrentLeaseDistinctTokens(t *testing.T) {
	store := openPurchaseTokenStoreForTest(t)
	for i := 1; i <= 3; i++ {
		insertPurchaseTokenForTest(t, store, "fetch-token-"+string(rune('0'+i)), purchaseTokenStatusAvailable, int64(i))
	}

	var (
		mu    sync.Mutex
		ids   []int64
		wg    sync.WaitGroup
		errCh = make(chan error, 3)
	)
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, err := store.LeaseToken(context.Background())
			if err != nil {
				errCh <- err
				return
			}
			mu.Lock()
			ids = append(ids, lease.ID())
			mu.Unlock()
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("LeaseToken() error = %v", err)
		}
	}

	slices.Sort(ids)
	if !slices.Equal(ids, []int64{1, 2, 3}) {
		t.Fatalf("leased ids = %v, want [1 2 3]", ids)
	}
}

func TestPurchaseTokenStoreImportFetchTokens(t *testing.T) {
	store := openPurchaseTokenStoreForTest(t)
	insertPurchaseTokenForTest(t, store, "fetch-token-1", purchaseTokenStatusAvailable, 1)

	result, err := store.ImportFetchTokens(context.Background(), []string{
		"fetch-token-1",
		"fetch-token-2",
		" ",
		"fetch-token-3",
	})
	if err != nil {
		t.Fatalf("ImportFetchTokens() error = %v", err)
	}
	if result.Inserted != 2 {
		t.Fatalf("Inserted = %d, want 2", result.Inserted)
	}
	if result.Duplicates != 1 {
		t.Fatalf("Duplicates = %d, want 1", result.Duplicates)
	}

	for _, want := range []string{"fetch-token-1", "fetch-token-2", "fetch-token-3"} {
		lease, err := store.LeaseToken(context.Background())
		if err != nil {
			t.Fatalf("LeaseToken() error = %v", err)
		}
		if lease.FetchToken() != want {
			t.Fatalf("FetchToken() = %q, want %q", lease.FetchToken(), want)
		}
	}
}

type purchaseTokenRow struct {
	status             string
	attemptCount       int
	accountID          sql.NullString
	lastError          string
	responseStatusCode sql.NullInt64
}

func openPurchaseTokenStoreForTest(t *testing.T) *PurchaseTokenStore {
	t.Helper()

	store, err := OpenPurchaseTokenStore(filepath.Join(t.TempDir(), "purchase.db"))
	if err != nil {
		t.Fatalf("OpenPurchaseTokenStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
	return store
}

func insertPurchaseTokenForTest(t *testing.T, store *PurchaseTokenStore, fetchToken string, status string, createdAtUnix int64) {
	t.Helper()

	if _, err := store.db.ExecContext(context.Background(), `
INSERT INTO purchase_tokens (fetch_token, status, created_at_unix)
VALUES (?, ?, ?)
`, fetchToken, status, createdAtUnix); err != nil {
		t.Fatalf("insertPurchaseTokenForTest() error = %v", err)
	}
}

func insertLeasedPurchaseTokenForTest(t *testing.T, store *PurchaseTokenStore, fetchToken string, createdAtUnix int64, leaseExpiresAtUnix int64) {
	t.Helper()

	if _, err := store.db.ExecContext(context.Background(), `
INSERT INTO purchase_tokens (fetch_token, status, leased_at_unix, lease_expires_at_unix, created_at_unix)
VALUES (?, ?, ?, ?, ?)
`, fetchToken, purchaseTokenStatusLeased, createdAtUnix, leaseExpiresAtUnix, createdAtUnix); err != nil {
		t.Fatalf("insertLeasedPurchaseTokenForTest() error = %v", err)
	}
}

func loadPurchaseTokenRowForTest(t *testing.T, store *PurchaseTokenStore, id int64) purchaseTokenRow {
	t.Helper()

	var row purchaseTokenRow
	if err := store.db.QueryRowContext(context.Background(), `
SELECT status, attempt_count, account_id, last_error, response_status_code
FROM purchase_tokens
WHERE id = ?
`, id).Scan(&row.status, &row.attemptCount, &row.accountID, &row.lastError, &row.responseStatusCode); err != nil {
		t.Fatalf("loadPurchaseTokenRowForTest() error = %v", err)
	}
	return row
}
