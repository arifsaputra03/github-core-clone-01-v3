// Package bare owns the on-disk layout of a bare repository: the
// directories and files git expects, created when a repository is made
// through the API. The object and ref contents are the git package's job;
// this package only knows where a bare repository lives and what its
// skeleton looks like.
package bare

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"forge/internal/git"
)

// Layout is the skeleton of a bare repository, as git init --bare writes
// it: objects/ and refs/ (with heads/ and tags/), info/, a HEAD naming the
// default branch, a config marking the repository bare, and a description.
var layoutDirs = []string{
	"objects",
	"objects/info",
	"objects/pack",
	"refs",
	"refs/heads",
	"refs/tags",
	"info",
	"hooks",
}

// Path returns the bare repository's directory for owner/name.
func Path(dataDir, owner, name string) string {
	return filepath.Join(dataDir, "repos", owner, name+".git")
}

// Exists reports whether a bare repository already exists on disk.
func Exists(dataDir, owner, name string) bool {
	_, err := os.Stat(filepath.Join(Path(dataDir, owner, name), "HEAD"))
	return err == nil
}

// Init creates the bare repository layout at Path(dataDir, owner, name).
// It is an error if a repository already exists there.
func Init(dataDir, owner, name, defaultBranch string) error {
	dir := Path(dataDir, owner, name)
	if Exists(dataDir, owner, name) {
		return fmt.Errorf("bare: repository %s/%s already exists", owner, name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, d := range layoutDirs {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return err
		}
	}
	files := map[string]string{
		"HEAD":        "ref: refs/heads/" + defaultBranch + "\n",
		"config":      "[core]\n\trepositoryformatversion = 0\n\tbare = true\n",
		"description": "Unnamed repository; edit this file 'description' to name the repository.\n",
		"info/exclude": "# git ls-files --others --exclude-from=.git/info/exclude\n" +
			"# Lines that start with '#' are comments.\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Open returns the git repository at Path(dataDir, owner, name), or an
// error when no bare repository exists there.
func Open(dataDir, owner, name string) (*git.Repo, error) {
	if !Exists(dataDir, owner, name) {
		return nil, fmt.Errorf("bare: no repository %s/%s", owner, name)
	}
	return git.Open(Path(dataDir, owner, name)), nil
}

// Remove deletes a bare repository from disk. It is used to roll back a
// failed creation.
func Remove(dataDir, owner, name string) error {
	return os.RemoveAll(Path(dataDir, owner, name))
}

// ValidateName reports whether name is a usable repository name. The suite
// uses names like gitconf-<slug>; this accepts the same shape GitHub does
// for a new repository, and rejects the forms that would escape the data
// directory or confuse git.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("bare: repository name is empty")
	}
	if len(name) > 100 {
		return fmt.Errorf("bare: repository name is too long")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("bare: repository name %q is not allowed", name)
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("bare: repository name %q must not contain a slash", name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("bare: repository name %q holds a control character", name)
		}
	}
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "-") {
		return fmt.Errorf("bare: repository name %q must not start with %q", name, name[:1])
	}
	return nil
}
