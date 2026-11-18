// Package domain holds the forge's core entities (see user.go).
package domain

import "time"

// Comment is one comment on an issue. Its id is a global, monotonically
// increasing number (the REST API addresses a comment by that id), while
// the issue it belongs to is addressed by its per-repository number.
type Comment struct {
	ID          int64     `json:"id"`
	RepoOwner   string    `json:"repo_owner"`
	RepoName    string    `json:"repo_name"`
	IssueNumber int       `json:"issue_number"`
	Body        string    `json:"body"`
	Author      string    `json:"author"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Public returns the GitHub-shaped comment object the REST v3 API serves.
func (c *Comment) Public(baseURL string) map[string]any {
	return map[string]any{
		"id":         c.ID,
		"body":       c.Body,
		"user":       map[string]any{"login": c.Author, "type": "User"},
		"created_at": c.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at": c.UpdatedAt.UTC().Format(time.RFC3339),
		"html_url":   baseURL + "/" + c.RepoOwner + "/" + c.RepoName + "/issues/" + itoa(c.IssueNumber) + "#issuecomment-" + itoa64(c.ID),
	}
}

func itoa64(n int64) string {
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
