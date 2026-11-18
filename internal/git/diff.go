// Package git implements the git object model and the smart-HTTP protocol
// in pure Go (see pktline.go). This file holds the tree diff and the
// three-way merge the pull-request API needs: the files a pull request
// changes, whether it can merge cleanly, and the squash merge itself.
package git

import (
	"bytes"
	"fmt"
	"sort"
)

// FileChange is one file a pull request changes, as the REST v3 API serves
// it on GET /pulls/{n}/files.
type FileChange struct {
	Filename  string
	Status    string // "added", "removed" or "modified"
	Additions int
	Deletions int
	Changes   int
}

// commitFiles returns the flat file map of a commit's tree.
func (r *Repo) commitFiles(commitSHA string) (map[string]FileEntry, error) {
	obj, err := r.ReadObject(commitSHA)
	if err != nil {
		return nil, err
	}
	commit, err := ParseCommit(obj.Data)
	if err != nil {
		return nil, err
	}
	return r.TreeFiles(commit.Tree)
}

// lineDiff counts the added and removed lines between two texts with a
// longest-common-subsequence diff. The suite's files are small (a few KiB),
// so the quadratic table is cheap; binary content is counted by lines too.
func lineDiff(oldText, newText []byte) (additions, deletions int) {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)
	if len(oldLines) == 0 && len(newLines) == 0 {
		return 0, 0
	}
	// LCS table over line numbers.
	prev := make([]int, len(newLines)+1)
	cur := make([]int, len(newLines)+1)
	for i := 1; i <= len(oldLines); i++ {
		for j := 1; j <= len(newLines); j++ {
			if oldLines[i-1] == newLines[j-1] {
				cur[j] = prev[j-1] + 1
			} else if prev[j] >= cur[j-1] {
				cur[j] = prev[j]
			} else {
				cur[j] = cur[j-1]
			}
		}
		prev, cur = cur, prev
	}
	lcs := prev[len(newLines)]
	return len(newLines) - lcs, len(oldLines) - lcs
}

func splitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	lines := bytes.Split(data, []byte("\n"))
	// A trailing newline does not start a new line.
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = string(l)
	}
	return out
}

