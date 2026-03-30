package plus

import (
	"slices"
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

func TestNormalizePaymentCardConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     PaymentCardConfig
		want    PaymentCardConfig
		wantErr string
	}{
		{
			name: "trim bins",
			cfg: PaymentCardConfig{
				BINs:         []string{" 625817 ", "", "624441"},
				TopUpEnabled: true,
			},
			want: PaymentCardConfig{
				BINs:         []string{"625817", "624441"},
				TopUpEnabled: true,
			},
		},
		{
			name: "reject empty bins",
			cfg: PaymentCardConfig{
				BINs:         []string{" ", "\t"},
				TopUpEnabled: true,
			},
			wantErr: "payment card bins are empty",
		},
		{
			name: "reject non-digit bin",
			cfg: PaymentCardConfig{
				BINs:         []string{"6258ab"},
				TopUpEnabled: true,
			},
			wantErr: `payment card bin "6258ab" must contain only digits`,
		},
		{
			name: "reject too long bin",
			cfg: PaymentCardConfig{
				BINs:         []string{"1234567890123456"},
				TopUpEnabled: true,
			},
			wantErr: `payment card bin "1234567890123456" is too long`,
		},
		{
			name: "reject short full number when topup disabled",
			cfg: PaymentCardConfig{
				BINs: []string{"625817"},
			},
			wantErr: `payment card number "625817" must be 16 digits when topup is disabled`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizePaymentCardConfig(tt.cfg)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("normalizePaymentCardConfig() error = nil, want %q", tt.wantErr)
				}
				if err.Error() != tt.wantErr {
					t.Fatalf("normalizePaymentCardConfig() error = %q, want %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizePaymentCardConfig() error = %v", err)
			}
			if !slices.Equal(got.BINs, tt.want.BINs) {
				t.Fatalf("BINs = %v, want %v", got.BINs, tt.want.BINs)
			}
		})
	}
}

func TestRandomCard(t *testing.T) {
	tests := []struct {
		name string
		cfg  PaymentCardConfig
	}{
		{name: "random card is valid", cfg: PaymentCardConfig{BINs: []string{"625817", "624441"}, TopUpEnabled: true}},
		{name: "random card uses provided full number when topup disabled", cfg: PaymentCardConfig{BINs: []string{"6258171234567894"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			card := randomCard(tt.cfg)

			if len(card.Number) != 16 {
				t.Fatalf("len(card.Number) = %d, want 16", len(card.Number))
			}
			if tt.cfg.TopUpEnabled {
				if !slices.Contains(tt.cfg.BINs, card.Number[:6]) {
					t.Fatalf("card.Number prefix = %q, want one of %v", card.Number[:6], tt.cfg.BINs)
				}
			} else if !slices.Contains(tt.cfg.BINs, card.Number) {
				t.Fatalf("card.Number = %q, want one of %v", card.Number, tt.cfg.BINs)
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
