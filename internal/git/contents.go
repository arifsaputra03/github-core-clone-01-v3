// Package git implements the git object model and the smart-HTTP protocol
// in pure Go (see pktline.go). This file holds the server-side file and
// commit operations the REST contents API and the pull-request merge need:
// committing a file, deleting a file, reading a path at a ref, and listing
// a branch's commits.
package git

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ErrStaleSHA is returned when a contents write names a blob sha that is
// not the path's current blob (the REST API answers 409).
var ErrStaleSHA = errors.New("git: stale sha")

// ErrPathExists is returned when a contents create names a path that
// already exists on the branch (the REST API answers 422).
var ErrPathExists = errors.New("git: path already exists")

// FileEntry is one file in a flattened tree: the blob it holds and its mode.
type FileEntry struct {
	OID  string
	Mode string
}

// ResolveRef resolves a ref to a commit id. ref may be a full ref
// ("refs/heads/main"), a branch name ("main"), a tag name, or a commit id.
// A tag is peeled to the commit it names.
func (r *Repo) ResolveRef(ref string) (string, error) {
	if IsOID(ref) {
		obj, err := r.ReadObject(ref)
		if err != nil {
			return "", err
		}
		if obj.Type == TypeTag {
			return r.PeelTag(ref)
		}
		if obj.Type != TypeCommit {
			return "", fmt.Errorf("git: %s is not a commit", ref)
		}
		return ref, nil
	}
	names := []string{ref}
	if !strings.HasPrefix(ref, "refs/") {
		names = append(names, "refs/heads/"+ref, "refs/tags/"+ref)
	}
	for _, name := range names {
		oid, err := r.ReadRef(name)
		if err != nil {
			continue
		}
		return r.PeelTag(oid)
	}
	return "", fmt.Errorf("%w: %s", ErrRefNotFound, ref)
}

// BranchTip returns the commit id a branch points at, or ErrRefNotFound.
func (r *Repo) BranchTip(branch string) (string, error) {
	return r.ReadRef("refs/heads/" + branch)
}

// DefaultBranch returns the branch HEAD names ("main"), or "" when HEAD is
// detached.
func (r *Repo) DefaultBranch() string {
	target, err := r.HeadTarget()
	if err != nil || target == "" {
		return ""
	}
	return strings.TrimPrefix(target, "refs/heads/")
}

