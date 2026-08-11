// Package rewrite drives git-filter-repo to replace commit messages in place.
package rewrite

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/andornaut/ai-attributions/internal/gitexec"
)

// callbackTemplate is the body of a git-filter-repo --commit-callback. It looks
// each commit up by its pre-rewrite hash in a map written by this program, so
// that every message decision is made in Go rather than duplicated in Python.
const callbackTemplate = `
import json
_cache = globals()
if "_ai_attributions" not in _cache:
    with open(%s) as _f:
        _cache["_ai_attributions"] = json.load(_f)
_replacement = _cache["_ai_attributions"].get(commit.original_id.decode())
if _replacement is not None:
    commit.message = _replacement.encode()
`

// CheckAvailable reports whether git-filter-repo is installed.
func CheckAvailable() error {
	cmd := exec.Command("git", "filter-repo", "--version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git-filter-repo is not installed; see https://github.com/newren/git-filter-repo")
	}
	return nil
}

// Run rewrites the given refs so that each commit named in messages carries its
// replacement message. Every other commit keeps its message, though rewriting
// an ancestor still changes the hashes of its descendants.
func Run(repo *gitexec.Repo, refs []string, messages map[string]string) error {
	mapFile, err := writeMapFile(messages)
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

	return repo.Run(args...)
}

func writeMapFile(messages map[string]string) (string, error) {
	file, err := os.CreateTemp("", "ai-attributions-*.json")
	if err != nil {
		return "", err
	}
	defer file.Close()

	if err := json.NewEncoder(file).Encode(messages); err != nil {
		os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}
