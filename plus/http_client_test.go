package plus

import (
	"strings"
	"testing"
)

func TestNewClientPicksFreshProxy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     RegisterOptions
		wantErr string
	}{
		{
			name: "refresh uses same pool but new session id",
			cfg: RegisterOptions{
				DataDir:               defaultDataDir,
				OTPWait:               defaultOTPWait,
				OTPPoll:               defaultOTPPoll,
				RegistrationProxyPool: RegistrationProxyPool{"http://user-%s:pass@proxy.example.com:7777"},
			},
		},
		{
			name: "empty proxy pool",
			cfg: RegisterOptions{
				RegistrationProxyPool: RegistrationProxyPool{" "},
			},
			wantErr: "pick registration proxy: proxy pool is empty",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client, err := newClient(tt.cfg)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("newClient() error = nil, want contains %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("newClient() error = %v, want contains %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("newClient() error = %v", err)
			}

			other, err := newClient(tt.cfg)
			if err != nil {
				t.Fatalf("newClient() second error = %v", err)
			}
			if client.Proxy() == other.Proxy() {
				t.Fatalf("newClient() should pick a fresh proxy, got %q twice", client.Proxy())
			}
		})
	}
}
