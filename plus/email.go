package plus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	randv2 "math/rand/v2"
	"net/http"
	"strings"
	"time"
)

const (
	emailAPIURL              = "https://mail.wxjerry.top"
	emailAuthToken           = ""
	defaultEmailLocalPartLen = 12
)

var (
	errEmailNotFound = errors.New("no email found for address")
	emailHTTPClient  = &http.Client{Timeout: 15 * time.Second}
	emailDomains     = [...]string{}
)

type emailListResponse struct {
	Code    int               `json:"code"`
	Message string            `json:"message"`
	Data    []emailListRecord `json:"data"`
}

type emailListRecord struct {
	EmailID    int    `json:"emailId"`
	SendEmail  string `json:"sendEmail"`
	SendName   string `json:"sendName"`
	Subject    string `json:"subject"`
	ToEmail    string `json:"toEmail"`
	ToName     string `json:"toName"`
	CreateTime string `json:"createTime"`
	Type       int    `json:"type"`
	Content    string `json:"content"`
	Text       string `json:"text"`
	IsDel      int    `json:"isDel"`
}

type emailCursor struct {
	EmailID      int
	CreatedAt    time.Time
	HasCreatedAt bool
}

// Generate creates a random email address in the form <random>@example.invalid.
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
	domain := emailDomains[randv2.N(len(emailDomains))]
	return localPart + "@" + domain, nil
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

	resp, err := fetchEmailList(ctx, address)
	if err != nil {
		return emailListRecord{}, fmt.Errorf("fetching emails: %w", err)
	}

	if resp.Code != http.StatusOK {
		msg := strings.TrimSpace(resp.Message)
		if msg == "" {
			return emailListRecord{}, fmt.Errorf("fetching emails: code %d", resp.Code)
		}
		return emailListRecord{}, fmt.Errorf("fetching emails: code %d: %s", resp.Code, msg)
	}

	records := filterInboxRecords(resp.Data, address)
	if len(records) == 0 {
		return emailListRecord{}, errEmailNotFound
	}
	records = preferOpenAIRecords(records)
	return pickLatestRecord(records), nil
}

func emailContent(record emailListRecord) (string, error) {
	content := strings.TrimSpace(record.Text)
	if content == "" {
		content = strings.TrimSpace(record.Content)
	}
	if content == "" {
		content = strings.TrimSpace(record.Subject)
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

func fetchEmailList(ctx context.Context, address string) (emailListResponse, error) {
	body, err := json.Marshal(map[string]string{"toEmail": address})
	if err != nil {
		return emailListResponse{}, fmt.Errorf("encode request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, emailAPIURL+"/api/public/emailList", bytes.NewReader(body))
	if err != nil {
		return emailListResponse{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", emailAuthToken)
	req.Header.Set("Content-Type", "application/json")

	res, err := emailHTTPClient.Do(req)
	if err != nil {
		return emailListResponse{}, fmt.Errorf("send request: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		body, err := io.ReadAll(res.Body)
		if err != nil {
			return emailListResponse{}, fmt.Errorf("read response body: %w", err)
		}
		return emailListResponse{}, fmt.Errorf("email list status %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload emailListResponse
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return emailListResponse{}, fmt.Errorf("decode response JSON: %w", err)
	}
	return payload, nil
}

func randomLocalPart(length int) (string, error) {
	if length <= 0 {
		return "", errors.New("length must be positive")
	}
	return randomString(length, "abcdefghijklmnopqrstuvwxyz0123456789")
}

func filterInboxRecords(records []emailListRecord, toEmail string) []emailListRecord {
	toEmail = strings.TrimSpace(toEmail)
	filtered := make([]emailListRecord, 0, len(records))
	for _, rec := range records {
		if rec.IsDel != 0 {
			continue
		}
		if rec.Type != 0 {
			continue
		}
		if toEmail != "" && !strings.EqualFold(strings.TrimSpace(rec.ToEmail), toEmail) {
			continue
		}
		filtered = append(filtered, rec)
	}

	if len(filtered) > 0 {
		return filtered
	}

	for _, rec := range records {
		if rec.IsDel == 0 {
			filtered = append(filtered, rec)
		}
	}
	return filtered
}

func preferOpenAIRecords(records []emailListRecord) []emailListRecord {
	openaiRecords := make([]emailListRecord, 0, len(records))
	for _, rec := range records {
		if isOpenAIRecord(rec) {
			openaiRecords = append(openaiRecords, rec)
		}
	}
	if len(openaiRecords) > 0 {
		return openaiRecords
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
