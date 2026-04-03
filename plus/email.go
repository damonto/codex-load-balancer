package plus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	randv2 "math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultEmailAPIURL           = "https://mailbox.sox.pm"
	defaultEmailLocalPartLen     = 12
	defaultEmailSubdomainRandLen = 3
)

var (
	errEmailNotFound = errors.New("no email found for address")
	emailAPIURL      = defaultEmailAPIURL
	emailHTTPClient  = &http.Client{Timeout: 15 * time.Second}
	emailDomains     = [...]string{}
)

type emailListRecord struct {
	EmailID    int    `json:"id,string"`
	SendEmail  string `json:"sender"`
	SendName   string `json:"sendName"`
	Subject    string `json:"subject"`
	ToEmail    string `json:"recipient"`
	CreateTime string `json:"received_at"`
	Content    string `json:"content"`
	Text       string `json:"plaintext"`
}

type emailCursor struct {
	EmailID      int
	CreatedAt    time.Time
	HasCreatedAt bool
}

// Generate creates a random email address using one of the configured inbox domains.
func Generate() (string, error) {
	return GenerateWithContext(context.Background())
}

// GenerateWithContext creates a random email address and respects cancellation.
func GenerateWithContext(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("generate email canceled: %w", err)
	}

	localPart, err := randomLocalPart(defaultEmailLocalPartLen)
	if err != nil {
		return "", fmt.Errorf("generate local part: %w", err)
	}
	baseDomain := emailDomains[randv2.N(len(emailDomains))]
	mailboxDomain, err := randomMailboxDomain(baseDomain)
	if err != nil {
		return "", fmt.Errorf("generate mailbox domain: %w", err)
	}
	return localPart + "@" + mailboxDomain, nil
}

// Latest returns the content of the newest received email for the given address.
func Latest(address string) (string, error) {
	return LatestWithContext(context.Background(), address)
}

// LatestWithContext returns the content of the newest email and respects cancellation.
func LatestWithContext(ctx context.Context, address string) (string, error) {
	record, err := latestInboxRecordWithContext(ctx, address)
	if err != nil {
		return "", err
	}

	content, err := emailContent(record)
	if err != nil {
		return "", err
	}
	return content, nil
}

func latestEmailCursorWithContext(ctx context.Context, address string) (emailCursor, error) {
	record, err := latestInboxRecordWithContext(ctx, address)
	if err != nil {
		if errors.Is(err, errEmailNotFound) {
			return emailCursor{}, nil
		}
		return emailCursor{}, err
	}
	return emailCursorFromRecord(record), nil
}

