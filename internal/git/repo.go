// Package git implements the git object model and the smart-HTTP protocol
// in pure Go (see pktline.go).
package git

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrRefNotFound is returned when a ref does not exist.
var ErrRefNotFound = errors.New("git: ref not found")

// ErrObjectNotFound is returned when an object is not in the store.
var ErrObjectNotFound = errors.New("git: object not found")

// Repo is one bare repository on disk: a loose-object store under objects/
// and loose refs under refs/, with HEAD naming the default branch. It is
// the only type that touches the filesystem for git data; upload-pack and
// receive-pack work through it.
type Repo struct {
	// Path is the bare repository directory.
	Path string
}

// Open returns a Repo for an existing bare repository directory.
func Open(path string) *Repo { return &Repo{Path: path} }

func (r *Repo) objectPath(oid string) string {
	return filepath.Join(r.Path, "objects", oid[:2], oid[2:])
}

func (r *Repo) refPath(name string) string {
	return filepath.Join(r.Path, filepath.FromSlash(name))
}

// HasObject reports whether the object is in the store.
func (r *Repo) HasObject(oid string) bool {
	if !IsOID(oid) {
		return false
	}
	_, err := os.Stat(r.objectPath(oid))
	return err == nil
}

// ReadObject reads a loose object and returns its type and content.
func (r *Repo) ReadObject(oid string) (*Object, error) {
	if !IsOID(oid) {
		return nil, fmt.Errorf("%w: %q", ErrObjectNotFound, oid)
	}
	raw, err := os.ReadFile(r.objectPath(oid))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrObjectNotFound, oid)
		}
		return nil, err
	}
	zr, err := zlib.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("git: object %s: %w", oid, err)
	}
	defer zr.Close()
	plain, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("git: object %s: %w", oid, err)
	}
	nul := bytes.IndexByte(plain, 0)
	if nul < 0 {
		return nil, fmt.Errorf("git: object %s has no header", oid)
	}
	header := string(plain[:nul])
	space := strings.IndexByte(header, ' ')
	if space < 0 {
		return nil, fmt.Errorf("git: object %s has a bad header", oid)
	}
	typ, err := ObjectTypeFromName(header[:space])
	if err != nil {
		return nil, fmt.Errorf("git: object %s: %w", oid, err)
	}
	return &Object{Type: typ, Data: plain[nul+1:]}, nil
}

// WriteObject stores a loose object (zlib-compressed "<type> <size>\0" +
// content) and returns its id. An object that already exists is left alone.
func (r *Repo) WriteObject(typ ObjectType, data []byte) (string, error) {
	oid := HashObject(typ, data)
	path := r.objectPath(oid)
	if _, err := os.Stat(path); err == nil {
		return oid, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	fmt.Fprintf(zw, "%s %d\x00", typ.Name(), len(data))
	zw.Write(data)
	if err := zw.Close(); err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o444); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		// Another writer may have created it first; that is fine.
		if _, statErr := os.Stat(path); statErr == nil {
			os.Remove(tmp)
			return oid, nil
		}
		return "", err
	}
	return oid, nil
}

// WriteObjectWithOID stores an object whose id is already known (it came
// from a pack, so its content is trusted to hash to oid). It verifies the
// hash before storing, so a corrupt pack cannot poison the store.
func (r *Repo) WriteObjectWithOID(oid string, typ ObjectType, data []byte) error {
	if HashObject(typ, data) != oid {
		return fmt.Errorf("git: object %s does not hash to its id", oid)
	}
	_, err := r.WriteObject(typ, data)
	return err
}

// Ref is one reference: its full name and the object id it points at.
type Ref struct {
	Name string
	OID  string
}

// ReadRef returns the object id a ref points at, or ErrRefNotFound.
func (r *Repo) ReadRef(name string) (string, error) {
	raw, err := os.ReadFile(r.refPath(name))
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %s", ErrRefNotFound, name)
		}
		return "", err
	}
	oid := strings.TrimSpace(string(raw))
	if !IsOID(oid) {
		return "", fmt.Errorf("git: ref %s holds a bad object id %q", name, oid)
	}
	return oid, nil
}

// WriteRef points a ref at an object id, creating parent directories. The
// write is atomic (temporary file + rename).
func (r *Repo) WriteRef(name, oid string) error {
	if !IsOID(oid) {
		return fmt.Errorf("git: ref %s: bad object id %q", name, oid)
	}
	path := r.refPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(oid+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// DeleteRef removes a ref. Deleting a ref that does not exist is an error.
func (r *Repo) DeleteRef(name string) error {
	if err := os.Remove(r.refPath(name)); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrRefNotFound, name)
		}
		return err
	}
	return nil
}

