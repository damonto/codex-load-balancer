package main

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type UsageRecord struct {
	AccountKey      string
	TokenID         string
	Path            string
	StatusCode      int
	IsStream        bool
	InputTokens     int64
	CachedTokens    int64
	OutputTokens    int64
	ReasoningTokens int64
	CreatedAt       time.Time
}

type UsageTotals struct {
	InputTokens     int64 `json:"input_tokens"`
	CachedTokens    int64 `json:"cached_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

func (t UsageTotals) TotalTokens() int64 {
	return t.InputTokens + t.CachedTokens + t.OutputTokens
}

type AccountUsageSummary struct {
	AccountKey      string
	InputTokens     int64
	CachedTokens    int64
	OutputTokens    int64
	ReasoningTokens int64
	Used5hTokens    int64
	UsedWeekTokens  int64
	Quota5hTokens   int64
	QuotaWeekTokens int64
	ActiveTokenIDs  []string
}

func (s AccountUsageSummary) TotalTokens() int64 {
	return s.InputTokens + s.CachedTokens + s.OutputTokens
}

type UsagePoint struct {
	Bucket       string `json:"bucket"`
	InputTokens  int64  `json:"input_tokens"`
	CachedTokens int64  `json:"cached_tokens"`
	OutputTokens int64  `json:"output_tokens"`
}

func (p UsagePoint) TotalTokens() int64 {
	return p.InputTokens + p.CachedTokens + p.OutputTokens
}

type AccountUsageTrends struct {
	Daily   []UsagePoint `json:"daily"`
	Weekly  []UsagePoint `json:"weekly"`
	Monthly []UsagePoint `json:"monthly"`
}

type UsageDB struct {
	db *sql.DB
}

const insertUsageSQL = `
INSERT INTO usage_events (
	account_key,
	token_id,
	request_path,
	status_code,
	is_stream,
	input_tokens,
	cached_tokens,
	output_tokens,
	reasoning_tokens,
	created_at_unix
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`

func openUsageDB(path string) (*UsageDB, error) {
	dsn := fmt.Sprintf("file:%s?mode=rwc&_journal_mode=WAL&_synchronous=NORMAL", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open usage db: %w", err)
	}
	// SQLite allows only one writer at a time; a single connection prevents
	// "database is locked" errors under concurrent dashboard reads + sink writes.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping usage db: %w", err)
	}

	store := &UsageDB{db: db}
	if err := store.initSchema(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *UsageDB) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *UsageDB) initSchema(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS usage_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	account_key TEXT NOT NULL,
	token_id TEXT NOT NULL,
	request_path TEXT NOT NULL,
	status_code INTEGER NOT NULL,
	is_stream INTEGER NOT NULL,
	input_tokens INTEGER NOT NULL,
	cached_tokens INTEGER NOT NULL,
	output_tokens INTEGER NOT NULL,
	reasoning_tokens INTEGER NOT NULL DEFAULT 0,
	created_at_unix INTEGER NOT NULL
);
`); err != nil {
		return fmt.Errorf("create usage_events: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
CREATE INDEX IF NOT EXISTS idx_usage_events_account_time
ON usage_events(account_key, created_at_unix);
`); err != nil {
		return fmt.Errorf("create idx_usage_events_account_time: %w", err)
	}
	if err := s.ensureColumn(
		ctx,
		"usage_events",
		"reasoning_tokens",
		`ALTER TABLE usage_events ADD COLUMN reasoning_tokens INTEGER NOT NULL DEFAULT 0`,
	); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS account_quotas (
	account_key TEXT PRIMARY KEY,
	quota_5h_tokens INTEGER NOT NULL,
	quota_week_tokens INTEGER NOT NULL,
	updated_at_unix INTEGER NOT NULL
);
`); err != nil {
		return fmt.Errorf("create account_quotas: %w", err)
	}
	return nil
}

func (s *UsageDB) ensureColumn(ctx context.Context, tableName string, columnName string, addColumnSQL string) error {
	hasColumn, err := s.columnExists(ctx, tableName, columnName)
	if err != nil {
		return err
	}
	if hasColumn {
		return nil
	}

	if _, err := s.db.ExecContext(ctx, addColumnSQL); err != nil {
		return fmt.Errorf("add %s.%s column: %w", tableName, columnName, err)
	}
	return nil
}

func (s *UsageDB) columnExists(ctx context.Context, tableName string, columnName string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%q)`, tableName))
	if err != nil {
		return false, fmt.Errorf("query %s columns: %w", tableName, err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			columnID     int
			name         string
			dataType     string
			notNull      int
			defaultValue sql.NullString
			primaryKey   int
		)
		if err := rows.Scan(&columnID, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, fmt.Errorf("scan %s column: %w", tableName, err)
		}
		if name == columnName {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate %s columns: %w", tableName, err)
	}
	return false, nil
}

func (s *UsageDB) InsertUsage(ctx context.Context, rec UsageRecord) error {
	if err := execInsertUsage(ctx, s.db, rec); err != nil {
		return fmt.Errorf("insert usage event: %w", err)
	}
	return nil
}

func (s *UsageDB) InsertUsageBatch(ctx context.Context, records []UsageRecord) error {
	if len(records) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin usage batch: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	for _, rec := range records {
		if err := execInsertUsage(ctx, tx, rec); err != nil {
			return fmt.Errorf("insert usage batch: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit usage batch: %w", err)
	}
	committed = true
	return nil
}

type usageExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func execInsertUsage(ctx context.Context, execer usageExecer, rec UsageRecord) error {
	createdAt := rec.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	_, err := execer.ExecContext(
		ctx,
		insertUsageSQL,
		rec.AccountKey,
		rec.TokenID,
		rec.Path,
		rec.StatusCode,
		boolToInt(rec.IsStream),
		rec.InputTokens,
		rec.CachedTokens,
		rec.OutputTokens,
		rec.ReasoningTokens,
		createdAt.Unix(),
	)
	return err
}

func (s *UsageDB) GlobalTotals(ctx context.Context) (UsageTotals, error) {
	var totals UsageTotals
	if err := s.db.QueryRowContext(ctx, `
SELECT
	COALESCE(SUM(input_tokens), 0),
	COALESCE(SUM(cached_tokens), 0),
	COALESCE(SUM(output_tokens), 0),
	COALESCE(SUM(reasoning_tokens), 0)
FROM usage_events
`).Scan(&totals.InputTokens, &totals.CachedTokens, &totals.OutputTokens, &totals.ReasoningTokens); err != nil {
		return UsageTotals{}, fmt.Errorf("query global totals: %w", err)
	}
	return totals, nil
}

type GlobalPeriodTotals struct {
	Daily        UsageTotals
	Recent7Days  UsageTotals
	Recent30Days UsageTotals
	Total        UsageTotals
}

func (s *UsageDB) GlobalPeriodTotals(ctx context.Context) (GlobalPeriodTotals, error) {
	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	sevenDayStart := now.AddDate(0, 0, -7)
	thirtyDayStart := now.AddDate(0, 0, -30)

	var r GlobalPeriodTotals
	err := s.db.QueryRowContext(ctx, `
SELECT
	COALESCE(SUM(input_tokens), 0),
	COALESCE(SUM(cached_tokens), 0),
	COALESCE(SUM(output_tokens), 0),
	COALESCE(SUM(reasoning_tokens), 0),
	COALESCE(SUM(CASE WHEN created_at_unix >= ? THEN input_tokens ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN created_at_unix >= ? THEN cached_tokens ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN created_at_unix >= ? THEN output_tokens ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN created_at_unix >= ? THEN reasoning_tokens ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN created_at_unix >= ? THEN input_tokens ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN created_at_unix >= ? THEN cached_tokens ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN created_at_unix >= ? THEN output_tokens ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN created_at_unix >= ? THEN reasoning_tokens ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN created_at_unix >= ? THEN input_tokens ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN created_at_unix >= ? THEN cached_tokens ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN created_at_unix >= ? THEN output_tokens ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN created_at_unix >= ? THEN reasoning_tokens ELSE 0 END), 0)
FROM usage_events
	`,
		dayStart.Unix(), dayStart.Unix(), dayStart.Unix(), dayStart.Unix(),
		sevenDayStart.Unix(), sevenDayStart.Unix(), sevenDayStart.Unix(), sevenDayStart.Unix(),
		thirtyDayStart.Unix(), thirtyDayStart.Unix(), thirtyDayStart.Unix(), thirtyDayStart.Unix(),
	).Scan(
		&r.Total.InputTokens, &r.Total.CachedTokens, &r.Total.OutputTokens, &r.Total.ReasoningTokens,
		&r.Daily.InputTokens, &r.Daily.CachedTokens, &r.Daily.OutputTokens, &r.Daily.ReasoningTokens,
		&r.Recent7Days.InputTokens, &r.Recent7Days.CachedTokens, &r.Recent7Days.OutputTokens, &r.Recent7Days.ReasoningTokens,
		&r.Recent30Days.InputTokens, &r.Recent30Days.CachedTokens, &r.Recent30Days.OutputTokens, &r.Recent30Days.ReasoningTokens,
	)
	if err != nil {
		return GlobalPeriodTotals{}, fmt.Errorf("query global period totals: %w", err)
	}
	return r, nil
}

func (s *UsageDB) quotaOverrides(ctx context.Context) (map[string]AccountQuota, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT account_key, quota_5h_tokens, quota_week_tokens
FROM account_quotas
`)
	if err != nil {
		return nil, fmt.Errorf("query account quota overrides: %w", err)
	}
	defer rows.Close()

	overrides := make(map[string]AccountQuota)
	for rows.Next() {
		var accountKey string
		var quota5h int64
		var quotaWeek int64
		if err := rows.Scan(&accountKey, &quota5h, &quotaWeek); err != nil {
			return nil, fmt.Errorf("scan account quota override: %w", err)
		}
		overrides[accountKey] = AccountQuota{
			Quota5hTokens:   quota5h,
			QuotaWeekTokens: quotaWeek,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate account quota overrides: %w", err)
	}
	return overrides, nil
}

