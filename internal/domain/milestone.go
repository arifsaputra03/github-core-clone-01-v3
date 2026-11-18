// Package domain holds the forge's core entities (see user.go).
package domain

import "time"

// MilestoneState is the lifecycle of a milestone: open or closed.
type MilestoneState string

const (
	// MilestoneOpen is a milestone that is still open.
	MilestoneOpen MilestoneState = "open"
	// MilestoneClosed is a milestone that is closed.
	MilestoneClosed MilestoneState = "closed"
)

// Milestone is one milestone on a repository. Number is per-repository,
// starting at 1, like an issue's.
type Milestone struct {
	ID          int64          `json:"id"`
	RepoOwner   string         `json:"repo_owner"`
	RepoName    string         `json:"repo_name"`
	Number      int            `json:"number"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	State       MilestoneState `json:"state"`
	DueOn       *time.Time     `json:"due_on,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	ClosedAt    *time.Time     `json:"closed_at,omitempty"`
}

// Public returns the GitHub-shaped milestone object the REST v3 API serves.
func (m *Milestone) Public(baseURL string) map[string]any {
	out := map[string]any{
		"id":            m.ID,
		"number":        m.Number,
		"title":         m.Title,
		"description":   m.Description,
		"state":         string(m.State),
		"creator":       map[string]any{"login": "", "type": "User"},
		"open_issues":   0,
		"closed_issues": 0,
		"created_at":    m.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":    m.UpdatedAt.UTC().Format(time.RFC3339),
		"html_url":      baseURL + "/" + m.RepoOwner + "/" + m.RepoName + "/milestone/" + itoa(m.Number),
	}
	if m.DueOn != nil {
		out["due_on"] = m.DueOn.UTC().Format(time.RFC3339)
	} else {
		out["due_on"] = nil
	}
	if m.ClosedAt != nil {
		out["closed_at"] = m.ClosedAt.UTC().Format(time.RFC3339)
	} else {
		out["closed_at"] = nil
	}
	return out
}
