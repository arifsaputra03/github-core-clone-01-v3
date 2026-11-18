// Package git implements the git object model and the smart-HTTP protocol
// in pure Go (see pktline.go).
package git

import (
	"fmt"
)

// ApplyDelta applies a git delta (the copy/insert instruction stream) to a
// base object's bytes and returns the reconstructed target.
//
// Delta format (git's Documentation/technical/pack-format.txt):
//   - source size, then target size, each a little-endian base-128 varint
//     (7 bits per byte, low group first, high bit set while more follow)
//   - instructions until the stream ends:
//     copy: opcode 0x80 | offset-bits | size-bits; the offset bytes
//     (little-endian) and size bytes (little-endian) follow as the
//     set bits name them; a size of 0 means 0x10000
//     insert: opcode 0x01..0x7f, the number of literal bytes that follow
//     0x00 is invalid
func ApplyDelta(base, delta []byte) ([]byte, error) {
	pos := 0
	readSize := func() (int, error) {
		value := 0
		shift := 0
		for {
			if pos >= len(delta) {
				return 0, fmt.Errorf("git: delta size varint is truncated")
			}
			b := delta[pos]
			pos++
			value += int(b&0x7f) << shift
			shift += 7
			if b&0x80 == 0 {
				break
			}
			if shift > 63 {
				return 0, fmt.Errorf("git: delta size varint is too long")
			}
		}
		return value, nil
	}
	sourceSize, err := readSize()
	if err != nil {
		return nil, err
	}
	targetSize, err := readSize()
	if err != nil {
		return nil, err
	}
	if sourceSize != len(base) {
		return nil, fmt.Errorf("git: delta expects a %d-byte base, got %d", sourceSize, len(base))
	}
	out := make([]byte, 0, targetSize)
	for pos < len(delta) {
		op := delta[pos]
		pos++
		if op&0x80 != 0 {
			// Copy from the base.
			copyOffset, copySize := 0, 0
			for bit := 0; bit < 4; bit++ {
				if op&(1<<uint(bit)) != 0 {
					if pos >= len(delta) {
						return nil, fmt.Errorf("git: delta copy offset is truncated")
					}
					copyOffset |= int(delta[pos]) << (8 * uint(bit))
					pos++
				}
			}
			for bit := 0; bit < 3; bit++ {
				if op&(1<<uint(bit+4)) != 0 {
					if pos >= len(delta) {
						return nil, fmt.Errorf("git: delta copy size is truncated")
					}
					copySize |= int(delta[pos]) << (8 * uint(bit))
					pos++
				}
			}
			if copySize == 0 {
				copySize = 0x10000
			}
			if copyOffset+copySize > len(base) {
				return nil, fmt.Errorf("git: delta copy is out of the base")
			}
			if len(out)+copySize > targetSize {
				return nil, fmt.Errorf("git: delta writes past its target size")
			}
			out = append(out, base[copyOffset:copyOffset+copySize]...)
		} else if op != 0 {
			// Insert literal bytes.
			n := int(op & 0x7f)
			if pos+n > len(delta) {
				return nil, fmt.Errorf("git: delta insert is truncated")
			}
			if len(out)+n > targetSize {
				return nil, fmt.Errorf("git: delta writes past its target size")
			}
			out = append(out, delta[pos:pos+n]...)
			pos += n
		} else {
			return nil, fmt.Errorf("git: delta opcode 0 is invalid")
		}
	}
	if len(out) != targetSize {
		return nil, fmt.Errorf("git: delta produced %d of %d bytes", len(out), targetSize)
	}
	return out, nil
}
