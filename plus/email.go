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
	defaultEmailLocalPartLen = 12
)

var (
	errEmailNotFound = errors.New("no email found for address")
	emailAPIURL      = "https://mail.salty.eu.org"
	emailHTTPClient  = &http.Client{Timeout: 15 * time.Second}
	emailDomains     = [...]string{}
)

type emailMessage struct {
	ID         string `json:"id"`
	Recipient  string `json:"recipient"`
	Sender     string `json:"sender"`
	Nexthop    string `json:"nexthop"`
	Subject    string `json:"subject"`
	Content    string `json:"content"`
	ReceivedAt int64  `json:"received_at"`
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
	domain := emailDomains[randv2.N(len(emailDomains))]
	return localPart + "@" + domain, nil
}

// Latest returns the content of the newest received email for the given address.
func Latest(address string) (string, error) {
	return LatestWithContext(context.Background(), address)
}

// LatestWithContext returns the content of the newest email and respects cancellation.
func LatestWithContext(ctx context.Context, address string) (string, error) {
	record, err := latestInboxMessageWithContext(ctx, address)
	if err != nil {
		return "", err
	}

	content, err := emailContent(record)
	if err != nil {
		return "", err
	}
	return content, nil
}

func latestEmailFingerprintWithContext(ctx context.Context, address string) (string, error) {
	record, err := latestInboxMessageWithContext(ctx, address)
	if err != nil {
		if errors.Is(err, errEmailNotFound) {
			return "", nil
		}
		return "", err
	}
	return emailFingerprint(record), nil
}

func latestChangedWithContext(ctx context.Context, address string, previous string) (string, bool, error) {
	record, err := latestInboxMessageWithContext(ctx, address)
	if err != nil {
		if errors.Is(err, errEmailNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	if previous != "" && emailFingerprint(record) == previous {
		return "", false, nil
	}

	content, err := emailContent(record)
	if err != nil {
		return "", true, err
	}
	return content, true, nil
}

func latestInboxMessageWithContext(ctx context.Context, address string) (emailMessage, error) {
	if err := ctx.Err(); err != nil {
		return emailMessage{}, fmt.Errorf("fetching emails canceled: %w", err)
	}

	address = strings.TrimSpace(address)
	if address == "" {
		return emailMessage{}, errors.New("address is empty")
	}

	resp, err := fetchLatestEmail(ctx, address)
	if err != nil {
		return emailMessage{}, fmt.Errorf("fetching emails: %w", err)
	}
	if recipient := strings.TrimSpace(resp.Recipient); recipient != "" && !strings.EqualFold(recipient, address) {
		return emailMessage{}, fmt.Errorf("latest email recipient %q does not match address %q", recipient, address)
	}
	return resp, nil
}

func emailContent(record emailMessage) (string, error) {
	content := strings.TrimSpace(record.Content)
	if content == "" {
		content = strings.TrimSpace(record.Subject)
	}
	if content == "" {
		return "", errors.New("latest email content is empty")
	}
	return content, nil
}

func emailFingerprint(record emailMessage) string {
	if id := strings.TrimSpace(record.ID); id != "" {
		return "id:" + id
	}
	return fmt.Sprintf(
		"received_at:%d\nsubject:%s\ncontent:%s",
		record.ReceivedAt,
		strings.TrimSpace(record.Subject),
		strings.TrimSpace(record.Content),
	)
}

func fetchLatestEmail(ctx context.Context, address string) (emailMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, emailAPIURL+"/?to="+url.QueryEscape(address), nil)
	if err != nil {
		return emailMessage{}, fmt.Errorf("create request: %w", err)
	}

	res, err := emailHTTPClient.Do(req)
	if err != nil {
		return emailMessage{}, fmt.Errorf("send request: %w", err)
	}
	defer res.Body.Close()

	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusNoContent, http.StatusNotFound:
		return emailMessage{}, errEmailNotFound
	default:
		body, err := io.ReadAll(res.Body)
		if err != nil {
			return emailMessage{}, fmt.Errorf("read response body: %w", err)
		}
		return emailMessage{}, fmt.Errorf("latest email status %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload emailMessage
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		return emailMessage{}, fmt.Errorf("decode response JSON: %w", err)
	}
	if isEmptyEmailMessage(payload) {
		return emailMessage{}, errEmailNotFound
	}
	return payload, nil
}

func randomLocalPart(length int) (string, error) {
	if length <= 0 {
		return "", errors.New("length must be positive")
	}
	return randomString(length, "abcdefghijklmnopqrstuvwxyz0123456789")
}

func isEmptyEmailMessage(record emailMessage) bool {
	return strings.TrimSpace(record.ID) == "" &&
		strings.TrimSpace(record.Recipient) == "" &&
		strings.TrimSpace(record.Sender) == "" &&
		strings.TrimSpace(record.Subject) == "" &&
		strings.TrimSpace(record.Content) == "" &&
		record.ReceivedAt == 0
}
