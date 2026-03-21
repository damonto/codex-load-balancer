package plus

import (
	"fmt"
	randv2 "math/rand/v2"
	"strings"
)

type PaymentCard struct {
	Number   string
	CVC      string
	ExpMonth string
	ExpYear  string
}

var paymentCardBINs = [...]string{"625817", "625814", "624441"}

func randomCard() PaymentCard {
	return newPaymentCard(paymentCardBINs[randv2.N(len(paymentCardBINs))])
}

func newPaymentCard(bin string) PaymentCard {
	return PaymentCard{
		Number:   generatePaymentCardNumber(bin),
		CVC:      fmt.Sprintf("%03d", randv2.N(1000)),
		ExpMonth: fmt.Sprintf("%02d", 1+randv2.N(12)),
		ExpYear:  fmt.Sprintf("%02d", 30+randv2.N(10)),
	}
}

func generatePaymentCardNumber(bin string) string {
	var digits strings.Builder
	digits.Grow(16)
	digits.WriteString(bin)
	for range 9 {
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