// DiffTrees diffs two commits' trees and returns the changed files, sorted
// by path.
func (r *Repo) DiffTrees(baseSHA, headSHA string) ([]FileChange, error) {
	baseFiles, err := r.commitFiles(baseSHA)
	if err != nil {
		return nil, err
	}
	headFiles, err := r.commitFiles(headSHA)
	if err != nil {
		return nil, err
	}
	paths := map[string]bool{}
	for p := range baseFiles {
		paths[p] = true
	}
	for p := range headFiles {
		paths[p] = true
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	out := []FileChange{}
	for _, p := range sorted {
		base, inBase := baseFiles[p]
		head, inHead := headFiles[p]
		if inBase && inHead && base.OID == head.OID {
			continue
		}
		change := FileChange{Filename: p}
		switch {
		case !inBase:
			change.Status = "added"
			blob, err := r.ReadObject(head.OID)
			if err != nil {
				return nil, err
			}
			change.Additions, change.Deletions = lineDiff(nil, blob.Data)
		case !inHead:
			change.Status = "removed"
			blob, err := r.ReadObject(base.OID)
			if err != nil {
				return nil, err
			}
			change.Additions, change.Deletions = lineDiff(blob.Data, nil)
		default:
			change.Status = "modified"
			oldBlob, err := r.ReadObject(base.OID)
			if err != nil {
				return nil, err
			}
			newBlob, err := r.ReadObject(head.OID)
			if err != nil {
				return nil, err
			}
			change.Additions, change.Deletions = lineDiff(oldBlob.Data, newBlob.Data)
		}
		change.Changes = change.Additions + change.Deletions
		out = append(out, change)
	}
	return out, nil
}

// MergeBase returns the nearest common ancestor of two commits: the first
// commit reachable from b that is also reachable from a.
func (r *Repo) MergeBase(a, b string) (string, error) {
	ancestors, err := r.ancestorSet(a)
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	queue := []string{b}
	for len(queue) > 0 {
		oid := queue[0]
		queue = queue[1:]
		if seen[oid] {
			continue
		}
		seen[oid] = true
		if ancestors[oid] {
			return oid, nil
		}
		obj, err := r.ReadObject(oid)
		if err != nil {
			continue
		}
		if obj.Type != TypeCommit {
			continue
		}
		commit, err := ParseCommit(obj.Data)
		if err != nil {
			return "", err
		}
		queue = append(queue, commit.Parents...)
	}
	return "", fmt.Errorf("git: no merge base for %s and %s", a, b)
}

func (r *Repo) ancestorSet(root string) (map[string]bool, error) {
	seen := map[string]bool{}
	queue := []string{root}
	for len(queue) > 0 {
		oid := queue[0]
		queue = queue[1:]
		if seen[oid] {
			continue
		}
		seen[oid] = true
		obj, err := r.ReadObject(oid)
		if err != nil {
			continue
		}
		if obj.Type != TypeCommit {
			continue
		}
		commit, err := ParseCommit(obj.Data)
		if err != nil {
			return nil, err
		}
		queue = append(queue, commit.Parents...)
	}
	return seen, nil
}

// IsMergeable reports whether the head commit's changes apply cleanly onto
// the base commit: for every path the head changed since the merge-base,
// the base must not have changed it differently in the meantime.
func (r *Repo) IsMergeable(baseSHA, headSHA string) (bool, error) {
	if baseSHA == headSHA {
		return true, nil
	}
	mergeBase, err := r.MergeBase(baseSHA, headSHA)
	if err != nil {
		// No common history: the head's changes cannot be related to the
		// base, so the merge is not clean.
		return false, nil
	}
	baseFiles, err := r.commitFiles(baseSHA)
	if err != nil {
		return false, err
	}
	headFiles, err := r.commitFiles(headSHA)
	if err != nil {
		return false, err
	}
	mergeFiles, err := r.commitFiles(mergeBase)
	if err != nil {
		return false, err
	}
	paths := map[string]bool{}
	for p := range mergeFiles {
		paths[p] = true
	}
	for p := range headFiles {
		paths[p] = true
	}
	for p := range paths {
		mergeEntry, inMerge := mergeFiles[p]
		headEntry, inHead := headFiles[p]
		headChanged := !inMerge || !inHead || headEntry.OID != mergeEntry.OID
		if !headChanged {
			continue
		}
		baseEntry, inBase := baseFiles[p]
		// The base changed the path only when its version differs from
		// the merge-base's: added by the base, removed by the base, or
		// modified to a different blob. A path the merge-base never had
		// and the base does not have either was not touched by the base
		// (it is a head-side addition, not a conflict).
		baseChanged := (inBase != inMerge) || (inBase && inMerge && baseEntry.OID != mergeEntry.OID)
		if !baseChanged {
			continue
		}
		// Both sides changed the path; clean only when they agree.
		if inBase == inHead && (!inBase || baseEntry.OID == headEntry.OID) {
			continue
		}
		return false, nil
	}
	return true, nil
}

// SquashMerge applies the head commit's changes since the merge-base onto
// the base commit as one squash commit on baseRef, and returns the merge
// commit's id. The caller must have checked IsMergeable first.
func (r *Repo) SquashMerge(baseRef, headSHA, baseSHA, message, authorName, authorEmail string) (string, error) {
	mergeBase, err := r.MergeBase(baseSHA, headSHA)
	if err != nil {
		return "", err
	}
	baseFiles, err := r.commitFiles(baseSHA)
	if err != nil {
		return "", err
	}
	headFiles, err := r.commitFiles(headSHA)
	if err != nil {
		return "", err
	}
	mergeFiles, err := r.commitFiles(mergeBase)
	if err != nil {
		return "", err
	}
	merged := map[string]FileEntry{}
	for p, e := range baseFiles {
		merged[p] = e
	}
	paths := map[string]bool{}
	for p := range mergeFiles {
		paths[p] = true
	}
	for p := range headFiles {
		paths[p] = true
	}
	for p := range paths {
		mergeEntry, inMerge := mergeFiles[p]
		headEntry, inHead := headFiles[p]
		headChanged := !inMerge || !inHead || headEntry.OID != mergeEntry.OID
		if !headChanged {
			continue
		}
		if inHead {
			merged[p] = headEntry
		} else {
			delete(merged, p)
		}
	}
	treeSHA, err := r.WriteTreeFromFiles(merged)
	if err != nil {
		return "", err
	}
	identity := commitIdentity(authorName, authorEmail)
	commitSHA, err := r.writeCommit(treeSHA, []string{baseSHA}, identity, identity, message)
	if err != nil {
		return "", err
	}
	if err := r.WriteRef(baseRef, commitSHA); err != nil {
		return "", err
	}
	return commitSHA, nil
}
