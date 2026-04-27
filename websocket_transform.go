package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
)

type proxyWebsocketFrame struct {
	opcode  byte
	fin     bool
	rsv     byte
	payload []byte
}

type websocketFrameHeader struct {
	first      byte
	payloadLen uint64
	masked     bool
	mask       [4]byte
}

func copyWebsocketClientToUpstream(dst io.Writer, src *bufio.Reader, injectResponseTools bool, injectionCtx responseToolInjectionContext) (int64, error) {
	if !injectResponseTools {
		return io.Copy(dst, src)
	}

	var written int64
	var textFragments bytes.Buffer
	bufferingText := false

	for {
		frame, err := readProxyWebsocketFrame(src)
		if err != nil {
			return written, fmt.Errorf("read websocket frame: %w", err)
		}

		switch frame.opcode {
		case websocketOpcodeText:
			if frame.rsv != 0 {
				n, err := writeProxyWebsocketFrame(dst, frame, true)
				written += n
				if err != nil {
					return written, err
				}
				continue
			}
			if frame.fin {
				n, err := writeToolInjectedTextFrame(dst, frame.payload, injectionCtx)
				written += n
				if err != nil {
					return written, err
				}
				continue
			}
			bufferingText = true
			textFragments.Reset()
			if err := appendWebsocketTextFragment(&textFragments, frame.payload); err != nil {
				return written, err
			}
		case websocketOpcodeContinuation:
			if !bufferingText {
				n, err := writeProxyWebsocketFrame(dst, frame, true)
				written += n
				if err != nil {
					return written, err
				}
				continue
			}
			if err := appendWebsocketTextFragment(&textFragments, frame.payload); err != nil {
				return written, err
			}
			if !frame.fin {
				continue
			}
			n, err := writeToolInjectedTextFrame(dst, textFragments.Bytes(), injectionCtx)
			written += n
			textFragments.Reset()
			bufferingText = false
			if err != nil {
				return written, err
			}
		default:
			if bufferingText && frame.opcode < websocketOpcodeClose {
				return written, fmt.Errorf("websocket text message interrupted by opcode %d", frame.opcode)
			}
			n, err := writeProxyWebsocketFrame(dst, frame, true)
			written += n
			if err != nil {
				return written, err
			}
			if frame.opcode == websocketOpcodeClose {
				return written, nil
			}
		}
	}
}

func appendWebsocketTextFragment(dst *bytes.Buffer, payload []byte) error {
	if dst.Len()+len(payload) > defaultMaxRequestBody {
		return fmt.Errorf("websocket text message too large")
	}
	if _, err := dst.Write(payload); err != nil {
		return fmt.Errorf("buffer websocket text message: %w", err)
	}
	return nil
}

func writeToolInjectedTextFrame(dst io.Writer, payload []byte, injectionCtx responseToolInjectionContext) (int64, error) {
	updated, _, err := injectResponseTools(payload, injectionCtx)
	if err != nil {
		return 0, fmt.Errorf("inject response tools websocket message: %w", err)
	}
	return writeProxyWebsocketFrame(dst, proxyWebsocketFrame{
		opcode:  websocketOpcodeText,
		fin:     true,
		payload: updated,
	}, true)
}

func readProxyWebsocketFrame(r *bufio.Reader) (proxyWebsocketFrame, error) {
	var rawHeader [14]byte
	if _, err := io.ReadFull(r, rawHeader[:2]); err != nil {
		return proxyWebsocketFrame{}, fmt.Errorf("read websocket header: %w", err)
	}
	headerLen := websocketFrameHeaderLen(rawHeader[:2])
	if _, err := io.ReadFull(r, rawHeader[2:headerLen]); err != nil {
		return proxyWebsocketFrame{}, fmt.Errorf("read websocket extended header: %w", err)
	}
	header, err := parseWebsocketFrameHeader(rawHeader[:headerLen])
	if err != nil {
		return proxyWebsocketFrame{}, err
	}
	if header.payloadLen > defaultMaxRequestBody {
		return proxyWebsocketFrame{}, fmt.Errorf("websocket payload too large")
	}

	payload := make([]byte, header.payloadLen)
	if _, err := io.ReadFull(r, payload); err != nil {
		return proxyWebsocketFrame{}, fmt.Errorf("read websocket payload: %w", err)
	}
	if header.masked {
		applyWebsocketMask(payload, header.mask[:])
	}

	return proxyWebsocketFrame{
		opcode:  header.first & 0x0F,
		fin:     header.first&0x80 != 0,
		rsv:     header.first & 0x70,
		payload: payload,
	}, nil
}

