// Package store defines the forge's persistence contract. Every store — the
// embedded file store the grading pod uses, and the PostgreSQL store a
// self-hosted deployment uses — implements this interface, so the service
// layer never knows which one is behind it.
package store

import (
	"errors"

	"forge/internal/domain"
)

// ErrNotFound is returned when the named record does not exist.
var ErrNotFound = errors.New("store: not found")

// ErrAlreadyExists is returned when a create names a record that already exists.
var ErrAlreadyExists = errors.New("store: already exists")

// Store is the forge's persistence contract. IDs and per-repository numbers
// are allocated by the store itself inside the Create calls, so callers never
// invent them. All methods are safe for concurrent use.
type Store interface {
	// Users.
	CreateUser(u *domain.User) error
	GetUser(login string) (*domain.User, error)

	// Repositories. A repository is addressed by owner login and name.
	CreateRepository(r *domain.Repository) error
	GetRepository(owner, name string) (*domain.Repository, error)
	// GetRepositoryByID looks a repository up by its numeric id (the
	// GraphQL databaseId).
	GetRepositoryByID(id int64) (*domain.Repository, error)
	ListRepositories(owner string) ([]*domain.Repository, error)
	UpdateRepository(r *domain.Repository) error
	DeleteRepository(owner, name string) error

	// Labels. A label must exist before an issue may carry it; a label
	// carries its colour, defaulted when the caller does not name one.
	CreateLabel(owner, repo string, label *domain.Label) error
	ListLabels(owner, repo string) ([]*domain.Label, error)
	HasLabel(owner, repo, name string) (bool, error)
	UpdateLabel(owner, repo, oldName string, label *domain.Label) error
	DeleteLabel(owner, repo, name string) error

	// Issues. Numbers are per-repository, starting at 1.
	CreateIssue(i *domain.Issue) error
	GetIssue(owner, repo string, number int) (*domain.Issue, error)
	// GetIssueByID looks an issue up by its global numeric id.
	GetIssueByID(id int64) (*domain.Issue, error)
	ListIssues(owner, repo string) ([]*domain.Issue, error)
	UpdateIssue(i *domain.Issue) error

	// Comments on issues. Ids are global and monotonically increasing.
	CreateComment(c *domain.Comment) error
	ListComments(owner, repo string, issueNumber int) ([]*domain.Comment, error)
	// GetCommentByID looks a comment up by its global numeric id.
	GetCommentByID(id int64) (*domain.Comment, error)

	// Pull requests. Numbers are per-repository, starting at 1, and share the
	// issue numbering space only in the sense that both start at 1; a pull
	// request's number is independent of any issue's.
	CreatePullRequest(p *domain.PullRequest) error
	GetPullRequest(owner, repo string, number int) (*domain.PullRequest, error)
	// GetPullRequestByID looks a pull request up by its global numeric id.
	GetPullRequestByID(id int64) (*domain.PullRequest, error)
	ListPullRequests(owner, repo string) ([]*domain.PullRequest, error)
	UpdatePullRequest(p *domain.PullRequest) error

	// Milestones. Numbers are per-repository, starting at 1.
	CreateMilestone(m *domain.Milestone) error
	GetMilestone(owner, repo string, number int) (*domain.Milestone, error)
	ListMilestones(owner, repo string) ([]*domain.Milestone, error)
	UpdateMilestone(m *domain.Milestone) error
	DeleteMilestone(owner, repo string, number int) error

	// Close releases any resources the store holds, flushing any pending
	// write first.
	Close() error
}
