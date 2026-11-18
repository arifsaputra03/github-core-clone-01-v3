// Package domain holds the forge's core entities. These are plain data
// structures with no behaviour and no dependencies on any store or transport:
// the use cases in internal/service and the stores in internal/store work
// with these types, and the HTTP layer only translates them to JSON.
package domain

import "time"

// User is one account on the forge. The viewer (the login the bearer token
// names) is the only user the grading suite drives, but the type is general:
// a repository, an issue and a pull request all name an owner by login.
type User struct {
	ID        int64     `json:"id"`
	Login     string    `json:"login"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	AvatarURL string    `json:"avatar_url,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Public returns the GitHub-shaped public view of a user.
func (u *User) Public() map[string]any {
	return map[string]any{
		"id":         u.ID,
		"login":      u.Login,
		"name":       u.Name,
		"email":      u.Email,
		"avatar_url": u.AvatarURL,
		"type":       "User",
	}
}
