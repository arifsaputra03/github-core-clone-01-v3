// Package git implements the git object model and the smart-HTTP protocol in
// pure Go, with no git binary anywhere: objects are hashed with SHA-1,
// packs are read and written by hand, and the protocol is pkt-lines over
// HTTP. The entry's HTTP layer (internal/http) only routes requests to the
// handlers in uploadpack.go and receivepack.go; everything about the wire
// lives here.
package git

import (
	"fmt"
)

// The pkt-line control packets, as git defines them.
const (
	// PktFlush is the "0000" flush packet: the end of a section.
	PktFlush = "0000"
	// PktDelim is the "0001" delimiter packet: it separates a command's
	// arguments from its options in protocol v2.
	PktDelim = "0001"
	// PktResponseEnd is the "0002" response-end packet.
	PktResponseEnd = "0002"
)

// PktLineMax is git's LARGE_PACKET_MAX: the largest pkt-line including its
// 4-byte length header.
const PktLineMax = 65520

// PacketKind is what a decoded pkt-line is.
type PacketKind int

const (
	// PacketData is a data packet carrying a payload.
	PacketData PacketKind = iota
	// PacketFlush is a "0000" flush packet.
	PacketFlush
	// PacketDelim is a "0001" delimiter packet.
	PacketDelim
	// PacketResponseEnd is a "0002" response-end packet.
	PacketResponseEnd
)

// Packet is one decoded pkt-line.
type Packet struct {
	Kind    PacketKind
	Payload []byte // only for PacketData
}

// EncodeLine frames payload as one pkt-line: a 4-hex-digit length (payload
// plus the 4 header bytes) followed by the payload.
func EncodeLine(payload []byte) []byte {
	out := make([]byte, 4+len(payload))
	n := len(payload) + 4
	hex := "0123456789abcdef"
	out[0] = hex[(n>>12)&0xf]
	out[1] = hex[(n>>8)&0xf]
	out[2] = hex[(n>>4)&0xf]
	out[3] = hex[n&0xf]
	copy(out[4:], payload)
	return out
}

// EncodeString frames a string payload as one pkt-line.
func EncodeString(s string) []byte { return EncodeLine([]byte(s)) }

// FlushPacket is the "0000" flush packet.
func FlushPacket() []byte { return []byte(PktFlush) }

// DelimPacket is the "0001" delimiter packet.
func DelimPacket() []byte { return []byte(PktDelim) }

// EncodeLines frames a sequence of data lines, flushes and delims into one
// byte string. A "flush" item is a flush packet, a "delim" item a delimiter.
func EncodeLines(items []any) []byte {
	var out []byte
	for _, item := range items {
		switch v := item.(type) {
		case string:
			out = append(out, EncodeString(v)...)
		case []byte:
			out = append(out, EncodeLine(v)...)
		case PacketKind:
			switch v {
			case PacketFlush:
				out = append(out, FlushPacket()...)
			case PacketDelim:
				out = append(out, DelimPacket()...)
			case PacketResponseEnd:
				out = append(out, []byte(PktResponseEnd)...)
			}
		}
	}
	return out
}

func hexVal(b byte) (int, bool) {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0'), true
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10, true
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10, true
	}
	return 0, false
}

// decodeOne decodes a single pkt-line at the start of data and returns the
// packet and the number of bytes consumed. It is what DecodePackets loops
// over, and what a receive-pack request uses to stop at the flush that
// precedes the raw pack bytes.
func decodeOne(data []byte) (Packet, int, error) {
	if len(data) < 4 {
		return Packet{}, 0, fmt.Errorf("git: truncated pkt-line: %d trailing byte(s)", len(data))
	}
	var length int
	for i := 0; i < 4; i++ {
		v, ok := hexVal(data[i])
		if !ok {
			return Packet{}, 0, fmt.Errorf("git: invalid pkt-line length %q", string(data[:4]))
		}
		length = length*16 + v
	}
	switch length {
	case 0:
		return Packet{Kind: PacketFlush}, 4, nil
	case 1:
		return Packet{Kind: PacketDelim}, 4, nil
	case 2:
		return Packet{Kind: PacketResponseEnd}, 4, nil
	case 3:
		return Packet{}, 0, fmt.Errorf("git: invalid pkt-line length 0003")
	default:
		if length > PktLineMax {
			return Packet{}, 0, fmt.Errorf("git: pkt-line length %d exceeds %d", length, PktLineMax)
		}
		if len(data) < length {
			return Packet{}, 0, fmt.Errorf("git: truncated pkt-line: %d announced, %d available", length, len(data))
		}
		payload := make([]byte, length-4)
		copy(payload, data[4:length])
		return Packet{Kind: PacketData, Payload: payload}, length, nil
	}
}

// DecodePackets parses a whole buffer of pkt-lines. It returns the packets
// in order. Trailing bytes that do not form a complete pkt-line are an error,
// as is a length over PktLineMax. A raw pack (which follows a flush in a
// receive-pack request) is not pkt-framed: callers stop parsing at the flush
// that precedes it and treat the rest as pack bytes.
func DecodePackets(data []byte) ([]Packet, error) {
	var packets []Packet
	pos := 0
	for pos < len(data) {
		pkt, n, err := decodeOne(data[pos:])
		if err != nil {
			return nil, err
		}
		packets = append(packets, pkt)
		pos += n
	}
	return packets, nil
}

// Lines returns the text of every data packet, dropping control packets.
func Lines(packets []Packet) []string {
	var out []string
	for _, p := range packets {
		if p.Kind == PacketData {
			out = append(out, string(p.Payload))
		}
	}
	return out
}
