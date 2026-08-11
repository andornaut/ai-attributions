// Command ai-attributions removes AI attributions from a repository's git
// history: co-author and session trailers, "generated with" footers, the
// emdashes that AI-written commit messages leave behind, and the agent
// identities on the commits themselves.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/andornaut/ai-attributions/internal/clean"
	"github.com/andornaut/ai-attributions/internal/gitexec"
	"github.com/andornaut/ai-attributions/internal/rewrite"
)

const usage = `usage: ai-attributions [flags] [repo-path]

Scans the commit messages and identities of the current branch for AI
attributions and reports what it would change. Nothing is rewritten without
-apply. repo-path defaults to the current directory.

flags:
`

const backupPrefix = "refs/ai-attributions-backup/"

// errFound reports attributions to the caller of -check without printing.
var errFound = errors.New("attributions found")

type config struct {
	all         bool
	apply       bool
	check       bool
	exclude     refPatterns
	identity    string
	listBackups bool
	noBackup    bool
	noEmdashes  bool
	noIdentity  bool
	noTrailers  bool
	push        bool
	remote      string
	restore     string
	verbose     bool
	version     bool
}

// target is a ref to rewrite, the commit it pointed at beforehand, and the
// value to expect on the remote when pushing. The lease is empty for a ref with
// no remote-tracking counterpart, such as a tag or a branch never pushed.
type target struct {
	ref   string
	hash  string
	lease string
}

// identity is the name and address to put on a commit an agent authored. Both
// parts are set together or not at all, so that a rewrite can never assign half
// of one.
type identity struct {
	name    string
	address string
	enabled bool
}

func (i identity) String() string { return i.name + " <" + i.address + ">" }

// resolved reports whether there is an identity to re-attribute to. Scanning
// works without one; only a rewrite needs it.
func (i identity) resolved() bool { return i.name != "" && i.address != "" }

// refPatterns collects the repeatable -exclude flag.
type refPatterns []string

func (p *refPatterns) String() string     { return strings.Join(*p, ",") }
func (p *refPatterns) Set(v string) error { *p = append(*p, v); return nil }

func main() {
	if err := run(os.Args[1:]); err != nil {
		if !errors.Is(err, errFound) {
			fmt.Fprintf(os.Stderr, "ai-attributions: %v\n", err)
		}
		os.Exit(1)
	}
}

func run(argv []string) error {
	cfg, path, err := parseFlags(argv)
	if err != nil {
		return err
	}
	if cfg.version {
		fmt.Println(version())
		return nil
	}

	repo, err := gitexec.Open(path)
	if err != nil {
		return err
	}
	switch {
	case cfg.listBackups:
		return listBackups(repo)
	case cfg.restore != "":
		return restoreBackup(repo, cfg.restore)
	}

	opts := clean.Options{Trailers: !cfg.noTrailers, Emdashes: !cfg.noEmdashes}
	who, err := targetIdentity(repo, cfg)
	if err != nil {
		return err
	}

	refs, err := targetRefs(repo, cfg)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		fmt.Println("no commits to inspect")
		return nil
	}

	commits, err := repo.Commits(refs)
	if err != nil {
		return err
	}
	found := inspect(opts, who, commits)
	found.report(cfg.verbose, refs)

	if err := found.reportRadius(repo, refs); err != nil {
		return err
	}
	remote, err := reportRemoteOnly(repo, cfg, opts, who, refs)
	if err != nil {
		return err
	}

	if found.flagged+remote == 0 {
		return nil
	}
	if !cfg.apply {
		if len(found.changes) > 0 {
			fmt.Println("\nnothing was rewritten. Pass -apply to rewrite the history")
		}
		if cfg.check {
			return errFound
		}
		return nil
	}
	return apply(repo, cfg, refs, found.changes)
}

