// Package rewrite drives git-filter-repo to replace commit messages in place.
package rewrite

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/andornaut/ai-attributions/internal/gitexec"
)

// Change is the replacement content for one commit. An empty field leaves what
// the commit already carries in place.
type Change struct {
	Message        string `json:"message,omitempty"`
	AuthorName     string `json:"author_name,omitempty"`
	AuthorEmail    string `json:"author_email,omitempty"`
	CommitterName  string `json:"committer_name,omitempty"`
	CommitterEmail string `json:"committer_email,omitempty"`
}

// callbackTemplate is the body of a git-filter-repo --commit-callback. It looks
// each commit up by its pre-rewrite hash in a map written by this program, so
// that every decision is made in Go rather than duplicated in Python.
const callbackTemplate = `
import json
_cache = globals()
if "_ai_attributions" not in _cache:
    with open(%s) as _f:
        _cache["_ai_attributions"] = json.load(_f)
_change = _cache["_ai_attributions"].get(commit.original_id.decode())
if _change is not None:
    for _field in ("message", "author_name", "author_email", "committer_name", "committer_email"):
        _value = _change.get(_field)
        if _value is not None:
            setattr(commit, _field, _value.encode())
`

// CheckAvailable reports whether git-filter-repo is installed.
func CheckAvailable() error {
	cmd := exec.Command("git", "filter-repo", "--version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git-filter-repo is not installed; see https://github.com/newren/git-filter-repo")
	}
	return nil
}

// Run rewrites the given refs so that each commit named in changes carries its
// replacement content. Every other commit keeps what it has, though rewriting
// an ancestor still changes the hashes of its descendants.
func Run(repo *gitexec.Repo, refs []string, changes map[string]Change) error {
	mapFile, err := writeMapFile(changes)
	if err != nil {
		return err
	}
	defer os.Remove(mapFile)

	quotedPath, err := json.Marshal(mapFile)
	if err != nil {
		return err
	}

	// --refs takes every ref as values of a single flag; repeating the flag
	// would keep only the last one.
	args := []string{"filter-repo", "--force", "--partial", "--refs"}
	args = append(args, refs...)
	args = append(args, "--commit-callback", fmt.Sprintf(callbackTemplate, quotedPath))

	// git-filter-repo writes its progress with a bare carriage return, which
	// runs its lines together when it is not on a terminal. Its output is only
	// worth showing when it fails.
	if out, err := repo.CombinedOutput(args...); err != nil {
		return fmt.Errorf("%w\n%s", err, out)
	}
	return nil
}

func writeMapFile(changes map[string]Change) (string, error) {
	file, err := os.CreateTemp("", "ai-attributions-*.json")
	if err != nil {
		return "", err
	}
	defer file.Close()

	if err := json.NewEncoder(file).Encode(changes); err != nil {
		os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}
