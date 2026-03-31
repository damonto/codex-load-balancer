package plus

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opts    RegisterOptions
		want    RegisterOptions
		wantErr string
	}{
		{
			name: "keep registration proxy pool",
			opts: RegisterOptions{
				DataDir:               " /tmp/data ",
				OTPWait:               10,
				OTPPoll:               5,
				RegistrationProxyPool: RegistrationProxyPool{"http://proxy-a", "http://proxy-b"},
				Purchase:              validPurchaseConfigForTest(),
			},
			want: RegisterOptions{
				DataDir:               "/tmp/data",
				OTPWait:               10,
				OTPPoll:               5,
				RegistrationProxyPool: RegistrationProxyPool{"http://proxy-a", "http://proxy-b"},
				Purchase:              validPurchaseConfigForTest(),
			},
		},
		{
			name: "trim proxy pool entries",
			opts: RegisterOptions{
				RegistrationProxyPool: RegistrationProxyPool{
					" http://user-%s:pass@proxy.example.com:7777 ",
					" ",
				},
				DataDir:  " /tmp/data ",
				Purchase: validPurchaseConfigForTest(),
			},
			want: RegisterOptions{
				DataDir:               "/tmp/data",
				OTPWait:               defaultOTPWait,
				OTPPoll:               defaultOTPPoll,
				RegistrationProxyPool: RegistrationProxyPool{"http://user-%s:pass@proxy.example.com:7777"},
				Purchase:              validPurchaseConfigForTest(),
			},
		},
		{
			name: "reject empty pool",
			opts: RegisterOptions{
				RegistrationProxyPool: RegistrationProxyPool{" ", "\t"},
				DataDir:               "/tmp/data",
				Purchase:              validPurchaseConfigForTest(),
			},
			wantErr: "proxy pool is empty",
		},
		{
			name: "reject empty data dir",
			opts: RegisterOptions{
				RegistrationProxyPool: RegistrationProxyPool{"http://proxy-a"},
				Purchase:              validPurchaseConfigForTest(),
			},
			wantErr: "data dir is empty",
		},
		{
			name: "reject missing purchase config",
			opts: RegisterOptions{
				DataDir:               "/tmp/data",
				RegistrationProxyPool: RegistrationProxyPool{"http://proxy-a"},
			},
			want: RegisterOptions{
				DataDir:               "/tmp/data",
				OTPWait:               defaultOTPWait,
				OTPPoll:               defaultOTPPoll,
				RegistrationProxyPool: RegistrationProxyPool{"http://proxy-a"},
				Purchase:              PurchaseConfig{},
			},
		},
		{
			name: "skip purchase validation when disabled",
			opts: RegisterOptions{
				DataDir:               "/tmp/data",
				RegistrationProxyPool: RegistrationProxyPool{"http://proxy-a"},
				Purchase: PurchaseConfig{
					Enabled: false,
				},
			},
			want: RegisterOptions{
				DataDir:               "/tmp/data",
				OTPWait:               defaultOTPWait,
				OTPPoll:               defaultOTPPoll,
				RegistrationProxyPool: RegistrationProxyPool{"http://proxy-a"},
				Purchase: PurchaseConfig{
					Enabled: false,
				},
			},
		},
		{
			name: "allow incomplete purchase config when enabled",
			opts: RegisterOptions{
				DataDir:               "/tmp/data",
				RegistrationProxyPool: RegistrationProxyPool{"http://proxy-a"},
				Purchase: PurchaseConfig{
					Enabled: true,
				},
			},
			want: RegisterOptions{
				DataDir:               "/tmp/data",
				OTPWait:               defaultOTPWait,
				OTPPoll:               defaultOTPPoll,
				RegistrationProxyPool: RegistrationProxyPool{"http://proxy-a"},
				Purchase: PurchaseConfig{
					Enabled: true,
				},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := normalizeOptions(tt.opts)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("normalizeOptions() error = nil, want contains %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("normalizeOptions() error = %v, want contains %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeOptions() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("normalizeOptions() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func validPurchaseConfigForTest() PurchaseConfig {
	return PurchaseConfig{
		Enabled:         true,
		PlanName:        "chatgptplusplan",
		Currency:        "KRW",
		PromoCampaignID: "plus-1-month-free",
		CheckoutUIMode:  "custom",
		Billing: PurchaseBillingConfig{
			Name:         "Minjun Kim",
			Country:      "KR",
			AddressLine1: "1 Teheran-ro, Gangnam-gu",
			AddressCity:  "Seoul",
			AddressState: "Seoul",
			PostalCode:   "06141",
		},
		PaymentCard: PaymentCardConfig{
			BINs: []string{"625817", "624441"},
		},
	}
}
