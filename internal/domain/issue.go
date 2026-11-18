// Package domain holds the forge's core entities (see user.go).
package domain

import "time"

// IssueState is the lifecycle of an issue: open or closed.
type IssueState string

const (
	// IssueOpen is an issue that is still open.
	IssueOpen IssueState = "open"
	// IssueClosed is an issue that has been closed.
	IssueClosed IssueState = "closed"
)

// Issue is one issue on a repository. Number is per-repository, starting
// at 1, exactly like GitHub: the REST API addresses an issue as
// /repos/{owner}/{repo}/issues/{number}.
type Issue struct {
	ID        int64      `json:"id"`
	RepoOwner string     `json:"repo_owner"`
	RepoName  string     `json:"repo_name"`
	Number    int        `json:"number"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	State     IssueState `json:"state"`
	Labels    []string   `json:"labels"`
	Author    string     `json:"author"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
}

// Public returns the GitHub-shaped issue object the REST v3 API serves.
// labelColors maps a label name to its six-hex-digit colour; a label
// missing from the map renders with the default colour.
func (i *Issue) Public(baseURL string, labelColors map[string]string) map[string]any {
	labels := make([]any, 0, len(i.Labels))
	for _, name := range i.Labels {
		color := labelColors[name]
		if color == "" {
			color = DefaultLabelColor
		}
		labels = append(labels, map[string]any{"name": name, "color": color})
	}
	out := map[string]any{
		"id":         i.ID,
		"number":     i.Number,
		"title":      i.Title,
		"body":       i.Body,
		"state":      string(i.State),
		"labels":     labels,
		"user":       map[string]any{"login": i.Author, "type": "User"},
		"created_at": i.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at": i.UpdatedAt.UTC().Format(time.RFC3339),
		"html_url":   baseURL + "/" + i.RepoOwner + "/" + i.RepoName + "/issues/" + itoa(i.Number),
	}
	if i.ClosedAt != nil {
		out["closed_at"] = i.ClosedAt.UTC().Format(time.RFC3339)
	} else {
		out["closed_at"] = nil
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
