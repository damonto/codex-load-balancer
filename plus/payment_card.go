package plus

import (
	"errors"
	"fmt"
	randv2 "math/rand/v2"
	"strings"
)

const paymentCardNumberLength = 16

type PaymentCardConfig struct {
	BINs         []string
	TopUpEnabled bool
}

type PaymentCard struct {
	Number   string
	CVC      string
	ExpMonth string
	ExpYear  string
}

func normalizePaymentCardConfig(cfg PaymentCardConfig) (PaymentCardConfig, error) {
	cleanBINs := make([]string, 0, len(cfg.BINs))
	for _, bin := range cfg.BINs {
		bin = strings.TrimSpace(bin)
		if bin == "" {
			continue
		}
		if strings.IndexFunc(bin, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			return PaymentCardConfig{}, fmt.Errorf("payment card bin %q must contain only digits", bin)
		}
		if cfg.TopUpEnabled {
			if len(bin) >= paymentCardNumberLength {
				return PaymentCardConfig{}, fmt.Errorf("payment card bin %q is too long", bin)
			}
		} else if len(bin) != paymentCardNumberLength {
			return PaymentCardConfig{}, fmt.Errorf("payment card number %q must be %d digits when topup is disabled", bin, paymentCardNumberLength)
		}
		cleanBINs = append(cleanBINs, bin)
	}
	if len(cleanBINs) == 0 {
		return PaymentCardConfig{}, errors.New("payment card bins are empty")
	}
	cfg.BINs = cleanBINs
	return cfg, nil
}

func randomCard(cfg PaymentCardConfig) PaymentCard {
	number := cfg.BINs[randv2.N(len(cfg.BINs))]
	if cfg.TopUpEnabled {
		number = generatePaymentCardNumber(number)
	}
	return PaymentCard{
		Number:   number,
		CVC:      fmt.Sprintf("%03d", randv2.N(1000)),
		ExpMonth: fmt.Sprintf("%02d", 1+randv2.N(12)),
		ExpYear:  fmt.Sprintf("%02d", 30+randv2.N(10)),
	}
}

func generatePaymentCardNumber(bin string) string {
	paddingDigits := paymentCardNumberLength - 1 - len(bin)
	var digits strings.Builder
	digits.Grow(len(bin) + paddingDigits + 1)
	digits.WriteString(bin)
	for range paddingDigits {
		digits.WriteByte(byte('0' + randv2.N(10)))
	}

	prefix := digits.String()
	return prefix + string(luhnCheckDigit(prefix))
}

func luhnCheckDigit(prefix string) byte {
	sum := 0
	double := true
	for i := len(prefix) - 1; i >= 0; i-- {
		digit := int(prefix[i] - '0')
		if double {
			digit *= 2
			if digit > 9 {
				digit -= 9
			}
		}
		sum += digit
		double = !double
	}
	return byte('0' + (10-sum%10)%10)
}
