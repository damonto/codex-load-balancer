package plus

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestTurnstileSolverFixtures(t *testing.T) {
	tests := []struct {
		name       string
		wantSHA256 string
		load       func(t *testing.T) (string, string)
	}{
		{
			name:       "self contained fixture",
			wantSHA256: "63daef30c24df9b4ed06f0af2b23ea32fd8e11b9f0ff2597acfd23f3c335a60b",
			load:       loadSyntheticTurnstileFixture,
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

func loadSyntheticTurnstileFixture(t *testing.T) (string, string) {
	t.Helper()

	proofPayload, err := json.Marshal([]any{})
	if err != nil {
		t.Fatalf("marshal proof payload: %v", err)
	}
	proof := "gAAAAAC" + base64.StdEncoding.EncodeToString(proofPayload)

	token := strings.Repeat("A", 160)
	thirdProgram := []any{
		[]any{2, 1, token},
	}
	secondProgram := make([]any, 0, 11)
	for range 10 {
		secondProgram = append(secondProgram, []any{0})
	}
	secondProgram = append(secondProgram, []any{2, 2, thirdProgram})
	firstProgram := []any{
		[]any{2, 100, secondProgram},
	}

	body, err := json.Marshal(firstProgram)
	if err != nil {
		t.Fatalf("marshal synthetic fixture: %v", err)
	}
	dx := base64.StdEncoding.EncodeToString([]byte(xorString(string(body), proof)))
	return dx, proof
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
