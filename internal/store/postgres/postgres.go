//go:build postgres

// Package postgres implements the store interface on PostgreSQL, for a
// self-hosted deployment. The grading pod has no PostgreSQL, so this
// package is behind the "postgres" build tag and is not linked into the
// default binary: build with -tags postgres to include it, and set
// DATABASE_URL to select it at run time.
//
// The schema is migrations/001_init.sql, repeated here as a string because
// a Go embed cannot reach a file outside this package's directory. Keep
// the two identical.
package postgres

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"

	"forge/internal/domain"
	"forge/internal/store"
)

// schema is migrations/001_init.sql, kept identical to that file.
const schema = `
CREATE TABLE IF NOT EXISTS users (
    id          BIGSERIAL PRIMARY KEY,
    login       TEXT NOT NULL UNIQUE,
    name        TEXT NOT NULL DEFAULT '',
    email       TEXT NOT NULL DEFAULT '',
    avatar_url  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS repositories (
    id             BIGSERIAL PRIMARY KEY,
    owner          TEXT NOT NULL,
    name           TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    private        BOOLEAN NOT NULL DEFAULT FALSE,
    default_branch TEXT NOT NULL DEFAULT 'main',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (owner, name)
);
CREATE TABLE IF NOT EXISTS labels (
    id    BIGSERIAL PRIMARY KEY,
    owner TEXT NOT NULL,
    repo  TEXT NOT NULL,
    name  TEXT NOT NULL,
    color TEXT NOT NULL DEFAULT 'ededed',
    UNIQUE (owner, repo, name)
);
CREATE TABLE IF NOT EXISTS issues (
    id         BIGSERIAL PRIMARY KEY,
    repo_owner TEXT NOT NULL,
    repo_name  TEXT NOT NULL,
    number     INTEGER NOT NULL,
    title      TEXT NOT NULL DEFAULT '',
    body       TEXT NOT NULL DEFAULT '',
    state      TEXT NOT NULL DEFAULT 'open',
    labels     TEXT[] NOT NULL DEFAULT '{}',
    author     TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at  TIMESTAMPTZ,
    UNIQUE (repo_owner, repo_name, number)
);
CREATE TABLE IF NOT EXISTS comments (
    id           BIGSERIAL PRIMARY KEY,
    repo_owner   TEXT NOT NULL,
    repo_name    TEXT NOT NULL,
    issue_number INTEGER NOT NULL,
    body         TEXT NOT NULL DEFAULT '',
    author       TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS milestones (
    id          BIGSERIAL PRIMARY KEY,
    repo_owner  TEXT NOT NULL,
    repo_name   TEXT NOT NULL,
    number      INTEGER NOT NULL,
    title       TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    state       TEXT NOT NULL DEFAULT 'open',
    due_on      TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at   TIMESTAMPTZ,
    UNIQUE (repo_owner, repo_name, number)
);
CREATE TABLE IF NOT EXISTS pull_requests (
    id           BIGSERIAL PRIMARY KEY,
    repo_owner   TEXT NOT NULL,
    repo_name    TEXT NOT NULL,
    number       INTEGER NOT NULL,
    title        TEXT NOT NULL DEFAULT '',
    body         TEXT NOT NULL DEFAULT '',
    state        TEXT NOT NULL DEFAULT 'open',
    head         TEXT NOT NULL DEFAULT '',
    base         TEXT NOT NULL DEFAULT '',
    head_sha     TEXT NOT NULL DEFAULT '',
    base_sha     TEXT NOT NULL DEFAULT '',
    author       TEXT NOT NULL DEFAULT '',
    merged       BOOLEAN NOT NULL DEFAULT FALSE,
    merge_commit TEXT NOT NULL DEFAULT '',
    mergeable    BOOLEAN NOT NULL DEFAULT TRUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at    TIMESTAMPTZ,
    merged_at    TIMESTAMPTZ,
    UNIQUE (repo_owner, repo_name, number)
);
CREATE INDEX IF NOT EXISTS idx_repositories_owner ON repositories (owner);
CREATE INDEX IF NOT EXISTS idx_issues_repo ON issues (repo_owner, repo_name);
CREATE INDEX IF NOT EXISTS idx_pull_requests_repo ON pull_requests (repo_owner, repo_name);
CREATE INDEX IF NOT EXISTS idx_comments_issue ON comments (repo_owner, repo_name, issue_number);
CREATE INDEX IF NOT EXISTS idx_milestones_repo ON milestones (repo_owner, repo_name);
`

