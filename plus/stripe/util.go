package stripe

import (
	"context"
	cryptoRand "crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

func buildURLWithQuery(base string, query url.Values) string {
	if encoded := query.Encode(); encoded != "" {
		return base + "?" + encoded
	}
	return base
}

func cloneValues(src url.Values) url.Values {
	dst := make(url.Values, len(src))
	for key, values := range src {
		dst[key] = append([]string(nil), values...)
	}
	return dst
}

func setIndexedValues(values url.Values, prefix string, items ...string) {
	for idx, item := range items {
		values.Set(fmt.Sprintf("%s[%d]", prefix, idx), item)
	}
}

func setIfNotEmpty(values url.Values, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	values.Set(key, value)
}

func waitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func randomString(length int, charset string) (string, error) {
	buf := make([]byte, length)
	if _, err := cryptoRand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, length)
	for i, b := range buf {
		out[i] = charset[int(b)%len(charset)]
	}
	return string(out), nil
}

func randomBase64URL(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := cryptoRand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func randomHexString(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := cryptoRand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
