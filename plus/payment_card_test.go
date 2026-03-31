package plus

import (
	"slices"
	"strings"
	"testing"
)

func TestLuhnCheckDigit(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		want   byte
	}{
		{name: "visa example", prefix: "7992739871", want: '3'},
		{name: "all zeros", prefix: "000000000000000", want: '0'},
		{name: "custom bin prefix", prefix: "625817123456789", want: '4'},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := luhnCheckDigit(tt.prefix); got != tt.want {
				t.Fatalf("luhnCheckDigit(%q) = %q, want %q", tt.prefix, got, tt.want)
			}
		})
	}
}

func TestRandomCard(t *testing.T) {
	tests := []struct {
		name    string
		cfg     PaymentCardConfig
		wantErr string
	}{
		{
			name:    "reject empty bins",
			cfg:     PaymentCardConfig{},
			wantErr: "payment card bins are empty",
		},
		{
			name: "reject empty entry",
			cfg: PaymentCardConfig{
				BINs: []string{""},
			},
			wantErr: "payment card entry is empty",
		},
		{
			name: "reject non-digit entry",
			cfg: PaymentCardConfig{
				BINs: []string{"6258ab"},
			},
			wantErr: `payment card entry "6258ab" must contain only digits`,
		},
		{
			name: "reject too long entry",
			cfg: PaymentCardConfig{
				BINs: []string{"12345678901234567"},
			},
			wantErr: `payment card entry "12345678901234567" is too long`,
		},
		{
			name: "random card expands bin prefix",
			cfg: PaymentCardConfig{
				BINs: []string{"625817"},
			},
		},
		{
			name: "random card keeps provided full number",
			cfg:  PaymentCardConfig{BINs: []string{"6258171234567894"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			card, err := randomCard(tt.cfg)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("randomCard() error = nil, want %q", tt.wantErr)
				}
				if err.Error() != tt.wantErr {
					t.Fatalf("randomCard() error = %q, want %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("randomCard() error = %v", err)
			}

			if len(card.Number) != 16 {
				t.Fatalf("len(card.Number) = %d, want 16", len(card.Number))
			}
			if !slices.Contains(tt.cfg.BINs, card.Number) && !slices.ContainsFunc(tt.cfg.BINs, func(bin string) bool {
				return len(bin) < paymentCardNumberLength && strings.HasPrefix(card.Number, bin)
			}) {
				t.Fatalf("card.Number = %q, want exact match or prefix from %v", card.Number, tt.cfg.BINs)
			}
			if got, want := len(card.CVC), 3; got != want {
				t.Fatalf("len(card.CVC) = %d, want %d", got, want)
			}
			if got, want := len(card.ExpMonth), 2; got != want {
				t.Fatalf("len(card.ExpMonth) = %d, want %d", got, want)
			}
			if got, want := len(card.ExpYear), 2; got != want {
				t.Fatalf("len(card.ExpYear) = %d, want %d", got, want)
			}
			checkDigitIndex := len(card.Number) - 1
			if check := luhnCheckDigit(card.Number[:checkDigitIndex]); card.Number[checkDigitIndex] != check {
				t.Fatalf("card.Number = %q, check digit = %q, want %q", card.Number, card.Number[checkDigitIndex], check)
			}
		})
	}
}
