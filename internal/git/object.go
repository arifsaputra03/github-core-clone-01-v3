// Package git implements the git object model and the smart-HTTP protocol
// in pure Go (see pktline.go).
package git

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"sort"
)

// ObjectType is one of git's four object types.
type ObjectType int

const (
	// TypeCommit is a commit object (pack type code 1).
	TypeCommit ObjectType = 1
	// TypeTree is a tree object (pack type code 2).
	TypeTree ObjectType = 2
	// TypeBlob is a blob object (pack type code 3).
	TypeBlob ObjectType = 3
	// TypeTag is an annotated tag object (pack type code 4).
	TypeTag ObjectType = 4
)

// Name returns the type's name as git spells it in object headers.
func (t ObjectType) Name() string {
	switch t {
	case TypeCommit:
		return "commit"
	case TypeTree:
		return "tree"
	case TypeBlob:
		return "blob"
	case TypeTag:
		return "tag"
	}
	return "unknown"
}

// TypeCode returns the type's pack type code.
func (t ObjectType) TypeCode() int { return int(t) }

// ObjectTypeFromCode returns the type for a pack type code, or an error.
func ObjectTypeFromCode(code int) (ObjectType, error) {
	switch code {
	case 1:
		return TypeCommit, nil
	case 2:
		return TypeTree, nil
	case 3:
		return TypeBlob, nil
	case 4:
		return TypeTag, nil
	}
	return 0, fmt.Errorf("git: unknown object type code %d", code)
}

// Object is one git object: its type and its exact byte content (no header;
// the hash is computed over header + content).
type Object struct {
	Type ObjectType
	Data []byte
}

// HashObject is git's object id: SHA-1 over "<type> <size>\0" + content.
func HashObject(typ ObjectType, data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "%s %d\x00", typ.Name(), len(data))
	h.Write(data)
	return fmt.Sprintf("%x", h.Sum(nil))
}

