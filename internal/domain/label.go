// Package domain holds the forge's core entities (see user.go).
package domain

// DefaultLabelColor is the colour a label gets when the caller does not
// name one.
const DefaultLabelColor = "ededed"

// Label is one label on a repository, as GitHub's REST v3 API serves it:
// a name and a six-hex-digit colour (no leading '#'). The suite creates
// labels with an explicit colour; when none is given the forge falls back
// to GitHub's conventional default for a new label.
type Label struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// Public returns the GitHub-shaped label object the REST v3 API serves.
func (l *Label) Public() map[string]any {
	color := l.Color
	if color == "" {
		color = DefaultLabelColor
	}
	return map[string]any{
		"name":        l.Name,
		"color":       color,
		"description": "",
		"default":     false,
	}
}
