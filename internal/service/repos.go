// Package service holds the forge's use cases (see service.go). This file
// holds the repository-metadata operations the REST, GraphQL and MCP layers
// drive: users, repository listing and updates, branches, commits and refs.
package service

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"forge/internal/bare"
	"forge/internal/domain"
	"forge/internal/git"
	"forge/internal/store"
)

// GetUser returns a user by login. The viewer is created on first use, so
// the token owner's record always exists.
func (s *Service) GetUser(login string) (*domain.User, error) {
	u, err := s.store.GetUser(login)
	if err == nil {
		return u, nil
	}
	if !errors.Is(err, store.ErrNotFound) || login != s.viewerLogin {
		return nil, err
	}
	if err := s.ensureViewer(); err != nil {
		return nil, err
	}
	return s.store.GetUser(login)
}

// RepositoryByID returns a repository by its numeric id.
func (s *Service) RepositoryByID(id int64) (*domain.Repository, error) {
	return s.store.GetRepositoryByID(id)
}

// ListRepositories returns every repository owned by a login, by name.
func (s *Service) ListRepositories(owner string) ([]*domain.Repository, error) {
	return s.store.ListRepositories(owner)
}

// UpdateRepository changes a repository's description (and, when given, its
// privacy). The name and owner never change through this call.
func (s *Service) UpdateRepository(owner, name, description string, private *bool) (*domain.Repository, error) {
	r, err := s.store.GetRepository(owner, name)
	if err != nil {
		return nil, err
	}
	r.Description = description
	if private != nil {
		r.Private = *private
	}
	if err := s.store.UpdateRepository(r); err != nil {
		return nil, err
	}
	return r, nil
}

// DeleteRepository removes a repository's metadata and its bare repository
// on disk.
func (s *Service) DeleteRepository(owner, name string) error {
	if _, err := s.store.GetRepository(owner, name); err != nil {
		return err
	}
	if err := s.store.DeleteRepository(owner, name); err != nil {
		return err
	}
	// The bare layout is removed best-effort: the metadata is the record
	// of truth, and a leftover directory must not resurrect the repo.
	_ = bare.Remove(s.dataDir, owner, name)
	return nil
}

// DefaultBranch returns the branch HEAD names for a repository, falling
// back to the stored default when the bare repository is unborn.
func (s *Service) DefaultBranch(owner, name string) (string, error) {
	r, err := s.store.GetRepository(owner, name)
	if err != nil {
		return "", err
	}
	bare, err := s.OpenRepo(owner, name)
	if err != nil {
		return r.DefaultBranch, nil
	}
	if branch := bare.DefaultBranch(); branch != "" {
		return branch, nil
	}
	return r.DefaultBranch, nil
}

// Branch is one branch with its tip commit.
type Branch struct {
	Name string
	SHA  string
}

// ListBranches returns every branch of a repository, sorted by name, with
// the default branch included.
func (s *Service) ListBranches(owner, name string) ([]Branch, error) {
	if _, err := s.store.GetRepository(owner, name); err != nil {
		return nil, err
	}
	bare, err := s.OpenRepo(owner, name)
	if err != nil {
		return nil, err
	}
	refs, err := bare.ListRefs()
	if err != nil {
		return nil, err
	}
	out := []Branch{}
	for _, ref := range refs {
		if !strings.HasPrefix(ref.Name, "refs/heads/") {
			continue
		}
		out = append(out, Branch{Name: strings.TrimPrefix(ref.Name, "refs/heads/"), SHA: ref.OID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// GetBranch returns one branch's tip commit.
func (s *Service) GetBranch(owner, name, branch string) (Branch, error) {
	if _, err := s.store.GetRepository(owner, name); err != nil {
		return Branch{}, err
	}
	bare, err := s.OpenRepo(owner, name)
	if err != nil {
		return Branch{}, err
	}
	sha, err := bare.BranchTip(branch)
	if err != nil {
		return Branch{}, err
	}
	return Branch{Name: branch, SHA: sha}, nil
}

// ListCommits returns a branch's (or the default branch's) history, newest
// first. ref may be a branch name, a full ref or a commit id; an empty ref
// means the default branch.
func (s *Service) ListCommits(owner, name, ref string) ([]git.CommitInfo, error) {
	if _, err := s.store.GetRepository(owner, name); err != nil {
		return nil, err
	}
	bare, err := s.OpenRepo(owner, name)
	if err != nil {
		return nil, err
	}
	if ref == "" {
		branch, err := s.DefaultBranch(owner, name)
		if err != nil {
			return nil, err
		}
		ref = branch
	}
	return bare.ListCommits(ref, 0)
}

// CreateRef creates a ref (a branch, or any ref under refs/) at an existing
// object. A ref that already exists is ErrAlreadyExists.
func (s *Service) CreateRef(owner, name, ref, sha string) (string, error) {
	if _, err := s.store.GetRepository(owner, name); err != nil {
		return "", err
	}
	if !strings.HasPrefix(ref, "refs/") {
		return "", fmt.Errorf("ref %q must start with refs/", ref)
	}
	if !git.IsOID(sha) {
		return "", fmt.Errorf("ref target %q is not an object id", sha)
	}
	bare, err := s.OpenRepo(owner, name)
	if err != nil {
		return "", err
	}
	if !bare.HasObject(sha) {
		return "", fmt.Errorf("%w: object %s", store.ErrNotFound, sha)
	}
	if _, err := bare.ReadRef(ref); err == nil {
		return "", fmt.Errorf("%w: ref %s", store.ErrAlreadyExists, ref)
	} else if !errors.Is(err, git.ErrRefNotFound) {
		return "", err
	}
	if err := bare.WriteRef(ref, sha); err != nil {
		return "", err
	}
	return sha, nil
}

// GetRef returns the object id a full ref name points at.
func (s *Service) GetRef(owner, name, ref string) (string, error) {
	if _, err := s.store.GetRepository(owner, name); err != nil {
		return "", err
	}
	bare, err := s.OpenRepo(owner, name)
	if err != nil {
		return "", err
	}
	return bare.ReadRef(ref)
}
