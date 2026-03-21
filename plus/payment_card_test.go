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

func TestRandomCard(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "random card is valid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			card := randomCard()

			if len(card.Number) != 16 {
				t.Fatalf("len(card.Number) = %d, want 16", len(card.Number))
			}
			if !slices.Contains(paymentCardBINs[:], card.Number[:6]) {
				t.Fatalf("card.Number prefix = %q, want one of %v", card.Number[:6], paymentCardBINs)
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
			if check := luhnCheckDigit(card.Number[:15]); card.Number[15] != check {
				t.Fatalf("card.Number = %q, check digit = %q, want %q", card.Number, card.Number[15], check)
			}
		})
	}
}