// ListRefs returns every ref in the repository, sorted by name.
func (r *Repo) ListRefs() ([]Ref, error) {
	root := filepath.Join(r.Path, "refs")
	var refs []Ref
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(r.Path, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		oid, err := r.ReadRef(name)
		if err != nil {
			return nil
		}
		refs = append(refs, Ref{Name: name, OID: oid})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Name < refs[j].Name })
	return refs, nil
}

// HeadTarget returns the ref HEAD points at ("refs/heads/main"), or "" when
// HEAD is detached (it holds a raw object id instead).
func (r *Repo) HeadTarget() (string, error) {
	raw, err := os.ReadFile(filepath.Join(r.Path, "HEAD"))
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(string(raw))
	if strings.HasPrefix(text, "ref: ") {
		return strings.TrimSpace(text[len("ref: "):]), nil
	}
	return "", nil
}

// HeadOID returns the object id HEAD resolves to, or ZeroOID when the
// default branch is unborn (no commit yet).
func (r *Repo) HeadOID() (string, error) {
	target, err := r.HeadTarget()
	if err != nil {
		return "", err
	}
	if target == "" {
		raw, err := os.ReadFile(filepath.Join(r.Path, "HEAD"))
		if err != nil {
			return "", err
		}
		oid := strings.TrimSpace(string(raw))
		if IsOID(oid) {
			return oid, nil
		}
		return ZeroOID, nil
	}
	oid, err := r.ReadRef(target)
	if err != nil {
		if errors.Is(err, ErrRefNotFound) {
			return ZeroOID, nil
		}
		return "", err
	}
	return oid, nil
}

// IsAncestor reports whether ancestor is reachable from descendant's commit
// ancestry (following parent links). A commit is its own ancestor.
func (r *Repo) IsAncestor(ancestor, descendant string) (bool, error) {
	if ancestor == descendant {
		return true, nil
	}
	if !IsOID(ancestor) || !IsOID(descendant) {
		return false, fmt.Errorf("git: IsAncestor needs object ids")
	}
	seen := map[string]bool{}
	queue := []string{descendant}
	for len(queue) > 0 {
		oid := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if seen[oid] {
			continue
		}
		seen[oid] = true
		if oid == ancestor {
			return true, nil
		}
		obj, err := r.ReadObject(oid)
		if err != nil {
			if errors.Is(err, ErrObjectNotFound) {
				continue
			}
			return false, err
		}
		if obj.Type != TypeCommit {
			continue
		}
		commit, err := ParseCommit(obj.Data)
		if err != nil {
			return false, err
		}
		queue = append(queue, commit.Parents...)
	}
	return false, nil
}

// WalkResult is the outcome of a reachability walk: the objects it found,
// and, for a depth-limited walk, the commits at the boundary (the shallow
// line). Objects are keyed by id.
type WalkResult struct {
	Objects map[string]*Object
	// Shallow holds the boundary commits of a depth-limited walk: the
	// deepest commits included, whose parents were not followed.
	Shallow []string
}

// walkState is the shared machinery of the two walks below.
type walkState struct {
	repo *Repo
	seen map[string]bool
	out  map[string]*Object
}

// visit reads one object and records it; false when it was already seen.
func (w *walkState) visit(oid string) (*Object, bool, error) {
	if w.seen[oid] {
		return nil, false, nil
	}
	obj, err := w.repo.ReadObject(oid)
	if err != nil {
		return nil, false, err
	}
	w.seen[oid] = true
	w.out[oid] = obj
	return obj, true, nil
}

// WalkReachable collects every object reachable from roots, with no depth
// limit. It does not traverse past a shallow boundary commit (a commit in
// stopAt is recorded with its tree and blobs, but its parents are not
// followed): that is the set of objects a shallow client already holds.
func (r *Repo) WalkReachable(roots []string, stopAt map[string]bool) (*WalkResult, error) {
	w := &walkState{repo: r, seen: map[string]bool{}, out: map[string]*Object{}}
	queue := append([]string{}, roots...)
	for len(queue) > 0 {
		oid := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if !IsOID(oid) || oid == ZeroOID {
			continue
		}
		obj, fresh, err := w.visit(oid)
		if err != nil {
			return nil, err
		}
		if !fresh {
			continue
		}
		switch obj.Type {
		case TypeCommit:
			commit, err := ParseCommit(obj.Data)
			if err != nil {
				return nil, err
			}
			queue = append(queue, commit.Tree)
			if !stopAt[oid] {
				queue = append(queue, commit.Parents...)
			}
		case TypeTag:
			tag, err := ParseTag(obj.Data)
			if err != nil {
				return nil, err
			}
			queue = append(queue, tag.Object)
		case TypeTree:
			entries, err := ParseTree(obj.Data)
			if err != nil {
				return nil, err
			}
			for _, e := range entries {
				if e.Mode == "160000" {
					continue // a gitlink names a commit the repo does not hold
				}
				queue = append(queue, e.OID)
			}
		}
	}
	return &WalkResult{Objects: w.out}, nil
}

