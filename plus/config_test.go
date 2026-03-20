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
			},
			want: RegisterOptions{
				DataDir:               "/tmp/data",
				OTPWait:               10,
				OTPPoll:               5,
				RegistrationProxyPool: RegistrationProxyPool{"http://proxy-a", "http://proxy-b"},
			},
		},
		{
			name: "trim proxy pool entries",
			opts: RegisterOptions{
				RegistrationProxyPool: RegistrationProxyPool{
					" http://user-%s:pass@proxy.example.com:7777 ",
					" ",
				},
			},
			want: RegisterOptions{
				DataDir:               defaultDataDir,
				OTPWait:               defaultOTPWait,
				OTPPoll:               defaultOTPPoll,
				RegistrationProxyPool: RegistrationProxyPool{"http://user-%s:pass@proxy.example.com:7777"},
			},
		},
		{
			name: "reject empty pool",
			opts: RegisterOptions{
				RegistrationProxyPool: RegistrationProxyPool{" ", "\t"},
			},
			wantErr: "proxy pool is empty",
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
