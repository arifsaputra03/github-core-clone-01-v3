// Package service holds the forge's use cases (see service.go). This file
// holds the pull requests and the contents API: the git operations the
// REST layer drives (committing a file, reading a path, merging).
package service

import (
	"errors"
	"fmt"
	"time"

	"forge/internal/domain"
	"forge/internal/git"
	"forge/internal/store"
)

// ErrConflict is returned when a pull request cannot merge cleanly (the
// REST API answers 405).
var ErrConflict = errors.New("merge conflict")

// ErrPullClosed is returned when a merge is attempted on a pull request
// that is already closed or merged.
var ErrPullClosed = errors.New("pull request is closed")

// authorEmail is the commit author identity for commits the API itself
// writes (contents writes and squash merges).
func authorEmail(login string) string { return login + "@example.invalid" }

// --- Contents ---

// PutContentResult is what a contents write returns: the commit it made
// and the blob it wrote.
type PutContentResult struct {
	CommitSHA string
	BlobSHA   string
	Created   bool // true when the path did not exist before
}

// PutContent writes one file on a branch through the contents API. branch
// may be empty (the default branch). expectedSHA, when non-empty, must be
// the path's current blob id.
func (s *Service) PutContent(owner, repo, path string, content []byte, message, branch, expectedSHA string) (*PutContentResult, error) {
	if _, err := s.store.GetRepository(owner, repo); err != nil {
		return nil, err
	}
	bare, err := s.OpenRepo(owner, repo)
	if err != nil {
		return nil, err
	}
	if branch == "" {
		branch, err = s.DefaultBranch(owner, repo)
		if err != nil {
			return nil, err
		}
	}
	var commitSHA, blobSHA string
	var created bool
	if expectedSHA != "" {
		commitSHA, blobSHA, created, err = bare.CommitFileWithSHA(branch, path, content, message, s.viewerLogin, authorEmail(s.viewerLogin), expectedSHA)
	} else {
		commitSHA, blobSHA, created, err = bare.CommitFile(branch, path, content, message, s.viewerLogin, authorEmail(s.viewerLogin))
	}
	if err != nil {
		return nil, err
	}
	return &PutContentResult{CommitSHA: commitSHA, BlobSHA: blobSHA, Created: created}, nil
}

// GetContentResult is what a contents read returns.
type GetContentResult struct {
	Kind    git.PathKind
	BlobSHA string
	Content []byte
	Entries []git.PathEntry
}

// GetContent reads a path at a ref (a branch name, a full ref or a commit
// id; empty means the default branch).
func (s *Service) GetContent(owner, repo, path, ref string) (*GetContentResult, error) {
	if _, err := s.store.GetRepository(owner, repo); err != nil {
		return nil, err
	}
	bare, err := s.OpenRepo(owner, repo)
	if err != nil {
		return nil, err
	}
	if ref == "" {
		ref, err = s.DefaultBranch(owner, repo)
		if err != nil {
			return nil, err
		}
	}
	kind, blobSHA, content, entries, err := bare.ReadPathAt(ref, path)
	if err != nil {
		return nil, err
	}
	return &GetContentResult{Kind: kind, BlobSHA: blobSHA, Content: content, Entries: entries}, nil
}

// DeleteContent removes one file from a branch with a commit.
func (s *Service) DeleteContent(owner, repo, path, message, branch, expectedSHA string) (string, error) {
	if _, err := s.store.GetRepository(owner, repo); err != nil {
		return "", err
	}
	bare, err := s.OpenRepo(owner, repo)
	if err != nil {
		return "", err
	}
	if branch == "" {
		branch, err = s.DefaultBranch(owner, repo)
		if err != nil {
			return "", err
		}
	}
	return bare.DeleteFile(branch, path, message, s.viewerLogin, authorEmail(s.viewerLogin), expectedSHA)
}

// --- Pull requests ---

// CreatePullInput is what CreatePull needs.
type CreatePullInput struct {
	Title  string
	Body   string
	Head   string
	Base   string
	Author string
}

// CreatePull opens a pull request from head into base. Both branches must
// exist, and an open pull request from the same head into the same base is
// refused with ErrAlreadyExists.
func (s *Service) CreatePull(owner, repo string, in CreatePullInput) (*domain.PullRequest, error) {
	if _, err := s.store.GetRepository(owner, repo); err != nil {
		return nil, err
	}
	bare, err := s.OpenRepo(owner, repo)
	if err != nil {
		return nil, err
	}
	headSHA, err := bare.BranchTip(in.Head)
	if err != nil {
		return nil, fmt.Errorf("%w: head branch %s", store.ErrNotFound, in.Head)
	}
	baseSHA, err := bare.BranchTip(in.Base)
	if err != nil {
		return nil, fmt.Errorf("%w: base branch %s", store.ErrNotFound, in.Base)
	}
	existing, err := s.store.ListPullRequests(owner, repo)
	if err != nil {
		return nil, err
	}
	for _, pr := range existing {
		if pr.Head == in.Head && pr.Base == in.Base && pr.State == domain.PullOpen {
			return nil, fmt.Errorf("%w: an open pull request already exists for %s", store.ErrAlreadyExists, in.Head)
		}
	}
	mergeable, err := bare.IsMergeable(baseSHA, headSHA)
	if err != nil {
		return nil, err
	}
	pr := &domain.PullRequest{
		RepoOwner: owner,
		RepoName:  repo,
		Title:     in.Title,
		Body:      in.Body,
		State:     domain.PullOpen,
		Head:      in.Head,
		Base:      in.Base,
		HeadSHA:   headSHA,
		BaseSHA:   baseSHA,
		Author:    in.Author,
		Mergeable: mergeable,
	}
	if err := s.store.CreatePullRequest(pr); err != nil {
		return nil, err
	}
	return pr, nil
}

