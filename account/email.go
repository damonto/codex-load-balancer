package account

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
	emailAPIBaseURL          = "https://mail.wxjerry.top"
	emailAuthToken           = ""
	defaultEmailLocalPartLen = 12
)

var (
	emailHTTPClient = &http.Client{Timeout: 15 * time.Second}
	emailDomains    = [...]string{}
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
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("fetching emails canceled: %w", err)
	}

	address = strings.TrimSpace(address)
	if address == "" {
		return "", errors.New("address is empty")
	}

	resp, err := fetchEmailList(ctx, address)
	if err != nil {
		return "", fmt.Errorf("fetching emails: %w", err)
	}

	if resp.Code != http.StatusOK {
		msg := strings.TrimSpace(resp.Message)
		if msg == "" {
			return "", fmt.Errorf("fetching emails: code %d", resp.Code)
		}
		return "", fmt.Errorf("fetching emails: code %d: %s", resp.Code, msg)
	}

	records := filterInboxRecords(resp.Data, address)
	if len(records) == 0 {
		return "", errors.New("no email found for address")
	}
	records = preferOpenAIRecords(records)

	record := pickLatestRecord(records)
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

func fetchEmailList(ctx context.Context, address string) (emailListResponse, error) {
	body, err := json.Marshal(map[string]string{"toEmail": address})
	if err != nil {
		return emailListResponse{}, fmt.Errorf("encode request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, emailAPIBaseURL+"/api/public/emailList", bytes.NewReader(body))
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

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return emailListResponse{}, fmt.Errorf("read response body: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return emailListResponse{}, fmt.Errorf("http status %d: %s", res.StatusCode, strings.TrimSpace(string(data)))
	}

	var payload emailListResponse
	if err := json.Unmarshal(data, &payload); err != nil {
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

	// Fallback: if upstream data has unexpected type/toEmail fields, keep non-deleted items.
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