func websocketFrameHeaderLen(header []byte) int {
	if len(header) < 2 {
		return 2
	}
	need := 2
	switch header[1] & 0x7F {
	case 126:
		need += 2
	case 127:
		need += 8
	}
	if header[1]&0x80 != 0 {
		need += 4
	}
	return need
}

func parseWebsocketFrameHeader(header []byte) (websocketFrameHeader, error) {
	need := websocketFrameHeaderLen(header)
	if len(header) < need {
		return websocketFrameHeader{}, fmt.Errorf("websocket header too short")
	}

	parsed := websocketFrameHeader{
		first:      header[0],
		payloadLen: uint64(header[1] & 0x7F),
		masked:     header[1]&0x80 != 0,
	}
	offset := 2
	switch parsed.payloadLen {
	case 126:
		parsed.payloadLen = uint64(binary.BigEndian.Uint16(header[offset : offset+2]))
		offset += 2
	case 127:
		parsed.payloadLen = binary.BigEndian.Uint64(header[offset : offset+8])
		offset += 8
	}
	if parsed.masked {
		copy(parsed.mask[:], header[offset:offset+4])
	}
	return parsed, nil
}

func writeProxyWebsocketFrame(w io.Writer, frame proxyWebsocketFrame, masked bool) (int64, error) {
	encoded, err := buildProxyWebsocketFrame(frame, masked)
	if err != nil {
		return 0, err
	}
	n, err := w.Write(encoded)
	if err != nil {
		return int64(n), fmt.Errorf("write websocket frame: %w", err)
	}
	return int64(n), nil
}

func buildProxyWebsocketFrame(frame proxyWebsocketFrame, masked bool) ([]byte, error) {
	var out bytes.Buffer
	first := frame.opcode | frame.rsv
	if frame.fin {
		first |= 0x80
	}
	if err := out.WriteByte(first); err != nil {
		return nil, fmt.Errorf("write websocket first byte: %w", err)
	}

	second := byte(0)
	if masked {
		second |= 0x80
	}
	switch n := len(frame.payload); {
	case n < 126:
		second |= byte(n)
		if err := out.WriteByte(second); err != nil {
			return nil, fmt.Errorf("write websocket second byte: %w", err)
		}
	case n <= 0xFFFF:
		second |= 126
		if err := out.WriteByte(second); err != nil {
			return nil, fmt.Errorf("write websocket second byte: %w", err)
		}
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(n))
		if _, err := out.Write(ext[:]); err != nil {
			return nil, fmt.Errorf("write websocket length16: %w", err)
		}
	default:
		second |= 127
		if err := out.WriteByte(second); err != nil {
			return nil, fmt.Errorf("write websocket second byte: %w", err)
		}
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		if _, err := out.Write(ext[:]); err != nil {
			return nil, fmt.Errorf("write websocket length64: %w", err)
		}
	}

	if !masked {
		if _, err := out.Write(frame.payload); err != nil {
			return nil, fmt.Errorf("write websocket payload: %w", err)
		}
		return out.Bytes(), nil
	}

	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return nil, fmt.Errorf("generate websocket mask: %w", err)
	}
	if _, err := out.Write(mask[:]); err != nil {
		return nil, fmt.Errorf("write websocket mask: %w", err)
	}
	maskedPayload := append([]byte(nil), frame.payload...)
	applyWebsocketMask(maskedPayload, mask[:])
	if _, err := out.Write(maskedPayload); err != nil {
		return nil, fmt.Errorf("write websocket masked payload: %w", err)
	}
	return out.Bytes(), nil
}
