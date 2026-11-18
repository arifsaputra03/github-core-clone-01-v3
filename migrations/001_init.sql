-- 001_init.sql: initial schema for the forge's PostgreSQL store.
--
-- The same SQL lives as a Go string in internal/store/postgres/postgres.go
-- (the package cannot embed a file outside its own directory), and the two
-- must be kept identical. Apply this file by hand with psql when setting a
-- database up outside the forge; the forge runs it itself on open.
--
-- Per-repository issue and pull-request numbers are allocated by taking
-- MAX(number)+1 inside a transaction that holds a row lock on the
-- repository (SELECT ... FOR UPDATE), so two concurrent creations on the
-- same repository cannot take the same number.

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
