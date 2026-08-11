// Package gitexec runs git commands against a repository.
package gitexec

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Repo is a git repository on disk.
type Repo struct {
	dir string
}

// Commit is a commit, its full message including the subject line, and the two
// identities it carries.
type Commit struct {
	Hash           string
	Message        string
	AuthorName     string
	AuthorEmail    string
	CommitterName  string
	CommitterEmail string
}

// Subject returns the first line of the commit message.
func (c Commit) Subject() string {
	if i := strings.IndexByte(c.Message, '\n'); i >= 0 {
		return c.Message[:i]
	}
	return c.Message
}

// Short returns an abbreviated commit hash.
func (c Commit) Short() string {
	if len(c.Hash) > 12 {
		return c.Hash[:12]
	}
	return c.Hash
}

// Open returns the repository rooted at or above dir.
func Open(dir string) (*Repo, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, err
	}
	repo := &Repo{dir: abs}
	if _, err := repo.Output("rev-parse", "--git-dir"); err != nil {
		return nil, fmt.Errorf("%s is not a git repository", abs)
	}
	return repo, nil
}

// Dir returns the path the repository was opened at.
func (r *Repo) Dir() string { return r.dir }

// Output runs git and returns its standard output.
func (r *Repo) Output(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// CombinedOutput runs git and returns its standard output and standard error
// together, for commands whose output is only worth showing when they fail.
func (r *Repo) CombinedOutput(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// Run runs git with its output attached to the current process, for commands
// whose progress the user should see.
func (r *Repo) Run(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// CurrentBranch returns the fully qualified ref that HEAD points at.
func (r *Repo) CurrentBranch() (string, error) {
	out, err := r.Output("symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		return "", fmt.Errorf("HEAD is detached, so there is no current branch to rewrite; use --all or check out a branch")
	}
	return strings.TrimSpace(out), nil
}

// ListRefs returns every local branch and tag.
func (r *Repo) ListRefs() ([]string, error) {
	out, err := r.Output("for-each-ref", "--format=%(refname)", "refs/heads", "refs/tags")
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(out), nil
}

// Resolve returns the hash a ref points at. For an annotated tag this is the
// tag object, not the commit it tags.
func (r *Repo) Resolve(ref string) (string, error) {
	out, err := r.Output("rev-parse", "--verify", ref)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// IsClean reports whether the working tree and index have no changes to
// tracked files. Untracked files cannot affect a message-only rewrite, so they
// do not count.
func (r *Repo) IsClean() (bool, error) {
	out, err := r.Output("status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

// commitFormat separates the fixed fields with a unit separator, so that a name
// holding a newline cannot be mistaken for the start of the message.
const commitFormat = "--format=%H%x1f%an%x1f%ae%x1f%cn%x1f%ce%x1f%B"

// Commits returns every commit reachable from refs, newest first.
func (r *Repo) Commits(refs []string) ([]Commit, error) {
	args := append([]string{"log", "-z", commitFormat}, refs...)
	return r.parseCommits(args)
}

// CommitsNotIn returns the commits reachable from ref but not from any of
// exclude, which is what a branch holds that the refs beside it do not.
func (r *Repo) CommitsNotIn(exclude []string, ref string) ([]Commit, error) {
	args := append([]string{"log", "-z", commitFormat, ref, "--not"}, exclude...)
	return r.parseCommits(args)
}

func (r *Repo) parseCommits(args []string) ([]Commit, error) {
	out, err := r.Output(args...)
	if err != nil {
		return nil, err
	}

	var commits []Commit
	for record := range strings.SplitSeq(out, "\x00") {
		if record == "" {
			continue
		}
		fields := strings.SplitN(record, "\x1f", 6)
		if len(fields) < 6 {
			continue
		}
		commits = append(commits, Commit{
			Hash:           fields[0],
			AuthorName:     fields[1],
			AuthorEmail:    fields[2],
			CommitterName:  fields[3],
			CommitterEmail: fields[4],
			Message:        fields[5],
		})
	}
	return commits, nil
}

// Graph returns every commit reachable from ref followed by its parents, newest
// first, which is enough to work out which commits a rewrite moves.
func (r *Repo) Graph(ref string) ([][]string, error) {
	out, err := r.Output("rev-list", "--topo-order", "--parents", ref)
	if err != nil {
		return nil, err
	}
	var graph [][]string
	for line := range strings.SplitSeq(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			graph = append(graph, strings.Fields(line))
		}
	}
	return graph, nil
}

// RemoteRefs returns the remote-tracking refs for a remote, excluding its HEAD.
func (r *Repo) RemoteRefs(remote string) ([]string, error) {
	out, err := r.Output("for-each-ref", "--format=%(refname)", "refs/remotes/"+remote)
	if err != nil {
		return nil, err
	}
	var refs []string
	for _, ref := range nonEmptyLines(out) {
		if !strings.HasSuffix(ref, "/HEAD") {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

// Describe returns a commit's date and subject, for naming it in a report.
func (r *Repo) Describe(hash string) string {
	out, err := r.Output("log", "-1", "--format=%cs %s", hash)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Config returns a git configuration value, or an empty string when it is unset.
func (r *Repo) Config(key string) string {
	out, err := r.Output("config", "--get", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// UpdateRef points ref at hash.
func (r *Repo) UpdateRef(hash, ref string) error {
	_, err := r.Output("update-ref", ref, hash)
	return err
}

// HasRemote reports whether the named remote is configured.
func (r *Repo) HasRemote(name string) bool {
	out, err := r.Output("remote")
	if err != nil {
		return false
	}
	return slices.Contains(nonEmptyLines(out), name)
}

func nonEmptyLines(out string) []string {
	var lines []string
	for line := range strings.SplitSeq(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
