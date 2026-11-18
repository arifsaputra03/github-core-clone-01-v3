// Package domain holds the forge's core entities (see user.go).
package domain

import "time"

// PullRequestState is the lifecycle of a pull request: open, closed or merged.
type PullRequestState string

const (
	// PullOpen is a pull request that is still open.
	PullOpen PullRequestState = "open"
	// PullClosed is a pull request that was closed without merging.
	PullClosed PullRequestState = "closed"
	// PullMerged is a pull request that was merged.
	PullMerged PullRequestState = "merged"
)

// PullRequest is one pull request on a repository. Like an issue it carries
// a per-repository number starting at 1; its head is the branch the change
// comes from and its base is the branch it merges into. Merged is true once
// the merge has happened, and MergeCommit is the squash commit the merge
// writes onto the default branch.
type PullRequest struct {
	ID        int64            `json:"id"`
	RepoOwner string           `json:"repo_owner"`
	RepoName  string           `json:"repo_name"`
	Number    int              `json:"number"`
	Title     string           `json:"title"`
	Body      string           `json:"body"`
	State     PullRequestState `json:"state"`
	Head      string           `json:"head"`
	Base      string           `json:"base"`
	HeadSHA   string           `json:"head_sha"`
	// BaseSHA is the base branch tip the pull request was created against
	// (the merge-base side of its diff). The REST view resolves the
	// CURRENT base tip at read time instead, like GitHub does.
	BaseSHA     string     `json:"base_sha"`
	Author      string     `json:"author"`
	Merged      bool       `json:"merged"`
	MergeCommit string     `json:"merge_commit_sha,omitempty"`
	Mergeable   bool       `json:"mergeable"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
	MergedAt    *time.Time `json:"merged_at,omitempty"`
}

// Public returns the GitHub-shaped pull request object the REST v3 API serves.
// baseSHA is the CURRENT tip of the base branch, resolved by the caller at
// read time (GitHub's base.sha tracks the branch, not the creation commit);
// when it is empty the stored BaseSHA is used instead.
func (p *PullRequest) Public(baseURL, baseSHA string) map[string]any {
	if baseSHA == "" {
		baseSHA = p.BaseSHA
	}
	state := string(p.State)
	if p.Merged {
		state = "closed"
	}
	out := map[string]any{
		"id":     p.ID,
		"number": p.Number,
		"title":  p.Title,
		"body":   p.Body,
		"state":  state,
		"user":   map[string]any{"login": p.Author, "type": "User"},
		"head": map[string]any{
			"label": p.RepoOwner + ":" + p.Head,
			"ref":   p.Head,
			"sha":   p.HeadSHA,
		},
		"base": map[string]any{
			"label": p.RepoOwner + ":" + p.Base,
			"ref":   p.Base,
			"sha":   baseSHA,
		},
		"merged":           p.Merged,
		"mergeable":        p.Mergeable,
		"merge_commit_sha": p.MergeCommit,
		"created_at":       p.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":       p.UpdatedAt.UTC().Format(time.RFC3339),
		"html_url":         baseURL + "/" + p.RepoOwner + "/" + p.RepoName + "/pull/" + itoa(p.Number),
	}
	if p.ClosedAt != nil {
		out["closed_at"] = p.ClosedAt.UTC().Format(time.RFC3339)
	} else {
		out["closed_at"] = nil
	}
	if p.MergedAt != nil {
		out["merged_at"] = p.MergedAt.UTC().Format(time.RFC3339)
	} else {
		out["merged_at"] = nil
	}
	return out
}
