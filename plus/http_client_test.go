package plus

import (
	"strings"
	"testing"
	"time"
)

func TestClientRefreshPicksFreshProxy(t *testing.T) {
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

			refreshed, err := client.Refresh()
			if err != nil {
				t.Fatalf("Refresh() error = %v", err)
			}

			deadline := time.Now().Add(100 * time.Millisecond)
			for client.Proxy() == refreshed.Proxy() && time.Now().Before(deadline) {
				refreshed, err = client.Refresh()
				if err != nil {
					t.Fatalf("Refresh() retry error = %v", err)
				}
			}
			if client.Proxy() == refreshed.Proxy() {
				t.Fatalf("Refresh() should pick a fresh proxy, got %q twice", client.Proxy())
			}
		})
	}
}