func (s *UsageDB) accountSummaries(ctx context.Context, cutoff5h time.Time, cutoffWeek time.Time) (map[string]*AccountUsageSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT
	account_key,
	COALESCE(SUM(input_tokens), 0),
	COALESCE(SUM(cached_tokens), 0),
	COALESCE(SUM(output_tokens), 0),
	COALESCE(SUM(reasoning_tokens), 0),
	COALESCE(SUM(CASE WHEN created_at_unix >= ? THEN input_tokens + cached_tokens + output_tokens ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN created_at_unix >= ? THEN input_tokens + cached_tokens + output_tokens ELSE 0 END), 0)
FROM usage_events
GROUP BY account_key
`, cutoff5h.Unix(), cutoffWeek.Unix())
	if err != nil {
		return nil, fmt.Errorf("query account summaries: %w", err)
	}
	defer rows.Close()

	summaries := make(map[string]*AccountUsageSummary)
	for rows.Next() {
		summary := &AccountUsageSummary{}
		if err := rows.Scan(
			&summary.AccountKey,
			&summary.InputTokens,
			&summary.CachedTokens,
			&summary.OutputTokens,
			&summary.ReasoningTokens,
			&summary.Used5hTokens,
			&summary.UsedWeekTokens,
		); err != nil {
			return nil, fmt.Errorf("scan account summary: %w", err)
		}
		summaries[summary.AccountKey] = summary
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate account summaries: %w", err)
	}
	return summaries, nil
}

func (s *UsageDB) ListAccountSummaries(
	ctx context.Context,
	activeAccountTokens map[string][]string,
	quota5hDefault int64,
	quotaWeekDefault int64,
) ([]AccountUsageSummary, error) {
	now := time.Now().UTC()
	cutoff5h := now.Add(-5 * time.Hour)
	cutoffWeek := weekStartUTC(now)

	summariesByAccount, err := s.accountSummaries(ctx, cutoff5h, cutoffWeek)
	if err != nil {
		return nil, err
	}
	overrides, err := s.quotaOverrides(ctx)
	if err != nil {
		return nil, err
	}

	for accountKey, tokenIDs := range activeAccountTokens {
		summary, ok := summariesByAccount[accountKey]
		if !ok {
			summary = &AccountUsageSummary{AccountKey: accountKey}
			summariesByAccount[accountKey] = summary
		}
		summary.ActiveTokenIDs = append(summary.ActiveTokenIDs, tokenIDs...)
	}

	results := make([]AccountUsageSummary, 0, len(summariesByAccount))
	for accountKey, summary := range summariesByAccount {
		override, ok := overrides[accountKey]
		if ok {
			summary.Quota5hTokens = override.Quota5hTokens
			summary.QuotaWeekTokens = override.QuotaWeekTokens
		}
		if summary.Quota5hTokens <= 0 {
			summary.Quota5hTokens = quota5hDefault
		}
		if summary.QuotaWeekTokens <= 0 {
			summary.QuotaWeekTokens = quotaWeekDefault
		}

		slices.Sort(summary.ActiveTokenIDs)
		summary.ActiveTokenIDs = slices.Compact(summary.ActiveTokenIDs)
		results = append(results, *summary)
	}

	slices.SortFunc(results, func(a, b AccountUsageSummary) int {
		if a.TotalTokens() != b.TotalTokens() {
			return cmp.Compare(b.TotalTokens(), a.TotalTokens())
		}
		return cmp.Compare(a.AccountKey, b.AccountKey)
	})
	return results, nil
}

func (s *UsageDB) AccountTrends(ctx context.Context, accountKey string) ([]UsagePoint, []UsagePoint, []UsagePoint, error) {
	trendsByAccount, err := s.AccountTrendsBatch(ctx, []string{accountKey})
	if err != nil {
		return nil, nil, nil, err
	}

	trends := trendsByAccount[accountKey]
	return trends.Daily, trends.Weekly, trends.Monthly, nil
}

func (s *UsageDB) AccountTrendsBatch(ctx context.Context, accountKeys []string) (map[string]AccountUsageTrends, error) {
	keys := slices.Clone(accountKeys)
	slices.Sort(keys)
	keys = slices.Compact(keys)

	trendsByAccount := make(map[string]AccountUsageTrends, len(keys))
	for _, accountKey := range keys {
		trendsByAccount[accountKey] = AccountUsageTrends{
			Daily:   []UsagePoint{},
			Weekly:  []UsagePoint{},
			Monthly: []UsagePoint{},
		}
	}
	if len(keys) == 0 {
		return trendsByAccount, nil
	}

	daily, err := s.queryTrendsByAccounts(ctx, keys, "%Y-%m-%d", 30)
	if err != nil {
		return nil, err
	}
	weekly, err := s.queryTrendsByAccounts(ctx, keys, "%Y-W%W", 16)
	if err != nil {
		return nil, err
	}
	monthly, err := s.queryTrendsByAccounts(ctx, keys, "%Y-%m", 12)
	if err != nil {
		return nil, err
	}

	for _, accountKey := range keys {
		trends := trendsByAccount[accountKey]
		if points, ok := daily[accountKey]; ok {
			trends.Daily = points
		}
		if points, ok := weekly[accountKey]; ok {
			trends.Weekly = points
		}
		if points, ok := monthly[accountKey]; ok {
			trends.Monthly = points
		}
		trendsByAccount[accountKey] = trends
	}
	return trendsByAccount, nil
}

func (s *UsageDB) queryTrendsByAccounts(
	ctx context.Context,
	accountKeys []string,
	format string,
	limit int,
) (map[string][]UsagePoint, error) {
	placeholders := make([]string, 0, len(accountKeys))
	args := make([]any, 0, 1+len(accountKeys))
	args = append(args, format)
	for _, accountKey := range accountKeys {
		placeholders = append(placeholders, "?")
		args = append(args, accountKey)
	}

	query := fmt.Sprintf(`
SELECT
	account_key,
	strftime(?, created_at_unix, 'unixepoch') AS bucket,
	COALESCE(SUM(input_tokens), 0),
	COALESCE(SUM(cached_tokens), 0),
	COALESCE(SUM(output_tokens), 0)
FROM usage_events
WHERE account_key IN (%s)
GROUP BY account_key, bucket
ORDER BY account_key ASC, bucket DESC
	`, strings.Join(placeholders, ", "))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query trends by account: %w", err)
	}
	defer rows.Close()

	pointsByAccount := make(map[string][]UsagePoint, len(accountKeys))
	counts := make(map[string]int, len(accountKeys))
	for rows.Next() {
		var accountKey string
		var point UsagePoint
		if err := rows.Scan(&accountKey, &point.Bucket, &point.InputTokens, &point.CachedTokens, &point.OutputTokens); err != nil {
			return nil, fmt.Errorf("scan trend point: %w", err)
		}
		if counts[accountKey] >= limit {
			continue
		}
		pointsByAccount[accountKey] = append(pointsByAccount[accountKey], point)
		counts[accountKey]++
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate trend points: %w", err)
	}

	for accountKey, points := range pointsByAccount {
		slices.Reverse(points)
		pointsByAccount[accountKey] = points
	}
	return pointsByAccount, nil
}

type AccountQuota struct {
	Quota5hTokens   int64
	QuotaWeekTokens int64
}

func weekStartUTC(now time.Time) time.Time {
	now = now.UTC()
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	daysSinceMonday := weekday - 1
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return dayStart.AddDate(0, 0, -daysSinceMonday)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