// WalkDepth collects the objects reachable from roots within depth commit
// generations (depth 1 is the tips alone). The boundary commits — the
// deepest ones included — are returned in Shallow, in deterministic order.
func (r *Repo) WalkDepth(roots []string, depth int) (*WalkResult, error) {
	if depth < 1 {
		return nil, fmt.Errorf("git: WalkDepth needs depth >= 1")
	}
	w := &walkState{repo: r, seen: map[string]bool{}, out: map[string]*Object{}}
	type item struct {
		oid string
		gen int
	}
	queue := []item{}
	for _, root := range roots {
		if IsOID(root) && root != ZeroOID {
			queue = append(queue, item{oid: root, gen: 0})
		}
	}
	shallowSet := map[string]bool{}
	for len(queue) > 0 {
		it := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		obj, fresh, err := w.visit(it.oid)
		if err != nil {
			return nil, err
		}
		if !fresh {
			continue
		}
		switch obj.Type {
		case TypeCommit:
			commit, err := ParseCommit(obj.Data)
			if err != nil {
				return nil, err
			}
			queue = append(queue, item{oid: commit.Tree, gen: it.gen})
			if it.gen+1 < depth {
				for _, p := range commit.Parents {
					queue = append(queue, item{oid: p, gen: it.gen + 1})
				}
			} else {
				// This commit is at the boundary: its parents are not
				// followed, and it is a shallow commit.
				shallowSet[it.oid] = true
			}
		case TypeTag:
			tag, err := ParseTag(obj.Data)
			if err != nil {
				return nil, err
			}
			queue = append(queue, item{oid: tag.Object, gen: it.gen})
		case TypeTree:
			entries, err := ParseTree(obj.Data)
			if err != nil {
				return nil, err
			}
			for _, e := range entries {
				if e.Mode == "160000" {
					continue
				}
				queue = append(queue, item{oid: e.OID, gen: it.gen})
			}
		}
	}
	shallow := make([]string, 0, len(shallowSet))
	for oid := range shallowSet {
		shallow = append(shallow, oid)
	}
	sort.Strings(shallow)
	return &WalkResult{Objects: w.out, Shallow: shallow}, nil
}

// Generation returns the distance (in parent links) from the nearest root
// to oid, or -1 when oid is not reachable from the roots. It is used to
// resolve a relative deepen: the client's current depth is the generation
// of its shallow boundaries plus one.
func (r *Repo) Generation(roots []string, oid string) (int, error) {
	if !IsOID(oid) {
		return -1, fmt.Errorf("git: Generation needs an object id")
	}
	type item struct {
		oid string
		gen int
	}
	seen := map[string]bool{}
	queue := []item{}
	for _, root := range roots {
		if IsOID(root) && root != ZeroOID {
			queue = append(queue, item{oid: root, gen: 0})
		}
	}
	for len(queue) > 0 {
		it := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if seen[it.oid] {
			continue
		}
		seen[it.oid] = true
		if it.oid == oid {
			return it.gen, nil
		}
		obj, err := r.ReadObject(it.oid)
		if err != nil {
			if errors.Is(err, ErrObjectNotFound) {
				continue
			}
			return -1, err
		}
		if obj.Type != TypeCommit {
			continue
		}
		commit, err := ParseCommit(obj.Data)
		if err != nil {
			return -1, err
		}
		for _, p := range commit.Parents {
			queue = append(queue, item{oid: p, gen: it.gen + 1})
		}
	}
	return -1, nil
}
func (r *Repo) PeelTag(oid string) (string, error) {
	for {
		obj, err := r.ReadObject(oid)
		if err != nil {
			return "", err
		}
		if obj.Type != TypeTag {
			return oid, nil
		}
		tag, err := ParseTag(obj.Data)
		if err != nil {
			return "", err
		}
		oid = tag.Object
	}
}