// GetPull returns one pull request by number.
func (s *Service) GetPull(owner, repo string, number int) (*domain.PullRequest, error) {
	return s.store.GetPullRequest(owner, repo, number)
}

// PullByID returns a pull request by its global numeric id.
func (s *Service) PullByID(id int64) (*domain.PullRequest, error) {
	return s.store.GetPullRequestByID(id)
}

// ListPulls returns a repository's pull requests. state is "open",
// "closed" or "all"; empty means "open".
func (s *Service) ListPulls(owner, repo, state string) ([]*domain.PullRequest, error) {
	all, err := s.store.ListPullRequests(owner, repo)
	if err != nil {
		return nil, err
	}
	if state == "" {
		state = "open"
	}
	out := []*domain.PullRequest{}
	for _, pr := range all {
		switch state {
		case "all":
		case "open":
			if pr.State != domain.PullOpen {
				continue
			}
		case "closed":
			if pr.State == domain.PullOpen {
				continue
			}
		default:
			if pr.State != domain.PullOpen {
				continue
			}
		}
		out = append(out, pr)
	}
	return out, nil
}

// currentBaseSHA resolves a pull request's base branch to its current tip,
// falling back to the stored BaseSHA when the branch is gone.
func (s *Service) currentBaseSHA(pr *domain.PullRequest) string {
	bare, err := s.OpenRepo(pr.RepoOwner, pr.RepoName)
	if err != nil {
		return pr.BaseSHA
	}
	sha, err := bare.BranchTip(pr.Base)
	if err != nil {
		return pr.BaseSHA
	}
	return sha
}

// currentHeadSHA resolves a pull request's head branch to its current tip,
// falling back to the stored HeadSHA when the branch is gone.
func (s *Service) currentHeadSHA(pr *domain.PullRequest) string {
	bare, err := s.OpenRepo(pr.RepoOwner, pr.RepoName)
	if err != nil {
		return pr.HeadSHA
	}
	sha, err := bare.BranchTip(pr.Head)
	if err != nil {
		return pr.HeadSHA
	}
	return sha
}

// BaseSHAFor returns the pull request's base branch tip as the REST view
// renders it.
func (s *Service) BaseSHAFor(pr *domain.PullRequest) string {
	return s.currentBaseSHA(pr)
}

// HeadSHAFor returns the pull request's head branch tip as the REST view
// renders it.
func (s *Service) HeadSHAFor(pr *domain.PullRequest) string {
	return s.currentHeadSHA(pr)
}

// ListPullFiles returns the files a pull request changes, diffed between
// its base (at creation) and its head's current tip.
func (s *Service) ListPullFiles(owner, repo string, number int) ([]git.FileChange, error) {
	pr, err := s.store.GetPullRequest(owner, repo, number)
	if err != nil {
		return nil, err
	}
	bare, err := s.OpenRepo(owner, repo)
	if err != nil {
		return nil, err
	}
	return bare.DiffTrees(pr.BaseSHA, s.currentHeadSHA(pr))
}

// MergeableFor reports whether a pull request can merge cleanly right now:
// "MERGEABLE", "CONFLICTING" or "UNKNOWN" (when the branches cannot be
// resolved).
func (s *Service) MergeableFor(owner, repo string, number int) string {
	pr, err := s.store.GetPullRequest(owner, repo, number)
	if err != nil {
		return "UNKNOWN"
	}
	bare, err := s.OpenRepo(owner, repo)
	if err != nil {
		return "UNKNOWN"
	}
	ok, err := bare.IsMergeable(s.currentBaseSHA(pr), s.currentHeadSHA(pr))
	if err != nil {
		return "UNKNOWN"
	}
	if ok {
		return "MERGEABLE"
	}
	return "CONFLICTING"
}

// MergePull squash-merges a pull request onto its base branch and returns
// the merge commit's id. A conflicted pull request is refused with
// ErrConflict; an already-closed one with ErrPullClosed.
func (s *Service) MergePull(owner, repo string, number int, method string) (string, error) {
	pr, err := s.store.GetPullRequest(owner, repo, number)
	if err != nil {
		return "", err
	}
	if pr.State != domain.PullOpen {
		return "", ErrPullClosed
	}
	bare, err := s.OpenRepo(owner, repo)
	if err != nil {
		return "", err
	}
	headSHA := s.currentHeadSHA(pr)
	baseSHA := s.currentBaseSHA(pr)
	ok, err := bare.IsMergeable(baseSHA, headSHA)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrConflict
	}
	message := "Merge pull request #" + itoa(number) + " from " + pr.Head
	mergeSHA, err := bare.SquashMerge("refs/heads/"+pr.Base, headSHA, baseSHA, message, s.viewerLogin, authorEmail(s.viewerLogin))
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	pr.State = domain.PullClosed
	pr.Merged = true
	pr.MergeCommit = mergeSHA
	pr.MergedAt = &now
	pr.ClosedAt = &now
	pr.Mergeable = false
	if err := s.store.UpdatePullRequest(pr); err != nil {
		return "", err
	}
	return mergeSHA, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
