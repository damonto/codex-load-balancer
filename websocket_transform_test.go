package main

import (
	"bufio"
	"bytes"
	"testing"
)

func TestParseWebsocketFrameHeader(t *testing.T) {
	tests := []struct {
		name       string
		payloadLen int
		masked     bool
	}{
		{name: "short unmasked payload", payloadLen: 12},
		{name: "length16 masked payload", payloadLen: 126, masked: true},
		{name: "length64 masked payload", payloadLen: 66000, masked: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := buildProxyWebsocketFrame(proxyWebsocketFrame{
				opcode:  websocketOpcodeText,
				fin:     true,
				payload: bytes.Repeat([]byte("x"), tt.payloadLen),
			}, tt.masked)
			if err != nil {
				t.Fatalf("buildProxyWebsocketFrame() error = %v", err)
			}

			headerLen := websocketFrameHeaderLen(encoded[:2])
			header, err := parseWebsocketFrameHeader(encoded[:headerLen])
			if err != nil {
				t.Fatalf("parseWebsocketFrameHeader() error = %v", err)
			}
			if int(header.payloadLen) != tt.payloadLen {
				t.Fatalf("payloadLen = %d, want %d", header.payloadLen, tt.payloadLen)
			}
			if header.masked != tt.masked {
				t.Fatalf("masked = %v, want %v", header.masked, tt.masked)
			}
			if header.first&0x0F != websocketOpcodeText {
				t.Fatalf("opcode = %d, want text", header.first&0x0F)
			}
		})
	}
}

func TestCopyWebsocketClientToUpstreamInjectsResponseTools(t *testing.T) {
	tests := []struct {
		name   string
		frames []proxyWebsocketFrame
	}{
		{
			name: "single text frame",
			frames: []proxyWebsocketFrame{
				{
					opcode:  websocketOpcodeText,
					fin:     true,
					payload: []byte(`{"type":"response.create","model":"gpt-5.4","tools":[]}`),
				},
			},
		},
		{
			name: "fragmented text frame",
			frames: []proxyWebsocketFrame{
				{
					opcode:  websocketOpcodeText,
					fin:     false,
					payload: []byte(`{"type":"response.create","model":"gpt-5.4",`),
				},
				{
					opcode:  websocketOpcodeContinuation,
					fin:     true,
					payload: []byte(`"tools":[]}`),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var input bytes.Buffer
			for _, frame := range tt.frames {
				encoded, err := buildProxyWebsocketFrame(frame, true)
				if err != nil {
					t.Fatalf("buildProxyWebsocketFrame() error = %v", err)
				}
				if _, err := input.Write(encoded); err != nil {
					t.Fatalf("buffer websocket frame: %v", err)
				}
			}
			closeFrame, err := buildProxyWebsocketFrame(proxyWebsocketFrame{
				opcode: websocketOpcodeClose,
				fin:    true,
			}, true)
			if err != nil {
				t.Fatalf("build close frame: %v", err)
			}
			if _, err := input.Write(closeFrame); err != nil {
				t.Fatalf("buffer close frame: %v", err)
			}

			var output bytes.Buffer
			written, err := copyWebsocketClientToUpstream(&output, bufio.NewReader(&input), true, responseToolInjectionContext{})
			if err != nil {
				t.Fatalf("copyWebsocketClientToUpstream() error = %v", err)
			}
			if written <= 0 {
				t.Fatalf("written = %d, want positive", written)
			}

			reader := bufio.NewReader(&output)
			frame, err := readProxyWebsocketFrame(reader)
			if err != nil {
				t.Fatalf("read transformed text frame: %v", err)
			}
			if frame.opcode != websocketOpcodeText || !frame.fin {
				t.Fatalf("frame opcode=%d fin=%v, want final text frame", frame.opcode, frame.fin)
			}
			if !hasToolType(frame.payload, imageGenerationToolType) {
				t.Fatalf("transformed payload missing image_generation: %s", string(frame.payload))
			}

			frame, err = readProxyWebsocketFrame(reader)
			if err != nil {
				t.Fatalf("read transformed close frame: %v", err)
			}
			if frame.opcode != websocketOpcodeClose {
				t.Fatalf("close opcode = %d, want %d", frame.opcode, websocketOpcodeClose)
			}
		})
	}
}

func TestCopyWebsocketClientToUpstreamSkipsNonCreateEvent(t *testing.T) {
	payload := []byte(`{"type":"response.cancel","response_id":"resp_1"}`)
	inputFrame, err := buildProxyWebsocketFrame(proxyWebsocketFrame{
		opcode:  websocketOpcodeText,
		fin:     true,
		payload: payload,
	}, true)
	if err != nil {
		t.Fatalf("build input frame: %v", err)
	}
	closeFrame, err := buildProxyWebsocketFrame(proxyWebsocketFrame{
		opcode: websocketOpcodeClose,
		fin:    true,
	}, true)
	if err != nil {
		t.Fatalf("build close frame: %v", err)
	}

	input := bytes.NewBuffer(append(inputFrame, closeFrame...))
	var output bytes.Buffer
	if _, err := copyWebsocketClientToUpstream(&output, bufio.NewReader(input), true, responseToolInjectionContext{}); err != nil {
		t.Fatalf("copyWebsocketClientToUpstream() error = %v", err)
	}

	frame, err := readProxyWebsocketFrame(bufio.NewReader(&output))
	if err != nil {
		t.Fatalf("read transformed frame: %v", err)
	}
	if string(frame.payload) != string(payload) {
		t.Fatalf("payload = %s, want %s", string(frame.payload), string(payload))
	}
}

func TestCopyWebsocketClientToUpstreamSkipsImageGenerationForFreePlan(t *testing.T) {
	payload := []byte(`{"type":"response.create","model":"gpt-5.4","tools":[]}`)
	inputFrame, err := buildProxyWebsocketFrame(proxyWebsocketFrame{
		opcode:  websocketOpcodeText,
		fin:     true,
		payload: payload,
	}, true)
	if err != nil {
		t.Fatalf("build input frame: %v", err)
	}
	closeFrame, err := buildProxyWebsocketFrame(proxyWebsocketFrame{
		opcode: websocketOpcodeClose,
		fin:    true,
	}, true)
	if err != nil {
		t.Fatalf("build close frame: %v", err)
	}

	input := bytes.NewBuffer(append(inputFrame, closeFrame...))
	var output bytes.Buffer
	if _, err := copyWebsocketClientToUpstream(&output, bufio.NewReader(input), true, responseToolInjectionContext{planType: "free"}); err != nil {
		t.Fatalf("copyWebsocketClientToUpstream() error = %v", err)
	}

	frame, err := readProxyWebsocketFrame(bufio.NewReader(&output))
	if err != nil {
		t.Fatalf("read transformed frame: %v", err)
	}
	if hasToolType(frame.payload, imageGenerationToolType) {
		t.Fatalf("payload unexpectedly has image_generation: %s", string(frame.payload))
	}
}
