package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"
)

func tunnelWebSocket(w http.ResponseWriter, r *http.Request, upstream *websocketUpstreamResponse) (int64, int64, error) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		upstream.conn.Close()
		return 0, 0, fmt.Errorf("hijack response writer: unsupported")
	}

	clientConn, clientRW, err := hijacker.Hijack()
	if err != nil {
		upstream.conn.Close()
		return 0, 0, fmt.Errorf("hijack client connection: %w", err)
	}
	defer clientConn.Close()
	defer upstream.conn.Close()

	if err := upstream.resp.Write(clientConn); err != nil {
		return 0, 0, fmt.Errorf("write websocket upgrade response: %w", err)
	}
	if err := clientRW.Writer.Flush(); err != nil {
		return 0, 0, fmt.Errorf("flush hijacked client writer: %w", err)
	}

	type copyResult struct {
		clientToUpstream bool
		n                int64
		err              error
	}
	resultCh := make(chan copyResult, 2)
	go func() {
		n, err := io.Copy(upstream.conn, clientRW.Reader)
		resultCh <- copyResult{clientToUpstream: true, n: n, err: err}
	}()
	go func() {
		n, err := io.Copy(clientConn, upstream.br)
		resultCh <- copyResult{clientToUpstream: false, n: n, err: err}
	}()

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-r.Context().Done():
			_ = clientConn.Close()
			_ = upstream.conn.Close()
		case <-done:
		}
	}()

	first := <-resultCh
	_ = clientConn.Close()
	_ = upstream.conn.Close()
	second := <-resultCh

	var clientToUpstream int64
	var upstreamToClient int64
	if first.clientToUpstream {
		clientToUpstream = first.n
		upstreamToClient = second.n
	} else {
		clientToUpstream = second.n
		upstreamToClient = first.n
	}

	copyErr := normalizeTunnelError(first.err)
	if copyErr == nil {
		copyErr = normalizeTunnelError(second.err)
	}
	return clientToUpstream, upstreamToClient, copyErr
}

func normalizeTunnelError(err error) error {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return nil
	}
	if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
		return nil
	}
	return err
}