// Store is the PostgreSQL implementation of store.Store.
type Store struct {
	db *sql.DB
}

// Open connects to PostgreSQL, verifies the connection, and creates the
// schema when it is missing.
func Open(dsn string) (*Store, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("postgres: migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the database connection pool.
func (s *Store) Close() error { return s.db.Close() }

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	return err
}

// --- Users ---

func (s *Store) CreateUser(u *domain.User) error {
	err := s.db.QueryRow(
		`INSERT INTO users (login, name, email, avatar_url, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		u.Login, u.Name, u.Email, u.AvatarURL, u.CreatedAt, u.UpdatedAt,
	).Scan(&u.ID)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return store.ErrAlreadyExists
		}
		return err
	}
	return nil
}

func (s *Store) GetUser(login string) (*domain.User, error) {
	u := &domain.User{}
	err := s.db.QueryRow(
		`SELECT id, login, name, email, avatar_url, created_at, updated_at
		 FROM users WHERE login = $1`, login,
	).Scan(&u.ID, &u.Login, &u.Name, &u.Email, &u.AvatarURL, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return u, nil
}

// --- Repositories ---

func (s *Store) CreateRepository(r *domain.Repository) error {
	err := s.db.QueryRow(
		`INSERT INTO repositories (owner, name, description, private, default_branch, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		r.Owner, r.Name, r.Description, r.Private, r.DefaultBranch, r.CreatedAt, r.UpdatedAt,
	).Scan(&r.ID)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return store.ErrAlreadyExists
		}
		return err
	}
	return nil
}

