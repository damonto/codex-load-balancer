package main

import (
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"strings"
)

func cloneHeaders(in http.Header) http.Header {
	out := make(http.Header, len(in))
	for key, values := range in {
		copied := make([]string, len(values))
		copy(copied, values)
		out[key] = copied
	}
	return out
}

func cloneForwardHeaders(in http.Header) http.Header {
	skip := hopByHopHeaderNames(in)
	out := make(http.Header, len(in))
	for key, values := range in {
		if _, ok := skip[key]; ok {
			continue
		}
		copied := make([]string, len(values))
		copy(copied, values)
		out[key] = copied
	}
	return out
}

func writeResponse(w http.ResponseWriter, resp *http.Response, body []byte) {
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

func streamResponseWithObserver(w http.ResponseWriter, resp *http.Response, observer io.Writer) (int64, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return 0, fmt.Errorf("stream response: response writer does not support flushing")
	}

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	flusher.Flush()

	defer resp.Body.Close()
	writer := io.Writer(flushWriter{w: w, f: flusher})
	if observer != nil {
		writer = io.MultiWriter(writer, observer)
	}
	written, err := io.Copy(writer, resp.Body)
	if err != nil {
		return written, fmt.Errorf("stream response body: %w", err)
	}
	return written, nil
}

type flushWriter struct {
	w http.ResponseWriter
	f http.Flusher
}

func (fw flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if err != nil {
		return n, err
	}
	fw.f.Flush()
	return n, nil
}

func copyHeaders(dst http.Header, src http.Header) {
	skip := hopByHopHeaderNames(src)
	for key, values := range src {
		if _, ok := skip[key]; ok {
			continue
		}
		copied := make([]string, len(values))
		copy(copied, values)
		dst[key] = copied
	}
}

func hopByHopHeaderNames(headers http.Header) map[string]struct{} {
	names := make(map[string]struct{}, len(hopByHopHeaders)+4)
	for key := range hopByHopHeaders {
		names[key] = struct{}{}
	}
	for _, value := range headers.Values("Connection") {
		for part := range strings.SplitSeq(value, ",") {
			name := textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(part))
			if name == "" {
				continue
			}
			names[name] = struct{}{}
		}
	}
	return names
}

// hopByHopHeaders lists headers that must not be forwarded by a proxy (RFC 7230 §6.1).
// These are connection-scoped and meaningful only for a single transport hop.
var hopByHopHeaders = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Proxy-Connection":    {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}