// IsOID reports whether s is a lowercase 40-hex object id.
func IsOID(s string) bool {
	if len(s) != 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// ZeroOID is the all-zero object id: an unborn branch's old/new value.
const ZeroOID = "0000000000000000000000000000000000000000"

// TreeEntry is one entry of a tree object.
type TreeEntry struct {
	Mode string // "100644", "40000", "160000", ...
	Name string
	OID  string
}

// compareTreeEntries is git's tree-entry sort: byte order of the name, a
// directory compared as if its name ended in '/'.
func compareTreeEntries(a, b TreeEntry) int {
	an, bn := a.Name, b.Name
	if a.Mode == "40000" {
		an += "/"
	}
	if b.Mode == "40000" {
		bn += "/"
	}
	return bytes.Compare([]byte(an), []byte(bn))
}

// ParseTree parses a tree object into its entries, in stored order.
func ParseTree(data []byte) ([]TreeEntry, error) {
	var entries []TreeEntry
	pos := 0
	for pos < len(data) {
		space := bytes.IndexByte(data[pos:], ' ')
		if space < 0 {
			return nil, fmt.Errorf("git: tree entry has no mode separator")
		}
		mode := string(data[pos : pos+space])
		rest := data[pos+space+1:]
		nul := bytes.IndexByte(rest, 0)
		if nul < 0 {
			return nil, fmt.Errorf("git: tree entry has no name terminator")
		}
		name := string(rest[:nul])
		if nul+21 > len(rest) {
			return nil, fmt.Errorf("git: tree entry has a truncated object id")
		}
		oid := fmt.Sprintf("%x", rest[nul+1:nul+21])
		for _, c := range mode {
			if c < '0' || c > '7' {
				return nil, fmt.Errorf("git: tree entry mode %q is not octal", mode)
			}
		}
		entries = append(entries, TreeEntry{Mode: mode, Name: name, OID: oid})
		pos = pos + space + 1 + nul + 21
	}
	return entries, nil
}

// BuildTree builds a tree object's bytes from entries, sorted the way git
// sorts them.
func BuildTree(entries []TreeEntry) ([]byte, error) {
	sorted := make([]TreeEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool { return compareTreeEntries(sorted[i], sorted[j]) < 0 })
	var buf bytes.Buffer
	for _, e := range sorted {
		if !IsOID(e.OID) {
			return nil, fmt.Errorf("git: tree entry %q has a bad object id", e.Name)
		}
		fmt.Fprintf(&buf, "%s %s\x00", e.Mode, e.Name)
		raw := make([]byte, 20)
		for i := 0; i < 20; i++ {
			var v int
			fmt.Sscanf(e.OID[i*2:i*2+2], "%02x", &v)
			raw[i] = byte(v)
		}
		buf.Write(raw)
	}
	return buf.Bytes(), nil
}

// Commit is a parsed commit object.
type Commit struct {
	Tree      string
	Parents   []string
	Author    string
	Committer string
	Message   string
	// Extra holds any headers before the blank line that this package does
	// not model (gpgsig, encoding, ...), in order.
	Extra [][2]string
}

// ParseCommit parses a commit object's bytes.
func ParseCommit(data []byte) (*Commit, error) {
	blank := bytes.Index(data, []byte("\n\n"))
	headerEnd := len(data)
	if blank >= 0 {
		headerEnd = blank
	}
	message := ""
	if blank >= 0 {
		message = string(data[blank+2:])
	}
	c := &Commit{}
	for _, line := range bytes.Split(data[:headerEnd], []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		space := bytes.IndexByte(line, ' ')
		key, value := string(line), ""
		if space >= 0 {
			key, value = string(line[:space]), string(line[space+1:])
		}
		switch key {
		case "tree":
			c.Tree = value
		case "parent":
			c.Parents = append(c.Parents, value)
		case "author":
			c.Author = value
		case "committer":
			c.Committer = value
		default:
			c.Extra = append(c.Extra, [2]string{key, value})
		}
	}
	if !IsOID(c.Tree) {
		return nil, fmt.Errorf("git: commit has no valid tree header")
	}
	for _, p := range c.Parents {
		if !IsOID(p) {
			return nil, fmt.Errorf("git: commit has a bad parent id")
		}
	}
	c.Message = message
	return c, nil
}

// BuildCommit builds a commit object's bytes. The message is used as-is (the
// caller ends it with a newline when it wants one).
func BuildCommit(tree string, parents []string, author, committer, message string) ([]byte, error) {
	if !IsOID(tree) {
		return nil, fmt.Errorf("git: buildCommit needs a valid tree id")
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "tree %s\n", tree)
	for _, p := range parents {
		if !IsOID(p) {
			return nil, fmt.Errorf("git: buildCommit needs valid parent ids")
		}
		fmt.Fprintf(&buf, "parent %s\n", p)
	}
	fmt.Fprintf(&buf, "author %s\n", author)
	fmt.Fprintf(&buf, "committer %s\n", committer)
	buf.WriteString("\n")
	buf.WriteString(message)
	return buf.Bytes(), nil
}

// Tag is a parsed annotated tag object.
type Tag struct {
	Object  string
	Type    ObjectType
	Tag     string
	Tagger  string
	Message string
}

// ParseTag parses an annotated tag object's bytes.
func ParseTag(data []byte) (*Tag, error) {
	blank := bytes.Index(data, []byte("\n\n"))
	headerEnd := len(data)
	if blank >= 0 {
		headerEnd = blank
	}
	headers := map[string]string{}
	for _, line := range bytes.Split(data[:headerEnd], []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		space := bytes.IndexByte(line, ' ')
		if space < 0 {
			return nil, fmt.Errorf("git: tag header %q has no value", string(line))
		}
		headers[string(line[:space])] = string(line[space+1:])
	}
	object := headers["object"]
	if !IsOID(object) {
		return nil, fmt.Errorf("git: tag has no valid object header")
	}
	typ, err := ObjectTypeFromName(headers["type"])
	if err != nil {
		return nil, fmt.Errorf("git: tag has bad type %q", headers["type"])
	}
	message := ""
	if blank >= 0 {
		message = string(data[blank+2:])
	}
	return &Tag{Object: object, Type: typ, Tag: headers["tag"], Tagger: headers["tagger"], Message: message}, nil
}

// ObjectTypeFromName returns the type for a name ("commit", "tree", ...).
func ObjectTypeFromName(name string) (ObjectType, error) {
	switch name {
	case "commit":
		return TypeCommit, nil
	case "tree":
		return TypeTree, nil
	case "blob":
		return TypeBlob, nil
	case "tag":
		return TypeTag, nil
	}
	return 0, fmt.Errorf("git: unknown object type %q", name)
}

// BuildTag builds an annotated tag object's bytes.
func BuildTag(object string, typ ObjectType, tag, tagger, message string) ([]byte, error) {
	if !IsOID(object) {
		return nil, fmt.Errorf("git: buildTag needs a valid object id")
	}
	return []byte(fmt.Sprintf("object %s\ntype %s\ntag %s\ntagger %s\n\n%s",
		object, typ.Name(), tag, tagger, message)), nil
}