// TreeFiles flattens a tree into path -> file entries, recursing into
// directories. Gitlinks are skipped: they name commits the repo does not
// hold.
func (r *Repo) TreeFiles(treeSHA string) (map[string]FileEntry, error) {
	out := map[string]FileEntry{}
	if err := r.walkTree(treeSHA, "", out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Repo) walkTree(treeSHA, prefix string, out map[string]FileEntry) error {
	obj, err := r.ReadObject(treeSHA)
	if err != nil {
		return err
	}
	if obj.Type != TypeTree {
		return fmt.Errorf("git: %s is not a tree", treeSHA)
	}
	entries, err := ParseTree(obj.Data)
	if err != nil {
		return err
	}
	for _, e := range entries {
		path := prefix + e.Name
		switch e.Mode {
		case "40000":
			if err := r.walkTree(e.OID, path+"/", out); err != nil {
				return err
			}
		case "160000":
			// A gitlink names a commit the repo does not hold.
		default:
			out[path] = FileEntry{OID: e.OID, Mode: e.Mode}
		}
	}
	return nil
}

// WriteTreeFromFiles builds the trees for a flat file map, bottom-up, and
// returns the root tree's id.
func (r *Repo) WriteTreeFromFiles(files map[string]FileEntry) (string, error) {
	// Group the files by directory, then build each directory's tree from
	// the leaves up.
	type dir struct {
		files map[string]FileEntry
		sub   map[string]*dir
	}
	root := &dir{files: map[string]FileEntry{}, sub: map[string]*dir{}}
	for path, entry := range files {
		parts := strings.Split(path, "/")
		cur := root
		for _, part := range parts[:len(parts)-1] {
			next, ok := cur.sub[part]
			if !ok {
				next = &dir{files: map[string]FileEntry{}, sub: map[string]*dir{}}
				cur.sub[part] = next
			}
			cur = next
		}
		cur.files[parts[len(parts)-1]] = entry
	}
	var build func(d *dir) (string, error)
	build = func(d *dir) (string, error) {
		entries := []TreeEntry{}
		for name, entry := range d.files {
			entries = append(entries, TreeEntry{Mode: entry.Mode, Name: name, OID: entry.OID})
		}
		names := make([]string, 0, len(d.sub))
		for name := range d.sub {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			oid, err := build(d.sub[name])
			if err != nil {
				return "", err
			}
			entries = append(entries, TreeEntry{Mode: "40000", Name: name, OID: oid})
		}
		raw, err := BuildTree(entries)
		if err != nil {
			return "", err
		}
		return r.WriteObject(TypeTree, raw)
	}
	return build(root)
}

// commitIdentity formats a git author/committer line: "Name <email> ts tz".
func commitIdentity(name, email string) string {
	return fmt.Sprintf("%s <%s> %d +0000", name, email, time.Now().Unix())
}

// writeCommit creates a commit object with these parents and returns its id.
func (r *Repo) writeCommit(tree string, parents []string, author, committer, message string) (string, error) {
	raw, err := BuildCommit(tree, parents, author, committer, message)
	if err != nil {
		return "", err
	}
	return r.WriteObject(TypeCommit, raw)
}

// tipCommit returns the tip commit id of a branch and its tree's files.
// When the branch does not exist yet it returns ZeroOID and an empty file
// map, so the caller can create a root commit (or branch from the default
// branch, see CommitFile).
func (r *Repo) tipCommit(branch string) (tip string, files map[string]FileEntry, err error) {
	tip, err = r.BranchTip(branch)
	if err != nil {
		if errors.Is(err, ErrRefNotFound) {
			return ZeroOID, map[string]FileEntry{}, nil
		}
		return "", nil, err
	}
	obj, err := r.ReadObject(tip)
	if err != nil {
		return "", nil, err
	}
	commit, err := ParseCommit(obj.Data)
	if err != nil {
		return "", nil, err
	}
	files, err = r.TreeFiles(commit.Tree)
	if err != nil {
		return "", nil, err
	}
	return tip, files, nil
}

// CommitFile commits one file's content on a branch and returns the new
// commit id and the blob id. expectedSHA, when non-empty, must be the
// path's current blob id or the write is refused with ErrStaleSHA; when it
// is empty and the path already exists the write is refused with
// ErrPathExists. A branch that does not exist yet is created from the
// default branch's tip (or as a root commit when the repository is unborn),
// which is the contents API's branch-creation route.
func (r *Repo) CommitFile(branch, path string, content []byte, message, authorName, authorEmail string) (commitSHA, blobSHA string, created bool, err error) {
	return r.commitFile(branch, path, content, message, authorName, authorEmail, "")
}

// CommitFileWithSHA is CommitFile with an expected current blob sha.
func (r *Repo) CommitFileWithSHA(branch, path string, content []byte, message, authorName, authorEmail, expectedSHA string) (commitSHA, blobSHA string, created bool, err error) {
	return r.commitFile(branch, path, content, message, authorName, authorEmail, expectedSHA)
}

func (r *Repo) commitFile(branch, path string, content []byte, message, authorName, authorEmail, expectedSHA string) (commitSHA, blobSHA string, created bool, err error) {
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return "", "", false, fmt.Errorf("git: bad path %q", path)
	}
	tip, files, err := r.tipCommit(branch)
	if err != nil {
		return "", "", false, err
	}
	// A missing branch starts from the default branch's tip, so a contents
	// write on a new branch lands on top of the default branch.
	if tip == ZeroOID {
		defBranch := r.DefaultBranch()
		if defBranch != "" && defBranch != branch {
			defTip, defFiles, err := r.tipCommit(defBranch)
			if err != nil {
				return "", "", false, err
			}
			tip, files = defTip, defFiles
		}
	}
	current, exists := files[path]
	if expectedSHA != "" {
		if !exists || current.OID != expectedSHA {
			return "", "", false, ErrStaleSHA
		}
	} else if exists {
		return "", "", false, ErrPathExists
	}
	blobSHA, err = r.WriteObject(TypeBlob, content)
	if err != nil {
		return "", "", false, err
	}
	files[path] = FileEntry{OID: blobSHA, Mode: "100644"}
	treeSHA, err := r.WriteTreeFromFiles(files)
	if err != nil {
		return "", "", false, err
	}
	identity := commitIdentity(authorName, authorEmail)
	parents := []string{}
	if tip != ZeroOID {
		parents = []string{tip}
	}
	commitSHA, err = r.writeCommit(treeSHA, parents, identity, identity, message)
	if err != nil {
		return "", "", false, err
	}
	if err := r.WriteRef("refs/heads/"+branch, commitSHA); err != nil {
		return "", "", false, err
	}
	return commitSHA, blobSHA, !exists, nil
}

// DeleteFile removes one file from a branch with a commit and returns the
// new commit id. expectedSHA, when non-empty, must be the path's current
// blob id or the delete is refused with ErrStaleSHA.
func (r *Repo) DeleteFile(branch, path, message, authorName, authorEmail, expectedSHA string) (string, error) {
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return "", fmt.Errorf("git: bad path %q", path)
	}
	tip, files, err := r.tipCommit(branch)
	if err != nil {
		return "", err
	}
	if tip == ZeroOID {
		return "", ErrRefNotFound
	}
	current, exists := files[path]
	if !exists {
		return "", fmt.Errorf("%w: %s", ErrObjectNotFound, path)
	}
	if expectedSHA != "" && current.OID != expectedSHA {
		return "", ErrStaleSHA
	}
	delete(files, path)
	treeSHA, err := r.WriteTreeFromFiles(files)
	if err != nil {
		return "", err
	}
	identity := commitIdentity(authorName, authorEmail)
	commitSHA, err := r.writeCommit(treeSHA, []string{tip}, identity, identity, message)
	if err != nil {
		return "", err
	}
	if err := r.WriteRef("refs/heads/"+branch, commitSHA); err != nil {
		return "", err
	}
	return commitSHA, nil
}

