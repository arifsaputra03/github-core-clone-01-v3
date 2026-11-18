// Package domain holds the forge's core entities (see user.go).
package domain

import "time"

// Repository is one git repository on the forge. Its code lives in a bare
// repository on disk (see internal/bare); this record is the metadata the
// REST API serves. Owner is a login, never a numeric id, so a repository is
// addressed as owner/name everywhere, exactly like GitHub.
type Repository struct {
	ID            int64     `json:"id"`
	Owner         string    `json:"owner"`
	Name          string    `json:"name"`
	FullName      string    `json:"full_name"`
	Description   string    `json:"description"`
	Private       bool      `json:"private"`
	DefaultBranch string    `json:"default_branch"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Public returns the GitHub-shaped repository object the REST v3 API serves.
// The URLs are relative to the forge's own base URL, filled in by the HTTP
// layer, so this method only carries the fields that do not depend on it.
func (r *Repository) Public(baseURL string) map[string]any {
	full := r.Owner + "/" + r.Name
	return map[string]any{
		"id":             r.ID,
		"name":           r.Name,
		"full_name":      full,
		"owner":          map[string]any{"login": r.Owner, "id": 0, "type": "User"},
		"private":        r.Private,
		"description":    r.Description,
		"fork":           false,
		"html_url":       baseURL + "/" + full,
		"clone_url":      baseURL + "/" + full + ".git",
		"git_url":        baseURL + "/" + full + ".git",
		"ssh_url":        "git@" + full + ".git",
		"default_branch": r.DefaultBranch,
		"created_at":     r.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":     r.UpdatedAt.UTC().Format(time.RFC3339),
		"pushed_at":      r.UpdatedAt.UTC().Format(time.RFC3339),
		"has_issues":     true,
		"has_projects":   false,
		"has_wiki":       false,
		"size":           0,
	}
}
