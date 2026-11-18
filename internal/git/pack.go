// Package git implements the git object model and the smart-HTTP protocol
// in pure Go (see pktline.go).
package git

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
)

// Pack is a parsed packfile: every object it holds, resolved from its
// entries (deltas applied), keyed by id.
type Pack struct {
	Version    int
	Declared   int
	Objects    map[string]*Object
	DeltaCount int
	ChecksumOK bool
	ByteLength int
}

// countReader counts the bytes read from an underlying reader. It implements
// io.ByteReader one byte at a time, so a zlib/flate reader built on it can
// never read past the end of the stream it is inflating: the count is the
// stream's exact length.
type countReader struct {
	r *bytes.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *countReader) ReadByte() (byte, error) {
	b, err := c.r.ReadByte()
	if err == nil {
		c.n++
	}
	return b, err
}

// inflateAt inflates the zlib stream that starts at offset in data and
// returns the inflated bytes and the offset just past the stream. The stream
// length is measured exactly (see countReader), so the caller can find the
// next pack entry.
func inflateAt(data []byte, offset int) ([]byte, int, error) {
	if offset >= len(data) {
		return nil, 0, fmt.Errorf("git: zlib stream at offset %d is past the end", offset)
	}
	cr := &countReader{r: bytes.NewReader(data[offset:])}
	zr, err := zlib.NewReader(cr)
	if err != nil {
		return nil, 0, fmt.Errorf("git: zlib stream at offset %d: %w", offset, err)
	}
	defer zr.Close()
	plain, err := io.ReadAll(zr)
	if err != nil {
		return nil, 0, fmt.Errorf("git: zlib stream at offset %d: %w", offset, err)
	}
	return plain, offset + int(cr.n), nil
}

// readPackVarint reads a pack object header's size varint: the first byte
// holds the low 4 bits of the size and the type in bits 4..6, then 7 bits
// per byte, little-endian, while the high bit is set.
func readPackVarint(data []byte, start int) (size int, typ int, next int, err error) {
	if start >= len(data) {
		return 0, 0, 0, fmt.Errorf("git: pack entry has no size header")
	}
	b := data[start]
	typ = int(b>>4) & 0x07
	size = int(b & 0x0f)
	shift := 4
	pos := start + 1
	for b&0x80 != 0 {
		if pos >= len(data) {
			return 0, 0, 0, fmt.Errorf("git: pack entry size varint is truncated")
		}
		b = data[pos]
		pos++
		size += int(b&0x7f) << shift
		shift += 7
		if shift > 63 {
			return 0, 0, 0, fmt.Errorf("git: pack entry size varint is too long")
		}
	}
	return size, typ, pos, nil
}

// readOfsBase reads an ofs-delta base offset: the first byte's low 7 bits,
// then each following byte adds (value+1)*128 + (byte & 0x7f).
func readOfsBase(data []byte, start int) (value int, next int, err error) {
	if start >= len(data) {
		return 0, 0, fmt.Errorf("git: ofs-delta has no base offset")
	}
	b := data[start]
	pos := start + 1
	value = int(b & 0x7f)
	for b&0x80 != 0 {
		if pos >= len(data) {
			return 0, 0, fmt.Errorf("git: ofs-delta base offset is truncated")
		}
		b = data[pos]
		pos++
		value = (value+1)*128 + int(b&0x7f)
	}
	return value, pos, nil
}

// rawEntry is one pack entry before delta resolution.
type rawEntry struct {
	offset     int
	typeCode   int
	size       int
	data       []byte // inflated (delta) data, or the object's bytes
	baseOffset int    // ofs-delta: the base entry's offset
	baseOID    string // ref-delta: the base object's id
	resolved   bool
	resType    ObjectType
	resData    []byte
	resOID     string
}