func parseFlags(argv []string) (config, string, error) {
	var cfg config
	flags := flag.NewFlagSet("ai-attributions", flag.ExitOnError)
	flags.BoolVar(&cfg.all, "all", false, "scan every local branch and tag, not just the current branch")
	flags.BoolVar(&cfg.apply, "apply", false, "rewrite the history; without this nothing is changed")
	flags.BoolVar(&cfg.check, "check", false, "exit non-zero when attributions are found")
	flags.Var(&cfg.exclude, "exclude", "skip refs matching this glob (repeatable)")
	flags.StringVar(&cfg.identity, "identity", "", "identity to put on agent-authored commits (default: the repository's user.name and user.email)")
	flags.BoolVar(&cfg.listBackups, "list-backups", false, "list the saved pre-rewrite refs, then exit")
	flags.BoolVar(&cfg.noBackup, "no-backup", false, "skip saving the pre-rewrite refs under "+backupPrefix)
	flags.BoolVar(&cfg.noEmdashes, "no-emdashes", false, "leave emdashes alone")
	flags.BoolVar(&cfg.noIdentity, "no-identity", false, "leave agent author and committer identities alone")
	flags.BoolVar(&cfg.noTrailers, "no-trailers", false, "leave attribution trailers and footers alone")
	flags.BoolVar(&cfg.push, "push", false, "force push the rewritten refs; requires -apply")
	flags.StringVar(&cfg.remote, "remote", "origin", "remote to push to")
	flags.StringVar(&cfg.restore, "restore", "", "restore the refs saved under this backup timestamp, then exit")
	flags.BoolVar(&cfg.verbose, "verbose", false, "report every commit rather than a summary")
	flags.BoolVar(&cfg.version, "version", false, "print the version, then exit")
	flags.Usage = func() {
		fmt.Fprint(flags.Output(), usage)
		flags.PrintDefaults()
	}
	if err := flags.Parse(argv); err != nil {
		return cfg, "", err
	}

	switch {
	case flags.NArg() > 1:
		return cfg, "", fmt.Errorf("expected at most one repo-path, got %d arguments", flags.NArg())
	case cfg.noTrailers && cfg.noIdentity:
		return cfg, "", fmt.Errorf("-no-trailers and -no-identity together leave nothing that can move a commit, since an emdash never does on its own")
	case cfg.push && !cfg.apply:
		return cfg, "", fmt.Errorf("-push needs -apply; there is nothing to push until the history is rewritten")
	case cfg.check && cfg.apply:
		return cfg, "", fmt.Errorf("-check reports without changing anything, so it cannot be combined with -apply")
	}

	repoPath := "."
	if flags.NArg() == 1 {
		repoPath = flags.Arg(0)
	}
	return cfg, repoPath, nil
}

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "devel"
}

// targetIdentity resolves who agent-authored commits are re-attributed to. A
// bad -identity is always an error, but an unset git identity is only one when
// there is a rewrite to do: scanning and -check report agent identities without
// needing somewhere to move them to.
func targetIdentity(repo *gitexec.Repo, cfg config) (identity, error) {
	if cfg.noIdentity {
		return identity{}, nil
	}

	if cfg.identity != "" {
		name, address, found := strings.Cut(strings.TrimSuffix(strings.TrimSpace(cfg.identity), ">"), "<")
		who := identity{name: strings.TrimSpace(name), address: strings.TrimSpace(address), enabled: true}
		if !found || !who.resolved() {
			return identity{}, fmt.Errorf("-identity should look like \"Name <email>\" with both parts set, got %q", cfg.identity)
		}
		return who, nil
	}

	who := identity{name: repo.Config("user.name"), address: repo.Config("user.email"), enabled: true}
	if !who.resolved() {
		if cfg.apply {
			return identity{}, fmt.Errorf("the repository has no user.name and user.email to re-attribute to; pass -identity or -no-identity")
		}
		fmt.Println("note: no user.name and user.email are set, so agent identities are reported but cannot be rewritten")
		return identity{enabled: true}, nil
	}
	return who, nil
}

// targetRefs returns the refs to scan and rewrite, or nothing when the
// repository has no commits to walk.
func targetRefs(repo *gitexec.Repo, cfg config) ([]string, error) {
	var refs []string
	if cfg.all {
		found, err := repo.ListRefs()
		if err != nil {
			return nil, err
		}
		refs = found
	} else {
		branch, err := repo.CurrentBranch()
		if err != nil {
			return nil, err
		}
		// A branch with no commits yet is a valid symbolic ref that nothing
		// resolves to, which git log cannot walk.
		if _, err := repo.Resolve(branch); err != nil {
			return nil, nil
		}
		refs = []string{branch}
	}

	var kept []string
	for _, ref := range refs {
		if excluded, err := cfg.exclude.matches(ref); err != nil {
			return nil, err
		} else if excluded {
			fmt.Printf("excluding %s\n", ref)
			continue
		}
		kept = append(kept, ref)
	}
	return kept, nil
}

