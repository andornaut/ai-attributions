// Package gitexec runs git commands against a repository.
package gitexec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

type Repo struct {
	dir string
}

// Commit carries the full message, subject line included, and both identities.
type Commit struct {
	Hash           string
	Message        string
	AuthorName     string
	AuthorEmail    string
	CommitterName  string
	CommitterEmail string
}

func (c Commit) Subject() string {
	if i := strings.IndexByte(c.Message, '\n'); i >= 0 {
		return c.Message[:i]
	}
	return c.Message
}

func (c Commit) Short() string { return Short(c.Hash) }

// Short abbreviates a commit hash. One definition, so that a report naming a
// commit twice cannot print two widths.
func Short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

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

func (r *Repo) Dir() string { return r.dir }

func (r *Repo) Output(args ...string) (string, error) {
	out, _, err := r.output(args...)
	return out, err
}

// output runs git and returns its standard output along with the exit status,
// so that a caller can tell the status a command uses to report "nothing
// matched" from a real failure. The status is -1 when git could not be run.
func (r *Repo) output(args ...string) (string, int, error) {
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
		status := -1
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			status = exit.ExitCode()
		}
		return "", status, fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), 0, nil
}

// outputWithInput runs git with stdin attached, for a command that reads what
// to work on rather than taking it as arguments.
func (r *Repo) outputWithInput(stdin io.Reader, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Stdin = stdin
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

// PathsAtRefs reports which of paths each of refs carries, as a map holding
// only the refs that carry one. A path naming a directory is reported as it was
// given rather than expanded to the files under it.
//
// One git process answers every combination, and each answer costs a descent to
// a known path rather than a walk of the tree it is in. A repository with a
// thousand tags is what this is for: listing every tree to find a file at a
// known path makes a question about the tips cost as much as the history.
func (r *Repo) PathsAtRefs(refs, paths []string) (map[string][]string, error) {
	if len(refs) == 0 || len(paths) == 0 {
		return map[string][]string{}, nil
	}

	var stdin bytes.Buffer
	for _, ref := range refs {
		for _, path := range paths {
			// git reads "<ref>:<path>" and hands back whatever followed the
			// first space as %(rest), which is how a hit names the pair that
			// produced it rather than being matched up by position. A refname
			// can hold neither a space nor a colon, and none of the paths hold
			// a space, so the split is unambiguous.
			fmt.Fprintf(&stdin, "%s:%s %s %s\n", ref, path, ref, path)
		}
	}

	out, err := r.outputWithInput(&stdin, "cat-file", "--batch-check=%(objecttype) %(rest)", "--buffer")
	if err != nil {
		return nil, err
	}

	found := map[string][]string{}
	for line := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
		// A pair that does not exist reports as "<ref>:<path> missing", which
		// carries no %(rest) to be read back, so a line that is not three
		// fields is one that found nothing.
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		ref, path := fields[1], fields[2]
		found[ref] = append(found[ref], path)
	}
	return found, nil
}

