package plus

import (
	"regexp"
	"strings"
	"testing"
)

func TestRegistrationProxyPoolRandom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		pool       RegistrationProxyPool
		wantErr    string
		wantRegexp string
	}{
		{
			name:    "empty pool",
			pool:    nil,
			wantErr: "proxy pool is empty",
		},
		{
			name:    "blank entries only",
			pool:    RegistrationProxyPool{" ", "\t"},
			wantErr: "proxy pool is empty",
		},
		{
			name:       "trim and replace session placeholder",
			pool:       RegistrationProxyPool{"  http://user-%s:pass@proxy.example.com:7777  "},
			wantRegexp: `^http://user-[a-z0-9]{12}:pass@proxy\.example\.com:7777$`,
		},
		{
			name:    "invalid proxy url",
			pool:    RegistrationProxyPool{"://bad"},
			wantErr: "parse proxy",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.pool.Random()
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Random() error = nil, want contains %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Random() error = %v, want contains %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Random() error = %v", err)
			}
			if matched := regexp.MustCompile(tt.wantRegexp).MatchString(got); !matched {
				t.Fatalf("Random() = %q, want match %q", got, tt.wantRegexp)
			}
		})
	}
}

func TestRegistrationProxyPoolRandomGeneratesFreshSessionID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		pool RegistrationProxyPool
	}{
		{
			name: "same template returns different urls",
			pool: RegistrationProxyPool{"http://user-%s:pass@proxy.example.com:7777"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			first, err := tt.pool.Random()
			if err != nil {
				t.Fatalf("first Random() error = %v", err)
			}
			second, err := tt.pool.Random()
			if err != nil {
				t.Fatalf("second Random() error = %v", err)
			}
			if first == second {
				t.Fatalf("Random() should return a fresh proxy url, got %q twice", first)
			}
		})
	}
}