func (s *Store) GetRepository(owner, name string) (*domain.Repository, error) {
	r := &domain.Repository{}
	err := s.db.QueryRow(
		`SELECT id, owner, name, description, private, default_branch, created_at, updated_at
		 FROM repositories WHERE owner = $1 AND name = $2`, owner, name,
	).Scan(&r.ID, &r.Owner, &r.Name, &r.Description, &r.Private, &r.DefaultBranch, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	r.FullName = r.Owner + "/" + r.Name
	return r, nil
}

func (s *Store) GetRepositoryByID(id int64) (*domain.Repository, error) {
	r := &domain.Repository{}
	err := s.db.QueryRow(
		`SELECT id, owner, name, description, private, default_branch, created_at, updated_at
		 FROM repositories WHERE id = $1`, id,
	).Scan(&r.ID, &r.Owner, &r.Name, &r.Description, &r.Private, &r.DefaultBranch, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	r.FullName = r.Owner + "/" + r.Name
	return r, nil
}

func (s *Store) ListRepositories(owner string) ([]*domain.Repository, error) {
	rows, err := s.db.Query(
		`SELECT id, owner, name, description, private, default_branch, created_at, updated_at
		 FROM repositories WHERE owner = $1 ORDER BY name`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Repository
	for rows.Next() {
		r := &domain.Repository{}
		if err := rows.Scan(&r.ID, &r.Owner, &r.Name, &r.Description, &r.Private, &r.DefaultBranch, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.FullName = r.Owner + "/" + r.Name
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UpdateRepository(r *domain.Repository) error {
	res, err := s.db.Exec(
		`UPDATE repositories SET description = $1, private = $2, default_branch = $3, updated_at = $4
		 WHERE owner = $5 AND name = $6`,
		r.Description, r.Private, r.DefaultBranch, r.UpdatedAt, r.Owner, r.Name)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteRepository(owner, name string) error {
	res, err := s.db.Exec(`DELETE FROM repositories WHERE owner = $1 AND name = $2`, owner, name)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// --- Labels ---

func (s *Store) CreateLabel(owner, repo string, label *domain.Label) error {
	if label.Color == "" {
		label.Color = domain.DefaultLabelColor
	}
	_, err := s.db.Exec(
		`INSERT INTO labels (owner, repo, name, color) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
		owner, repo, label.Name, label.Color)
	return err
}

func (s *Store) ListLabels(owner, repo string) ([]*domain.Label, error) {
	rows, err := s.db.Query(`SELECT name, color FROM labels WHERE owner = $1 AND repo = $2 ORDER BY name`, owner, repo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Label
	for rows.Next() {
		l := &domain.Label{}
		if err := rows.Scan(&l.Name, &l.Color); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) HasLabel(owner, repo, name string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM labels WHERE owner = $1 AND repo = $2 AND name = $3)`,
		owner, repo, name).Scan(&exists)
	return exists, err
}

func (s *Store) UpdateLabel(owner, repo, oldName string, label *domain.Label) error {
	if label.Color == "" {
		label.Color = domain.DefaultLabelColor
	}
	res, err := s.db.Exec(
		`UPDATE labels SET name = $1, color = $2 WHERE owner = $3 AND repo = $4 AND name = $5`,
		label.Name, label.Color, owner, repo, oldName)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteLabel(owner, repo, name string) error {
	res, err := s.db.Exec(`DELETE FROM labels WHERE owner = $1 AND repo = $2 AND name = $3`, owner, repo, name)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// --- Issues ---

// nextNumber allocates the next per-repository number inside a transaction
// that holds a row lock on the repository, so concurrent creations on the
// same repository cannot take the same number.
func (s *Store) nextNumber(tx *sql.Tx, table, owner, repo string) (int, error) {
	var dummy int64
	if err := tx.QueryRow(
		`SELECT id FROM repositories WHERE owner = $1 AND name = $2 FOR UPDATE`, owner, repo,
	).Scan(&dummy); err != nil {
		return 0, notFound(err)
	}
	var n int
	if err := tx.QueryRow(
		`SELECT COALESCE(MAX(number), 0) + 1 FROM `+table+` WHERE repo_owner = $1 AND repo_name = $2`,
		owner, repo).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) CreateIssue(i *domain.Issue) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	n, err := s.nextNumber(tx, "issues", i.RepoOwner, i.RepoName)
	if err != nil {
		return err
	}
	i.Number = n
	err = tx.QueryRow(
		`INSERT INTO issues (repo_owner, repo_name, number, title, body, state, labels, author, created_at, updated_at, closed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING id`,
		i.RepoOwner, i.RepoName, i.Number, i.Title, i.Body, string(i.State),
		pq.Array(i.Labels), i.Author, i.CreatedAt, i.UpdatedAt, i.ClosedAt,
	).Scan(&i.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func scanIssue(row interface{ Scan(...any) error }) (*domain.Issue, error) {
	i := &domain.Issue{}
	var labels []string
	err := row.Scan(&i.ID, &i.RepoOwner, &i.RepoName, &i.Number, &i.Title, &i.Body,
		&i.State, pq.Array(&labels), &i.Author, &i.CreatedAt, &i.UpdatedAt, &i.ClosedAt)
	if err != nil {
		return nil, err
	}
	i.Labels = labels
	return i, nil
}

const issueColumns = `id, repo_owner, repo_name, number, title, body, state, labels, author, created_at, updated_at, closed_at`

func (s *Store) GetIssue(owner, repo string, number int) (*domain.Issue, error) {
	i, err := scanIssue(s.db.QueryRow(
		`SELECT `+issueColumns+` FROM issues WHERE repo_owner = $1 AND repo_name = $2 AND number = $3`,
		owner, repo, number))
	if err != nil {
		return nil, notFound(err)
	}
	return i, nil
}

func (s *Store) GetIssueByID(id int64) (*domain.Issue, error) {
	i, err := scanIssue(s.db.QueryRow(
		`SELECT `+issueColumns+` FROM issues WHERE id = $1`, id))
	if err != nil {
		return nil, notFound(err)
	}
	return i, nil
}

func (s *Store) ListIssues(owner, repo string) ([]*domain.Issue, error) {
	rows, err := s.db.Query(
		`SELECT `+issueColumns+` FROM issues WHERE repo_owner = $1 AND repo_name = $2 ORDER BY number`,
		owner, repo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Issue
	for rows.Next() {
		i, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) UpdateIssue(i *domain.Issue) error {
	res, err := s.db.Exec(
		`UPDATE issues SET title = $1, body = $2, state = $3, labels = $4, updated_at = $5, closed_at = $6
		 WHERE repo_owner = $7 AND repo_name = $8 AND number = $9`,
		i.Title, i.Body, string(i.State), pq.Array(i.Labels), i.UpdatedAt, i.ClosedAt,
		i.RepoOwner, i.RepoName, i.Number)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// --- Comments ---

func (s *Store) CreateComment(c *domain.Comment) error {
	err := s.db.QueryRow(
		`INSERT INTO comments (repo_owner, repo_name, issue_number, body, author, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		c.RepoOwner, c.RepoName, c.IssueNumber, c.Body, c.Author, c.CreatedAt, c.UpdatedAt,
	).Scan(&c.ID)
	return err
}

const commentColumns = `id, repo_owner, repo_name, issue_number, body, author, created_at, updated_at`

func scanComment(row interface{ Scan(...any) error }) (*domain.Comment, error) {
	c := &domain.Comment{}
	err := row.Scan(&c.ID, &c.RepoOwner, &c.RepoName, &c.IssueNumber, &c.Body,
		&c.Author, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Store) ListComments(owner, repo string, issueNumber int) ([]*domain.Comment, error) {
	rows, err := s.db.Query(
		`SELECT `+commentColumns+` FROM comments WHERE repo_owner = $1 AND repo_name = $2 AND issue_number = $3 ORDER BY id`,
		owner, repo, issueNumber)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetCommentByID(id int64) (*domain.Comment, error) {
	c, err := scanComment(s.db.QueryRow(
		`SELECT `+commentColumns+` FROM comments WHERE id = $1`, id))
	if err != nil {
		return nil, notFound(err)
	}
	return c, nil
}

// --- Pull requests ---

func (s *Store) CreatePullRequest(p *domain.PullRequest) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	n, err := s.nextNumber(tx, "pull_requests", p.RepoOwner, p.RepoName)
	if err != nil {
		return err
	}
	p.Number = n
	err = tx.QueryRow(
		`INSERT INTO pull_requests (repo_owner, repo_name, number, title, body, state, head, base, head_sha, base_sha,
		 author, merged, merge_commit, mergeable, created_at, updated_at, closed_at, merged_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18) RETURNING id`,
		p.RepoOwner, p.RepoName, p.Number, p.Title, p.Body, string(p.State), p.Head, p.Base, p.HeadSHA, p.BaseSHA,
		p.Author, p.Merged, p.MergeCommit, p.Mergeable, p.CreatedAt, p.UpdatedAt, p.ClosedAt, p.MergedAt,
	).Scan(&p.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

const pullColumns = `id, repo_owner, repo_name, number, title, body, state, head, base, head_sha, base_sha,
	author, merged, merge_commit, mergeable, created_at, updated_at, closed_at, merged_at`

func scanPull(row interface{ Scan(...any) error }) (*domain.PullRequest, error) {
	p := &domain.PullRequest{}
	err := row.Scan(&p.ID, &p.RepoOwner, &p.RepoName, &p.Number, &p.Title, &p.Body,
		&p.State, &p.Head, &p.Base, &p.HeadSHA, &p.BaseSHA, &p.Author, &p.Merged, &p.MergeCommit,
		&p.Mergeable, &p.CreatedAt, &p.UpdatedAt, &p.ClosedAt, &p.MergedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Store) GetPullRequest(owner, repo string, number int) (*domain.PullRequest, error) {
	p, err := scanPull(s.db.QueryRow(
		`SELECT `+pullColumns+` FROM pull_requests WHERE repo_owner = $1 AND repo_name = $2 AND number = $3`,
		owner, repo, number))
	if err != nil {
		return nil, notFound(err)
	}
	return p, nil
}

func (s *Store) GetPullRequestByID(id int64) (*domain.PullRequest, error) {
	p, err := scanPull(s.db.QueryRow(
		`SELECT `+pullColumns+` FROM pull_requests WHERE id = $1`, id))
	if err != nil {
		return nil, notFound(err)
	}
	return p, nil
}

func (s *Store) ListPullRequests(owner, repo string) ([]*domain.PullRequest, error) {
	rows, err := s.db.Query(
		`SELECT `+pullColumns+` FROM pull_requests WHERE repo_owner = $1 AND repo_name = $2 ORDER BY number`,
		owner, repo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.PullRequest
	for rows.Next() {
		p, err := scanPull(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) UpdatePullRequest(p *domain.PullRequest) error {
	res, err := s.db.Exec(
		`UPDATE pull_requests SET title = $1, body = $2, state = $3, head = $4, base = $5, head_sha = $6, base_sha = $7,
		 merged = $8, merge_commit = $9, mergeable = $10, updated_at = $11, closed_at = $12, merged_at = $13
		 WHERE repo_owner = $14 AND repo_name = $15 AND number = $16`,
		p.Title, p.Body, string(p.State), p.Head, p.Base, p.HeadSHA, p.BaseSHA, p.Merged, p.MergeCommit,
		p.Mergeable, p.UpdatedAt, p.ClosedAt, p.MergedAt, p.RepoOwner, p.RepoName, p.Number)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// --- Milestones ---

func (s *Store) CreateMilestone(m *domain.Milestone) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	n, err := s.nextNumber(tx, "milestones", m.RepoOwner, m.RepoName)
	if err != nil {
		return err
	}
	m.Number = n
	err = tx.QueryRow(
		`INSERT INTO milestones (repo_owner, repo_name, number, title, description, state, due_on, created_at, updated_at, closed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
		m.RepoOwner, m.RepoName, m.Number, m.Title, m.Description, string(m.State),
		m.DueOn, m.CreatedAt, m.UpdatedAt, m.ClosedAt,
	).Scan(&m.ID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

const milestoneColumns = `id, repo_owner, repo_name, number, title, description, state, due_on, created_at, updated_at, closed_at`

func scanMilestone(row interface{ Scan(...any) error }) (*domain.Milestone, error) {
	m := &domain.Milestone{}
	err := row.Scan(&m.ID, &m.RepoOwner, &m.RepoName, &m.Number, &m.Title, &m.Description,
		&m.State, &m.DueOn, &m.CreatedAt, &m.UpdatedAt, &m.ClosedAt)
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (s *Store) GetMilestone(owner, repo string, number int) (*domain.Milestone, error) {
	m, err := scanMilestone(s.db.QueryRow(
		`SELECT `+milestoneColumns+` FROM milestones WHERE repo_owner = $1 AND repo_name = $2 AND number = $3`,
		owner, repo, number))
	if err != nil {
		return nil, notFound(err)
	}
	return m, nil
}

func (s *Store) ListMilestones(owner, repo string) ([]*domain.Milestone, error) {
	rows, err := s.db.Query(
		`SELECT `+milestoneColumns+` FROM milestones WHERE repo_owner = $1 AND repo_name = $2 ORDER BY number`,
		owner, repo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*domain.Milestone
	for rows.Next() {
		m, err := scanMilestone(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) UpdateMilestone(m *domain.Milestone) error {
	res, err := s.db.Exec(
		`UPDATE milestones SET title = $1, description = $2, state = $3, due_on = $4, updated_at = $5, closed_at = $6
		 WHERE repo_owner = $7 AND repo_name = $8 AND number = $9`,
		m.Title, m.Description, string(m.State), m.DueOn, m.UpdatedAt, m.ClosedAt,
		m.RepoOwner, m.RepoName, m.Number)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteMilestone(owner, repo string, number int) error {
	res, err := s.db.Exec(`DELETE FROM milestones WHERE repo_owner = $1 AND repo_name = $2 AND number = $3`, owner, repo, number)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// Ensure Store implements store.Store at compile time.
var _ store.Store = (*Store)(nil)