// ReadPack parses a packfile (version 2 or 3), resolves every entry
// (OFS_DELTA and REF_DELTA, recursively), verifies the trailer checksum and
// returns the objects. resolveBase supplies a base object a thin pack leaves
// out (an object the caller already holds); it may be nil.
func ReadPack(data []byte, resolveBase func(oid string) (*Object, error)) (*Pack, error) {
	if len(data) < 32 {
		return nil, fmt.Errorf("git: pack is %d bytes, too short", len(data))
	}
	if string(data[0:4]) != "PACK" {
		return nil, fmt.Errorf("git: pack does not start with PACK")
	}
	version := int(binary.BigEndian.Uint32(data[4:8]))
	if version != 2 && version != 3 {
		return nil, fmt.Errorf("git: unsupported pack version %d", version)
	}
	declared := int(binary.BigEndian.Uint32(data[8:12]))
	trailerStart := len(data) - 20
	trailer := fmt.Sprintf("%x", data[trailerStart:])
	sum := sha1.Sum(data[:trailerStart])
	checksumOK := fmt.Sprintf("%x", sum[:]) == trailer

	raw := make([]*rawEntry, 0, declared)
	byOffset := map[int]*rawEntry{}
	offset := 12
	for i := 0; i < declared; i++ {
		entryOffset := offset
		size, typ, next, err := readPackVarint(data, offset)
		if err != nil {
			return nil, err
		}
		offset = next
		entry := &rawEntry{offset: entryOffset, typeCode: typ, size: size}
		if typ == 6 {
			base, next, err := readOfsBase(data, offset)
			if err != nil {
				return nil, err
			}
			offset = next
			if base <= 0 || base > entryOffset {
				return nil, fmt.Errorf("git: ofs-delta at %d names an invalid base offset %d", entryOffset, base)
			}
			entry.baseOffset = entryOffset - base
		} else if typ == 7 {
			if offset+20 > trailerStart {
				return nil, fmt.Errorf("git: ref-delta base id is truncated")
			}
			entry.baseOID = fmt.Sprintf("%x", data[offset:offset+20])
			offset += 20
		} else if _, err := ObjectTypeFromCode(typ); err != nil {
			return nil, fmt.Errorf("git: pack entry %d has unknown type code %d", i, typ)
		}
		plain, next, err := inflateAt(data[:trailerStart], offset)
		if err != nil {
			return nil, err
		}
		if len(plain) != size {
			return nil, fmt.Errorf("git: pack entry at %d declares %d bytes but inflated to %d", entryOffset, size, len(plain))
		}
		entry.data = plain
		offset = next
		raw = append(raw, entry)
		byOffset[entryOffset] = entry
	}
	if offset != trailerStart {
		return nil, fmt.Errorf("git: pack has %d stray bytes before its trailer", trailerStart-offset)
	}

	objects := map[string]*Object{}
	resolvedByOID := map[string]*rawEntry{}
	resolving := map[int]bool{}
	deltaCount := 0

	var resolve func(e *rawEntry) (ObjectType, []byte, string, error)
	resolve = func(e *rawEntry) (ObjectType, []byte, string, error) {
		if e.resolved {
			return e.resType, e.resData, e.resOID, nil
		}
		if resolving[e.offset] {
			return 0, nil, "", fmt.Errorf("git: delta cycle at pack offset %d", e.offset)
		}
		resolving[e.offset] = true
		defer delete(resolving, e.offset)
		var typ ObjectType
		var content []byte
		switch e.typeCode {
		case 6:
			baseEntry, ok := byOffset[e.baseOffset]
			if !ok {
				return 0, nil, "", fmt.Errorf("git: ofs-delta base at offset %d is not in the pack", e.baseOffset)
			}
			baseType, baseData, _, err := resolve(baseEntry)
			if err != nil {
				return 0, nil, "", err
			}
			typ = baseType
			content, err = ApplyDelta(baseData, e.data)
			if err != nil {
				return 0, nil, "", err
			}
		case 7:
			// The base is an entry in this pack, or an object the caller
			// already holds (a thin pack).
			var baseType ObjectType
			var baseData []byte
			if baseEntry, ok := resolvedByOID[e.baseOID]; ok {
				var err error
				baseType, baseData, _, err = resolve(baseEntry)
				if err != nil {
					return 0, nil, "", err
				}
			} else {
				found := false
				for _, candidate := range raw {
					if resolving[candidate.offset] {
						continue
					}
					_, _, oid, err := resolve(candidate)
					if err != nil {
						continue
					}
					if oid == e.baseOID {
						baseType, baseData, _, err = resolve(candidate)
						if err != nil {
							return 0, nil, "", err
						}
						found = true
						break
					}
				}
				if !found {
					if resolveBase == nil {
						return 0, nil, "", fmt.Errorf("git: ref-delta base %s is neither in the pack nor supplied", e.baseOID)
					}
					ext, err := resolveBase(e.baseOID)
					if err != nil {
						return 0, nil, "", fmt.Errorf("git: ref-delta base %s: %w", e.baseOID, err)
					}
					baseType, baseData = ext.Type, ext.Data
				}
			}
			typ = baseType
			var err error
			content, err = ApplyDelta(baseData, e.data)
			if err != nil {
				return 0, nil, "", err
			}
		default:
			var err error
			typ, err = ObjectTypeFromCode(e.typeCode)
			if err != nil {
				return 0, nil, "", err
			}
			content = e.data
		}
		oid := HashObject(typ, content)
		e.resolved = true
		e.resType, e.resData, e.resOID = typ, content, oid
		resolvedByOID[oid] = e
		return typ, content, oid, nil
	}

	for _, e := range raw {
		if e.typeCode == 6 || e.typeCode == 7 {
			deltaCount++
		}
		typ, content, oid, err := resolve(e)
		if err != nil {
			return nil, err
		}
		if _, ok := objects[oid]; !ok {
			objects[oid] = &Object{Type: typ, Data: content}
		}
	}
	if !checksumOK {
		return nil, fmt.Errorf("git: pack trailer %s does not match its bytes", trailer)
	}
	return &Pack{
		Version:    version,
		Declared:   declared,
		Objects:    objects,
		DeltaCount: deltaCount,
		ChecksumOK: checksumOK,
		ByteLength: len(data),
	}, nil
}