func (r *Repo) CombinedOutput(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// Run attaches git's output to the current process, so its progress is visible.
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
		return "", errors.New("HEAD is detached, so there is no current branch to rewrite; drop --current-branch or check out a branch")
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

// Tag names a tag ref and the commit it resolves to. An annotated tag resolves
// through its tag object to the commit below it, which is the value a rewrite
// moves.
type Tag struct {
	Ref    string
	Commit string
}

// tagFormat asks for the tag's own object and the object it peels to, so that
// an annotated tag and a lightweight one are told apart by their fields rather
// than by a second command.
const tagFormat = "--format=%(refname)%00%(objecttype)%00%(objectname)%00%(*objecttype)%00%(*objectname)"

// Tags returns every local tag along with the commit it names. A tag naming
// something other than a commit, a blob for instance, has no commit a rewrite
// could move, so it is left out.
func (r *Repo) Tags() ([]Tag, error) {
	out, err := r.Output("for-each-ref", tagFormat, "refs/tags")
	if err != nil {
		return nil, err
	}

	var tags []Tag
	for _, line := range nonEmptyLines(out) {
		fields := strings.Split(line, "\x00")
		if len(fields) < 5 {
			continue
		}
		ref, kind, hash, peeledKind, peeled := fields[0], fields[1], fields[2], fields[3], fields[4]
		switch {
		case peeledKind == "commit":
			tags = append(tags, Tag{Ref: ref, Commit: peeled})
		case kind == "commit":
			tags = append(tags, Tag{Ref: ref, Commit: hash})
		}
	}
	return tags, nil
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

// CommitsNotIn returns the commits reachable from refs but not from any of
// exclude.
func (r *Repo) CommitsNotIn(exclude []string, refs []string) ([]Commit, error) {
	args := append([]string{"log", "-z", commitFormat}, refs...)
	args = append(args, "--not")
	args = append(args, exclude...)
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
// first.
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

// RemoteValues returns the commit each of the named refs points at on the
// remote, read over the network. A tag has no remote-tracking ref to read
// instead, and a ref the remote does not carry is left out of the map rather
// than reported as an error. --refs drops the peeled ^{} line an annotated tag
// adds, leaving the value the ref itself holds.
func (r *Repo) RemoteValues(remote string, refs []string) (map[string]string, error) {
	args := append([]string{"ls-remote", "--refs", remote}, refs...)
	out, err := r.Output(args...)
	if err != nil {
		return nil, err
	}

	values := map[string]string{}
	for _, line := range nonEmptyLines(out) {
		hash, ref, found := strings.Cut(line, "\t")
		if !found {
			continue
		}
		values[strings.TrimSpace(ref)] = strings.TrimSpace(hash)
	}
	return values, nil
}

// Describe returns a commit's date and subject.
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

// UpdateRefs points each ref at the hash it is keyed with, in one update rather
// than one per ref.
func (r *Repo) UpdateRefs(refs map[string]string) error {
	var stdin bytes.Buffer
	for ref, hash := range refs {
		fmt.Fprintf(&stdin, "update %s %s\n", ref, hash)
	}
	return r.refUpdates(&stdin)
}

// DeleteRefs removes refs, in one update rather than one per ref.
func (r *Repo) DeleteRefs(refs []string) error {
	var stdin bytes.Buffer
	for _, ref := range refs {
		fmt.Fprintf(&stdin, "delete %s\n", ref)
	}
	return r.refUpdates(&stdin)
}

// refUpdates runs one update-ref transaction, in which every line lands or none
// does. A loop of single updates can stop half way through the refs one run
// saves, and half a saved run reads like a whole one to whatever puts it back.
func (r *Repo) refUpdates(commands *bytes.Buffer) error {
	if commands.Len() == 0 {
		return nil
	}
	_, err := r.outputWithInput(commands, "update-ref", "--stdin")
	return err
}

type Remote struct {
	Name    string
	URL     string
	Project string
}

// Remotes returns every configured remote, one per remote rather than one per
// URL: remote.<name>.url is multi-valued, and a remote with several URLs
// fetches from the first, so that is the one that describes it.
func (r *Repo) Remotes() ([]Remote, error) {
	out, status, err := r.output("config", "--get-regexp", `^remote\..*\.url$`)
	if err != nil {
		// git config exits 1 when the pattern matches nothing, which here is a
		// repository with no remotes. Any other status is a failure to read the
		// configuration, not an absence of remotes.
		if status == 1 {
			return nil, nil
		}
		return nil, err
	}

	var remotes []Remote
	seen := make(map[string]bool)
	for line := range strings.SplitSeq(out, "\n") {
		key, url, found := strings.Cut(strings.TrimSpace(line), " ")
		if !found {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "remote."), ".url")
		if seen[name] {
			continue
		}
		seen[name] = true
		remotes = append(remotes, Remote{Name: name, URL: url, Project: project(url)})
	}
	return remotes, nil
}

// project reduces a remote URL to host/owner/repo, so that the same project
// reached over ssh and over https compares equal and two different projects
// do not. Case is folded, since the forges resolve a host, owner, and
// repository name without regard to it.
func project(url string) string {
	rest := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(url), "/"), ".git")

	hasScheme := false
	if _, after, found := strings.Cut(rest, "://"); found {
		rest, hasScheme = after, true
	}
	// A user names who connects, not what is connected to.
	if _, after, found := strings.Cut(rest, "@"); found {
		rest = after
	}

	if i := strings.IndexByte(rest, ':'); i >= 0 {
		host, tail := rest[:i], rest[i+1:]
		switch {
		case hasScheme:
			// ssh://git@host:22/owner/repo. A port names how to reach the
			// project, not which project it is.
			if port, path, found := strings.Cut(tail, "/"); found && isPort(port) {
				rest = host + "/" + path
			}
		case !strings.Contains(host, "/"):
			// The scp-like form, host:owner/repo, with or without a user,
			// which git recognizes by a colon ahead of the first slash.
			rest = host + "/" + tail
		}
	}
	return strings.ToLower(rest)
}

func isPort(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

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