// matches reports whether a ref is excluded, testing each pattern against the
// full ref and its short forms, so that -exclude dev covers refs/tags/dev and
// -exclude agent-work covers refs/remotes/origin/agent-work.
func (p refPatterns) matches(ref string) (bool, error) {
	candidates := []string{ref}
	for _, prefix := range []string{"refs/heads/", "refs/tags/", "refs/remotes/"} {
		if short, ok := strings.CutPrefix(ref, prefix); ok {
			candidates = append(candidates, short)
			// A remote-tracking ref is still qualified by its remote, so the
			// branch name on its own is offered too.
			if prefix == "refs/remotes/" {
				if _, branch, found := strings.Cut(short, "/"); found {
					candidates = append(candidates, branch)
				}
			}
		}
	}

	for _, pattern := range p {
		for _, candidate := range candidates {
			matched, err := path.Match(pattern, candidate)
			if err != nil {
				return false, fmt.Errorf("bad -exclude pattern %q: %w", pattern, err)
			}
			if matched {
				return true, nil
			}
		}
	}
	return false, nil
}

func apply(repo *gitexec.Repo, cfg config, refs []string, changes map[string]rewrite.Change) error {
	if err := checkRewritable(repo, cfg); err != nil {
		return err
	}
	targets, err := collectTargets(repo, cfg, refs)
	if err != nil {
		return err
	}
	if err := rewrite.Run(repo, refs, changes); err != nil {
		return err
	}
	reportRewritten(repo, targets)
	reportUnleased(targets)

	if cfg.push {
		fmt.Printf("\npushing to %s\n", cfg.remote)
		return repo.Run(pushArgs(cfg.remote, targets)...)
	}
	fmt.Printf("\nnot pushed. To publish the rewrite:\n\n    git %s\n\n",
		strings.Join(pushArgs(cfg.remote, targets), " "))
	return nil
}

func checkRewritable(repo *gitexec.Repo, cfg config) error {
	if err := rewrite.CheckAvailable(); err != nil {
		return err
	}
	// Checked before the rewrite rather than after, so a typo in -remote does
	// not leave a rewritten history with no way to publish it.
	if cfg.push && !repo.HasRemote(cfg.remote) {
		return fmt.Errorf("the repository has no remote named %q", cfg.remote)
	}
	isClean, err := repo.IsClean()
	if err != nil {
		return err
	}
	if !isClean {
		return fmt.Errorf("the working tree has uncommitted changes; commit or stash them first")
	}
	return nil
}

// collectTargets records where each ref pointed before the rewrite, along with
// the remote value to hold the push lease against. The pre-rewrite hashes are
// needed even when the backup refs are not written.
func collectTargets(repo *gitexec.Repo, cfg config, refs []string) ([]target, error) {
	stamp := time.Now().UTC().Format("20060102T150405Z")
	targets := make([]target, 0, len(refs))
	for _, ref := range refs {
		hash, err := repo.Resolve(ref)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target{ref: ref, hash: hash, lease: leaseFor(repo, cfg.remote, ref)})

		if cfg.noBackup {
			continue
		}
		if err := repo.UpdateRef(hash, backupPrefix+stamp+"/"+strings.TrimPrefix(ref, "refs/")); err != nil {
			return nil, err
		}
	}
	if !cfg.noBackup {
		fmt.Printf("saved the pre-rewrite refs under %s%s/\n", backupPrefix, stamp)
	}
	return targets, nil
}

// leaseFor returns the commit the remote-tracking ref points at, which is what
// the remote held when it was last fetched. The local ref is the wrong value to
// lease against, since it moves ahead of the remote on every local commit.
func leaseFor(repo *gitexec.Repo, remote, ref string) string {
	branch, ok := strings.CutPrefix(ref, "refs/heads/")
	if !ok {
		return ""
	}
	lease, err := repo.Resolve("refs/remotes/" + remote + "/" + branch)
	if err != nil {
		return ""
	}
	return lease
}