func latestAfterWithContext(ctx context.Context, address string, after emailCursor) (string, bool, error) {
	record, err := latestInboxRecordWithContext(ctx, address)
	if err != nil {
		if errors.Is(err, errEmailNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	if !isRecordAfterCursor(record, after) {
		return "", false, nil
	}

	content, err := emailContent(record)
	if err != nil {
		return "", true, err
	}
	return content, true, nil
}

func latestInboxRecordWithContext(ctx context.Context, address string) (emailListRecord, error) {
	if err := ctx.Err(); err != nil {
		return emailListRecord{}, fmt.Errorf("fetching emails canceled: %w", err)
	}

	address = strings.TrimSpace(address)
	if address == "" {
		return emailListRecord{}, errors.New("address is empty")
	}

	records, err := fetchEmailList(ctx, address)
	if err != nil {
		return emailListRecord{}, fmt.Errorf("fetching emails: %w", err)
	}

	records = filterInboxRecords(records, address)
	if len(records) == 0 {
		return emailListRecord{}, errEmailNotFound
	}
	records = preferOpenAIRecords(records)
	return pickLatestRecord(records), nil
}

func emailContent(record emailListRecord) (string, error) {
	content := strings.TrimSpace(record.Text)
	if content == "" {
		content = strings.TrimSpace(record.Subject)
	}
	if content == "" {
		content = strings.TrimSpace(record.Content)
	}
	if content == "" {
		return "", errors.New("latest email content is empty")
	}
	return content, nil
}

func emailCursorFromRecord(record emailListRecord) emailCursor {
	createdAt, ok := parseEmailCreateTime(record.CreateTime)
	return emailCursor{
		EmailID:      record.EmailID,
		CreatedAt:    createdAt,
		HasCreatedAt: ok,
	}
}

func isRecordAfterCursor(record emailListRecord, cursor emailCursor) bool {
	if cursor.EmailID == 0 && !cursor.HasCreatedAt {
		return true
	}

	recordTime, recordHasTime := parseEmailCreateTime(record.CreateTime)
	if cursor.HasCreatedAt && recordHasTime && !recordTime.Equal(cursor.CreatedAt) {
		return recordTime.After(cursor.CreatedAt)
	}
	if record.EmailID != cursor.EmailID {
		return record.EmailID > cursor.EmailID
	}
	if cursor.HasCreatedAt && recordHasTime {
		return recordTime.After(cursor.CreatedAt)
	}
	return false
}

func fetchEmailList(ctx context.Context, address string) ([]emailListRecord, error) {
	reqURL, err := url.Parse(emailAPIURL)
	if err != nil {
		return nil, fmt.Errorf("parse api url: %w", err)
	}
	reqURL = reqURL.JoinPath("api.php")
	query := reqURL.Query()
	query.Set("to", address)
	reqURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	res, err := emailHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		body, err := io.ReadAll(res.Body)
		if err != nil {
			return nil, fmt.Errorf("read response body: %w", err)
		}
		return nil, fmt.Errorf("email list status %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload []emailListRecord
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode response JSON: %w", err)
	}
	return payload, nil
}

func randomLocalPart(length int) (string, error) {
	if length <= 0 {
		return "", errors.New("length must be positive")
	}
	return randomString(length, "abcdefghijklmnopqrstuvwxyz0123456789")
}

func randomMailboxDomain(baseDomain string) (string, error) {
	baseDomain = strings.TrimSpace(baseDomain)
	if baseDomain == "" {
		return "", errors.New("base domain is empty")
	}

	prefix, err := randomString(defaultEmailSubdomainRandLen, "abcdefghijklmnopqrstuvwxyz0123456789")
	if err != nil {
		return "", fmt.Errorf("generate mailbox subdomain prefix: %w", err)
	}
	return prefix + "mail." + baseDomain, nil
}

func filterInboxRecords(records []emailListRecord, toEmail string) []emailListRecord {
	toEmail = strings.TrimSpace(toEmail)
	filtered := make([]emailListRecord, 0, len(records))
	for _, rec := range records {
		if toEmail != "" && !strings.EqualFold(strings.TrimSpace(rec.ToEmail), toEmail) {
			continue
		}
		filtered = append(filtered, rec)
	}

	if len(filtered) > 0 {
		return filtered
	}
	return records
}

func preferOpenAIRecords(records []emailListRecord) []emailListRecord {
	openAIRecords := make([]emailListRecord, 0, len(records))
	for _, rec := range records {
		if isOpenAIRecord(rec) {
			openAIRecords = append(openAIRecords, rec)
		}
	}
	if len(openAIRecords) > 0 {
		return openAIRecords
	}
	return records
}

func isOpenAIRecord(rec emailListRecord) bool {
	combined := strings.ToLower(strings.Join([]string{
		rec.SendEmail, rec.SendName, rec.Subject, rec.Text, rec.Content,
	}, "\n"))
	return strings.Contains(combined, "openai")
}

func pickLatestRecord(records []emailListRecord) emailListRecord {
	latest := records[0]
	for _, rec := range records[1:] {
		if isRecordNewer(rec, latest) {
			latest = rec
		}
	}
	return latest
}

func isRecordNewer(left, right emailListRecord) bool {
	leftTime, leftOK := parseEmailCreateTime(left.CreateTime)
	rightTime, rightOK := parseEmailCreateTime(right.CreateTime)

	if leftOK && rightOK && !leftTime.Equal(rightTime) {
		return leftTime.After(rightTime)
	}
	if leftOK != rightOK {
		return leftOK
	}

	return left.EmailID > right.EmailID
}

func parseEmailCreateTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}

	layouts := []string{
		"2006-01-02 15:04:05",
		time.RFC3339,
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		t, err := time.ParseInLocation(layout, raw, time.UTC)
		if err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