// writePackObjectHeader encodes one object's pack header: the type/size
// varint git's pack format uses.
func writePackObjectHeader(typ ObjectType, size int) []byte {
	code := typ.TypeCode()
	first := (code << 4) | (size & 0x0f)
	remaining := size >> 4
	var out []byte
	for remaining > 0 {
		out = append(out, byte(first|0x80))
		first = remaining & 0x7f
		remaining >>= 7
	}
	out = append(out, byte(first))
	return out
}

// WritePack builds a packfile from plain objects (no deltas). The order is
// the caller's; git accepts any order. The trailer is SHA-1 over everything
// before it. The object list must be deterministic (same input, same bytes)
// so two runs of the same fetch produce the same pack.
func WritePack(objects []Object) ([]byte, error) {
	var body bytes.Buffer
	header := make([]byte, 12)
	copy(header[0:4], "PACK")
	binary.BigEndian.PutUint32(header[4:8], 2)
	binary.BigEndian.PutUint32(header[8:12], uint32(len(objects)))
	body.Write(header)
	for _, obj := range objects {
		body.Write(writePackObjectHeader(obj.Type, len(obj.Data)))
		zw := zlib.NewWriter(&body)
		if _, err := zw.Write(obj.Data); err != nil {
			return nil, err
		}
		if err := zw.Close(); err != nil {
			return nil, err
		}
	}
	pack := body.Bytes()
	sum := sha1.Sum(pack)
	return append(pack, sum[:]...), nil
}

// SortObjects orders objects deterministically for a pack: commits first,
// then tags, trees, blobs, each by id. Two fetches of the same set produce
// the same pack bytes.
func SortObjects(objects map[string]*Object) []Object {
	out := make([]Object, 0, len(objects))
	ids := make([]string, 0, len(objects))
	for oid, obj := range objects {
		out = append(out, Object{Type: obj.Type, Data: obj.Data})
		ids = append(ids, oid)
	}
	rank := func(t ObjectType) int {
		switch t {
		case TypeCommit:
			return 0
		case TypeTag:
			return 1
		case TypeTree:
			return 2
		default:
			return 3
		}
	}
	type pair struct {
		oid string
		obj Object
	}
	pairs := make([]pair, len(out))
	for i := range out {
		pairs[i] = pair{oid: ids[i], obj: out[i]}
	}
	sort.Slice(pairs, func(i, j int) bool {
		ri, rj := rank(pairs[i].obj.Type), rank(pairs[j].obj.Type)
		if ri != rj {
			return ri < rj
		}
		return pairs[i].oid < pairs[j].oid
	})
	for i := range pairs {
		out[i] = pairs[i].obj
	}
	return out
}
