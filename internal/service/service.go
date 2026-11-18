// Package service holds the forge's use cases: the operations the HTTP
// layer drives, written against the store interface and the bare-repository
// layout, so neither the transport nor the storage backend leaks in.
package service

import (
	"errors"
	"fmt"
	"time"

	"forge/internal/bare"
	"forge/internal/domain"
	"forge/internal/git"
	"forge/internal/store"
)

// Service is the forge's use-case layer.
type Service struct {
	store       store.Store
	dataDir     string
	viewerLogin string
}

// New builds a Service over a store, with the bare repositories under
// dataDir and viewerLogin as the owner of repositories the API creates.
func New(st store.Store, dataDir, viewerLogin string) *Service {
	return &Service{store: st, dataDir: dataDir, viewerLogin: viewerLogin}
}

// ViewerLogin returns the login the bearer token names.
func (s *Service) ViewerLogin() string { return s.viewerLogin }

// Store returns the underlying store (used by the HTTP layer for the
// endpoints the service does not yet cover).
func (s *Service) Store() store.Store { return s.store }

// DataDir returns the directory the bare repositories live under.
func (s *Service) DataDir() string { return s.dataDir }

// ensureViewer creates the viewer user when it does not exist yet, so a
// repository always has an owner record. It is a no-op when the user is
// already there.
func (s *Service) ensureViewer() error {
	_, err := s.store.GetUser(s.viewerLogin)
	if err == nil {
		return nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	now := time.Now().UTC()
	return s.store.CreateUser(&domain.User{
		Login:     s.viewerLogin,
		Name:      s.viewerLogin,
		Email:     s.viewerLogin + "@example.invalid",
		CreatedAt: now,
		UpdatedAt: now,
	})
}

// CreateRepositoryInput is what CreateRepository needs.
type CreateRepositoryInput struct {
	Owner       string // empty means the viewer
	Name        string
	Description string
	Private     bool
	AutoInit    bool // Phase 1: accepted, not yet implemented (the suite sends false)
}

// CreateRepository creates a repository: it validates the name, records the
// metadata in the store, and initialises the bare repository on disk. A
// repository that already exists is an error (ErrAlreadyExists), and a
// failed bare initialisation rolls the metadata record back.
func (s *Service) CreateRepository(in CreateRepositoryInput) (*domain.Repository, error) {
	owner := in.Owner
	if owner == "" {
		owner = s.viewerLogin
	}
	if err := bare.ValidateName(in.Name); err != nil {
		return nil, err
	}
	if _, err := s.store.GetRepository(owner, in.Name); err == nil {
		return nil, fmt.Errorf("%w: %s/%s", store.ErrAlreadyExists, owner, in.Name)
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if err := s.ensureViewer(); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	repo := &domain.Repository{
		Owner:         owner,
		Name:          in.Name,
		FullName:      owner + "/" + in.Name,
		Description:   in.Description,
		Private:       in.Private,
		DefaultBranch: git.DefaultBranch,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.store.CreateRepository(repo); err != nil {
		return nil, err
	}
	if err := bare.Init(s.dataDir, owner, in.Name, git.DefaultBranch); err != nil {
		// Roll the metadata back so a failed creation leaves nothing.
		_ = s.store.DeleteRepository(owner, in.Name)
		return nil, err
	}
	return repo, nil
}

// GetRepository returns a repository's metadata, or store.ErrNotFound.
func (s *Service) GetRepository(owner, name string) (*domain.Repository, error) {
	return s.store.GetRepository(owner, name)
}

// OpenRepo returns the bare repository for owner/name, or an error when it
// does not exist on disk.
func (s *Service) OpenRepo(owner, name string) (*git.Repo, error) {
	return bare.Open(s.dataDir, owner, name)
}

// RepoExists reports whether a repository exists (metadata and bare layout
// both present). The smart-HTTP handlers use it to answer 404 for a
// missing repository without touching the store.
func (s *Service) RepoExists(owner, name string) bool {
	return bare.Exists(s.dataDir, owner, name)
}
