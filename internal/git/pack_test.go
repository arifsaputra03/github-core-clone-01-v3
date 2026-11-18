// Tests for the pack reader/writer. The critical property under test is
// that inflateAt measures a zlib stream's exact length: the pack reader
// locates the next entry by it, so an off-by-one would corrupt every pack.
package git

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"testing"
)

func compress(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestInflateAtExact verifies that inflating a zlib stream reports the
// offset just past it, so a second stream starting there inflates cleanly.
func TestInflateAtExact(t *testing.T) {
	first := []byte("hello world\n")
	second := []byte("another blob, longer than the first one, with more text\n")
	joined := append(append([]byte{}, compress(t, first)...), compress(t, second)...)

	plain1, next, err := inflateAt(joined, 0)
	if err != nil {
		t.Fatalf("inflateAt first stream: %v", err)
	}
	if !bytes.Equal(plain1, first) {
		t.Fatalf("first stream inflated to %q, want %q", plain1, first)
	}
	if next != len(compress(t, first)) {
		t.Fatalf("inflateAt next offset = %d, want %d (the first stream's length)", next, len(compress(t, first)))
	}
	plain2, next2, err := inflateAt(joined, next)
	if err != nil {
		t.Fatalf("inflateAt second stream: %v", err)
	}
	if !bytes.Equal(plain2, second) {
		t.Fatalf("second stream inflated to %q, want %q", plain2, second)
	}
	if next2 != len(joined) {
		t.Fatalf("inflateAt end offset = %d, want %d", next2, len(joined))
	}
}

// TestPackRoundTrip writes a pack and reads it back, checking every object
// and the trailer checksum.
func TestPackRoundTrip(t *testing.T) {
	blob := &Object{Type: TypeBlob, Data: []byte("readme\n")}
	tree, err := BuildTree([]TreeEntry{{Mode: "100644", Name: "README.md", OID: HashObject(TypeBlob, blob.Data)}})
	if err != nil {
		t.Fatal(err)
	}
	commit, err := BuildCommit(HashObject(TypeTree, tree), nil, "Ada Iversen <ada@example.invalid> 1790000000 +0000", "Ada Iversen <ada@example.invalid> 1790000000 +0000", "initial\n")
	if err != nil {
		t.Fatal(err)
	}
	objs := []Object{
		{Type: TypeBlob, Data: blob.Data},
		{Type: TypeTree, Data: tree},
		{Type: TypeCommit, Data: commit},
	}
	packed, err := WritePack(objs)
	if err != nil {
		t.Fatalf("WritePack: %v", err)
	}
	pack, err := ReadPack(packed, nil)
	if err != nil {
		t.Fatalf("ReadPack: %v", err)
	}
	if !pack.ChecksumOK {
		t.Fatal("pack trailer checksum did not verify")
	}
	if len(pack.Objects) != len(objs) {
		t.Fatalf("pack holds %d objects, want %d", len(pack.Objects), len(objs))
	}
	for _, want := range objs {
		oid := HashObject(want.Type, want.Data)
		got, ok := pack.Objects[oid]
		if !ok {
			t.Fatalf("pack is missing object %s", oid)
		}
		if got.Type != want.Type || !bytes.Equal(got.Data, want.Data) {
			t.Fatalf("object %s differs from what was packed", oid)
		}
	}
}

// encodeOfsBase encodes a distance the way git writes an ofs-delta base
// offset (see pack-format.txt): the low 7 bits first, then each following
// byte contributes (value+1)*128 + (byte & 0x7f).
func encodeOfsBase(distance int) []byte {
	buf := []byte{byte(distance & 0x7f)}
	distance >>= 7
	for distance > 0 {
		distance--
		buf = append(buf, byte(0x80|(distance&0x7f)))
		distance >>= 7
	}
	// Reverse so the most significant group comes first.
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return buf
}

// TestPackOfsDelta builds a pack by hand with one full blob and one
// ofs-delta against it, and checks the delta resolves to the right object.
func TestPackOfsDelta(t *testing.T) {
	base := []byte("aaaa bbbb cccc\n")
	target := []byte("aaaa XXXX cccc\n")
	// Delta: source 15, target 15; copy 5 from 0, insert "XXXX", copy 6 from 9.
	delta := []byte{
		0x0f, 0x0f, // source size 15, target size 15
		0x91, 0x00, 0x05, // copy offset 0, size 5
		0x04, 'X', 'X', 'X', 'X', // insert "XXXX"
		0x91, 0x09, 0x06, // copy offset 9, size 6
	}
	var body bytes.Buffer
	header := make([]byte, 12)
	copy(header[0:4], "PACK")
	header[4] = 0
	header[5] = 0
	header[6] = 0
	header[7] = 2 // version 2
	header[8] = 0
	header[9] = 0
	header[10] = 0
	header[11] = 2 // two objects
	body.Write(header)
	baseOffset := body.Len()
	body.Write(writePackObjectHeader(TypeBlob, len(base)))
	body.Write(compress(t, base))
	deltaOffset := body.Len()
	body.Write(writePackObjectHeader(ObjectType(6), len(delta)))
	body.Write(encodeOfsBase(deltaOffset - baseOffset))
	body.Write(compress(t, delta))
	packed := body.Bytes()
	sum := [20]byte{}
	_ = sum
	// Trailer: SHA-1 over the bytes so far.
	packed = append(packed, sha1Sum(packed)...)
	pack, err := ReadPack(packed, nil)
	if err != nil {
		t.Fatalf("ReadPack: %v", err)
	}
	if pack.DeltaCount != 1 {
		t.Fatalf("pack delta count = %d, want 1", pack.DeltaCount)
	}
	got, ok := pack.Objects[HashObject(TypeBlob, target)]
	if !ok {
		t.Fatalf("pack is missing the delta target %s", HashObject(TypeBlob, target))
	}
	if !bytes.Equal(got.Data, target) {
		t.Fatalf("delta resolved to %q, want %q", got.Data, target)
	}
	if _, ok := pack.Objects[HashObject(TypeBlob, base)]; !ok {
		t.Fatal("pack is missing the base blob")
	}
}

func sha1Sum(data []byte) []byte {
	sum := sha1.Sum(data)
	return sum[:]
}
