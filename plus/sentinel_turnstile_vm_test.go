package plus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"regexp"
	"testing"
)

func TestTurnstileSolverFixtures(t *testing.T) {
	tests := []struct {
		name       string
		wantSHA256 string
		load       func(t *testing.T) (string, string)
	}{
		{
			name:       "legacy fixture from reversed sample",
			wantSHA256: "a6d747017e2235157e506823171a1fbff15aa38228ac1c476e714c5862af6bf6",
			load:       loadLegacyTurnstileFixture,
		},
		{
			name:       "current fixture from captured sentinel input",
			wantSHA256: "0fe83ba26f84ea08a6155d7da5fec387e05c34c79dd6e5f264a7b92b3b9ae679",
			load:       loadCurrentTurnstileFixture,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dx, proof := tt.load(t)

			first, err := sentinelSolveTurnstileToken(context.Background(), dx, proof)
			if err != nil {
				t.Fatalf("sentinelSolveTurnstileToken() error = %v", err)
			}
			second, err := sentinelSolveTurnstileToken(context.Background(), dx, proof)
			if err != nil {
				t.Fatalf("sentinelSolveTurnstileToken() second error = %v", err)
			}
			if first != second {
				t.Fatalf("sentinelSolveTurnstileToken() is not deterministic")
			}
			if err := sentinelValidateTurnstileToken(first); err != nil {
				t.Fatalf("sentinelValidateTurnstileToken() error = %v", err)
			}

			sum := sha256.Sum256([]byte(first))
			gotSHA256 := hex.EncodeToString(sum[:])
			if gotSHA256 != tt.wantSHA256 {
				t.Fatalf("token sha256 = %s, want %s", gotSHA256, tt.wantSHA256)
			}
		})
	}
}

func loadLegacyTurnstileFixture(t *testing.T) (string, string) {
	t.Helper()
	body, err := os.ReadFile("turnstile-reversed/generate_turnstile.py")
	if err != nil {
		t.Fatalf("read legacy fixture: %v", err)
	}
	keyMatch := regexp.MustCompile(`(?m)^key = "([^"]+)"`).FindSubmatch(body)
	dxMatch := regexp.MustCompile(`(?m)^t = "([^"]+)"`).FindSubmatch(body)
	if len(keyMatch) != 2 || len(dxMatch) != 2 {
		t.Fatalf("parse legacy fixture: key=%d dx=%d", len(keyMatch), len(dxMatch))
	}
	return string(dxMatch[1]), string(keyMatch[1])
}

func loadCurrentTurnstileFixture(t *testing.T) (string, string) {
	t.Helper()
	body, err := os.ReadFile("/tmp/sentinel_input.json")
	if err != nil {
		t.Skipf("current fixture not available: %v", err)
	}
	var payload struct {
		DX    string `json:"dx"`
		Proof string `json:"proof"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("parse current fixture: %v", err)
	}
	if payload.DX == "" || payload.Proof == "" {
		t.Fatalf("current fixture is incomplete")
	}
	return payload.DX, payload.Proof
}