// PathKind is what ReadPathAt found at a path.
type PathKind int

const (
	// PathMissing is a path that does not exist at the ref.
	PathMissing PathKind = iota
	// PathFile is a file (a blob).
	PathFile
	// PathDir is a directory (a tree).
	PathDir
)

// PathEntry is one entry of a directory listing at a ref.
type PathEntry struct {
	Name string
	Path string
	OID  string
	Kind PathKind
}

// ReadPathAt resolves ref to a commit and reads path in its tree. For a
// file it returns the blob's bytes and id; for a directory it returns the
// entries; for a missing path it returns PathMissing. An empty path reads
// the root.
func (r *Repo) ReadPathAt(ref, path string) (kind PathKind, blobSHA string, content []byte, entries []PathEntry, err error) {
	commitSHA, err := r.ResolveRef(ref)
	if err != nil {
		return PathMissing, "", nil, nil, err
	}
	obj, err := r.ReadObject(commitSHA)
	if err != nil {
		return PathMissing, "", nil, nil, err
	}
	commit, err := ParseCommit(obj.Data)
	if err != nil {
		return PathMissing, "", nil, nil, err
	}
	if path == "" {
		entries, err = r.readDir(commit.Tree, "")
		return PathDir, "", nil, entries, err
	}
	parts := strings.Split(path, "/")
	treeSHA := commit.Tree
	for i, part := range parts {
		tobj, err := r.ReadObject(treeSHA)
		if err != nil {
			return PathMissing, "", nil, nil, err
		}
		treeEntries, err := ParseTree(tobj.Data)
		if err != nil {
			return PathMissing, "", nil, nil, err
		}
		var found *TreeEntry
		for j := range treeEntries {
			if treeEntries[j].Name == part {
				found = &treeEntries[j]
				break
			}
		}
		if found == nil {
			return PathMissing, "", nil, nil, nil
		}
		last := i == len(parts)-1
		switch found.Mode {
		case "40000":
			if last {
				entries, err = r.readDir(found.OID, path)
				return PathDir, "", nil, entries, err
			}
			treeSHA = found.OID
		case "160000":
			return PathMissing, "", nil, nil, nil
		default:
			if !last {
				return PathMissing, "", nil, nil, nil
			}
			bobj, err := r.ReadObject(found.OID)
			if err != nil {
				return PathMissing, "", nil, nil, err
			}
			return PathFile, found.OID, bobj.Data, nil, nil
		}
	}
	return PathMissing, "", nil, nil, nil
}

func (r *Repo) readDir(treeSHA, prefix string) ([]PathEntry, error) {
	obj, err := r.ReadObject(treeSHA)
	if err != nil {
		return nil, err
	}
	treeEntries, err := ParseTree(obj.Data)
	if err != nil {
		return nil, err
	}
	out := []PathEntry{}
	for _, e := range treeEntries {
		path := e.Name
		if prefix != "" {
			path = prefix + "/" + e.Name
		}
		kind := PathFile
		if e.Mode == "40000" {
			kind = PathDir
		} else if e.Mode == "160000" {
			continue
		}
		out = append(out, PathEntry{Name: e.Name, Path: path, OID: e.OID, Kind: kind})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// CommitInfo is one commit in a branch's history, newest first.
type CommitInfo struct {
	SHA     string
	Message string
}

// ListCommits walks a ref's history, newest first, following every parent.
// The walk is bounded by limit when it is positive.
func (r *Repo) ListCommits(ref string, limit int) ([]CommitInfo, error) {
	tip, err := r.ResolveRef(ref)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := []CommitInfo{}
	queue := []string{tip}
	for len(queue) > 0 {
		oid := queue[0]
		queue = queue[1:]
		if seen[oid] {
			continue
		}
		seen[oid] = true
		obj, err := r.ReadObject(oid)
		if err != nil {
			if errors.Is(err, ErrObjectNotFound) {
				continue
			}
			return nil, err
		}
		if obj.Type != TypeCommit {
			continue
		}
		commit, err := ParseCommit(obj.Data)
		if err != nil {
			return nil, err
		}
		out = append(out, CommitInfo{SHA: oid, Message: commit.Message})
		if limit > 0 && len(out) >= limit {
			break
		}
		queue = append(queue, commit.Parents...)
	}
	return out, nil
}
