package main

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"
	"testing"
)

func TestNormalizeTunnelError(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantNil bool
	}{
		{
			name:    "nil error",
			err:     nil,
			wantNil: true,
		},
		{
			name:    "io eof",
			err:     io.EOF,
			wantNil: true,
		},
		{
			name:    "net closed",
			err:     net.ErrClosed,
			wantNil: true,
		},
		{
			name:    "context canceled",
			err:     context.Canceled,
			wantNil: true,
		},
		{
			name:    "wrapped broken pipe",
			err:     errors.Join(errors.New("copy failed"), syscall.EPIPE),
			wantNil: true,
		},
		{
			name:    "wrapped connection reset",
			err:     errors.Join(errors.New("copy failed"), syscall.ECONNRESET),
			wantNil: true,
		},
		{
			name:    "unexpected error",
			err:     errors.New("boom"),
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeTunnelError(tt.err)
			if tt.wantNil && got != nil {
				t.Fatalf("normalizeTunnelError() = %v, want nil", got)
			}
			if !tt.wantNil && got == nil {
				t.Fatalf("normalizeTunnelError() = nil, want non-nil")
			}
		})
	}
}