func reportRewritten(repo *gitexec.Repo, targets []target) {
	fmt.Println()
	for _, t := range targets {
		hash, err := repo.Resolve(t.ref)
		if err != nil {
			continue
		}
		fmt.Printf("%s %s -> %s\n", t.ref, shorten(t.hash), shorten(hash))
	}
}

// reportUnleased names the refs that will be pushed without a lease, so that
// the weaker guarantee is visible rather than implied.
func reportUnleased(targets []target) {
	var unleased []string
	for _, t := range targets {
		if t.lease == "" {
			unleased = append(unleased, t.ref)
		}
	}
	if len(unleased) > 0 {
		fmt.Printf("\nno remote-tracking ref for %s, so these are pushed without a lease\n",
			strings.Join(unleased, ", "))
	}
}

// pushArgs builds the push. A ref with a known remote value is leased against
// it, so a remote that moved since the last fetch rejects the push; a ref
// without one is forced, since there is no observed value to compare to.
func pushArgs(remote string, targets []target) []string {
	args := []string{"push", remote}
	for _, t := range targets {
		if t.lease != "" {
			args = append(args, fmt.Sprintf("--force-with-lease=%s:%s", t.ref, t.lease))
		}
	}
	for _, t := range targets {
		if t.lease == "" {
			args = append(args, fmt.Sprintf("+%s:%s", t.ref, t.ref))
			continue
		}
		args = append(args, fmt.Sprintf("%s:%s", t.ref, t.ref))
	}
	return args
}

// listBackups prints the saved refs, grouped by the run that saved them.
func listBackups(repo *gitexec.Repo) error {
	out, err := repo.Output("for-each-ref", "--format=%(refname) %(objectname:short)", backupPrefix)
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) == "" {
		fmt.Println("no backups saved")
		return nil
	}
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		ref, hash, _ := strings.Cut(line, " ")
		saved := strings.TrimPrefix(ref, backupPrefix)
		stamp, original, _ := strings.Cut(saved, "/")
		fmt.Printf("%s  refs/%s  %s\n", stamp, original, hash)
	}
	fmt.Printf("\nrestore one run with: ai-attributions -restore <timestamp>\n")
	return nil
}

// restoreBackup points each saved ref back at the commit it held.
func restoreBackup(repo *gitexec.Repo, stamp string) error {
	// Ref completion offers a trailing slash. Left on, it would build a prefix
	// that matches nothing, and every ref would be restored to a name derived
	// from an untrimmed path while the real branch stayed where it was.
	stamp = strings.Trim(stamp, "/")
	if stamp == "" {
		return fmt.Errorf("-restore needs a backup timestamp; ai-attributions -list-backups shows them")
	}

	prefix := backupPrefix + stamp + "/"
	out, err := repo.Output("for-each-ref", "--format=%(refname) %(objectname)", prefix)
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) == "" {
		return fmt.Errorf("no backup saved under %s%s", backupPrefix, stamp)
	}

	restored := 0
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		ref, hash, _ := strings.Cut(line, " ")
		saved, ok := strings.CutPrefix(ref, prefix)
		if !ok {
			return fmt.Errorf("%s is not under %s, so the ref to restore cannot be worked out", ref, prefix)
		}
		original := "refs/" + saved
		if err := repo.UpdateRef(hash, original); err != nil {
			return err
		}
		fmt.Printf("%s -> %s\n", original, shorten(hash))
		restored++
	}
	if restored == 0 {
		return fmt.Errorf("no backup saved under %s%s", backupPrefix, stamp)
	}
	fmt.Println("\nrestored. A published rewrite still needs a force push to undo on the remote")
	return nil
}

func shorten(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

// sortedByCount renders a tally as lines ordered by descending count.
func sortedByCount(tally map[string]int) []string {
	keys := make([]string, 0, len(tally))
	for key := range tally {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(a, b int) bool {
		if tally[keys[a]] != tally[keys[b]] {
			return tally[keys[a]] > tally[keys[b]]
		}
		return keys[a] < keys[b]
	})
	return keys
}
