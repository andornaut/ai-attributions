// Command ai-attributions removes AI attributions from a repository's git
// history: co-author and session trailers, "generated with" footers, and the
// agent identities on the commits themselves. --emdashes adds the emdashes
// that ride along on those same commits.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/andornaut/ai-attributions/internal/clean"
	"github.com/andornaut/ai-attributions/internal/gitexec"
	"github.com/andornaut/ai-attributions/internal/rewrite"
)

const usage = `usage: ai-attributions [command] [flags] [repo-path]

AI attributions in commits are ads, remove them!

Reports the AI attributions in a repository's history. Nothing is rewritten
unless the apply command asks for it. repo-path defaults to the current
directory.

commands:
  scan                 report what would change (default)
  apply                rewrite the history
  backups              list the pre-rewrite refs saved by earlier runs
  restore <timestamp>  put the refs saved by one run back
  version              print the version

flags:
`

const (
	backupPrefix = "refs/ai-attributions-backup/"
	identityNone = "none"
)

// errFound carries a --exit-code failure without printing anything: the report
// above it has already said what was found.
var errFound = errors.New("attributions found")

// stampRe matches the timestamp a backup is saved under.
var stampRe = regexp.MustCompile(`^\d{8}T\d{6}Z$`)

var commands = map[string]bool{
	"scan": true, "apply": true,
	"backups": true, "restore": true, "version": true,
}

type config struct {
	command  string
	all      bool
	emdashes bool
	exclude  refPatterns
	exitCode bool
	identity string
	push     bool
	verbose  bool

	// remote is resolved from the branch's upstream, rather than assumed to be
	// origin.
	remote string
}

func (c config) applying() bool { return c.command == "apply" }

// target is a ref to rewrite, the commit it pointed at beforehand, and the
// value to expect on the remote when pushing. The lease is empty for a ref with
// no remote-tracking counterpart, such as a tag or a branch never pushed.
type target struct {
	ref   string
	hash  string
	lease string
}

// identity is the name and address to put on a commit an agent authored. Both
// parts are set together or not at all, so a rewrite cannot assign half of one.
type identity struct {
	name    string
	address string
	enabled bool
}

func (i identity) String() string { return i.name + " <" + i.address + ">" }

// resolved reports whether there is an identity to re-attribute to. Scanning
// works without one.
func (i identity) resolved() bool { return i.name != "" && i.address != "" }

// refPatterns collects the repeatable --exclude flag.
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
	cfg, args, err := parseArgs(argv)
	if err != nil {
		return err
	}
	if cfg.command == "version" {
		fmt.Println(version())
		return nil
	}

	stamp := ""
	if cfg.command == "restore" {
		if len(args) == 0 {
			return fmt.Errorf("restore needs a backup timestamp; ai-attributions backups lists them")
		}
		stamp, args = args[0], args[1:]
		// The timestamp comes first, so a lone path would otherwise be read as
		// one.
		if !stampRe.MatchString(strings.Trim(stamp, "/")) {
			return fmt.Errorf("restore expects a timestamp like 20260811T121757Z, got %q; ai-attributions backups lists them", stamp)
		}
	}
	if len(args) > 1 {
		return fmt.Errorf("expected at most one repo-path, got %d", len(args))
	}
	repoPath := "."
	if len(args) == 1 {
		repoPath = args[0]
	}

	repo, err := gitexec.Open(repoPath)
	if err != nil {
		return err
	}
	cfg.remote = targetRemote(repo)

	// backups and restore only put back refs this tool saved, so they stay
	// available whatever the repository is.
	switch cfg.command {
	case "backups":
		return listBackups(repo)
	case "restore":
		return restoreBackup(repo, stamp)
	}

	upstream, isFork, err := forkUpstream(repo, cfg.remote)
	if err != nil {
		return err
	}
	if isFork {
		reportFork(repo, upstream)
		return nil
	}
	return scan(repo, cfg)
}

// parseArgs pulls the command off the front, then parses the flags that follow.
// A first argument that is not a command is a repo-path, so that the common
// case stays "ai-attributions <path>".
func parseArgs(argv []string) (config, []string, error) {
	cfg := config{command: "scan"}
	if len(argv) > 0 && commands[argv[0]] {
		cfg.command, argv = argv[0], argv[1:]
	}

	flags := flag.NewFlagSet("ai-attributions", flag.ExitOnError)
	flags.BoolVar(&cfg.all, "all", false, "every local branch and tag, not just the current branch")
	flags.BoolVar(&cfg.emdashes, "emdashes", false, "also rewrite emdashes, on the commits an attribution is already moving")
	flags.Var(&cfg.exclude, "exclude", "skip refs matching this `glob` (repeatable)")
	flags.BoolVar(&cfg.exitCode, "exit-code", false, "exit 1 when attributions are found, as git diff does (scan only)")
	flags.StringVar(&cfg.identity, "identity", "", "`identity` to put on agent-authored commits, or none to leave them alone (default: the repository's user.name and user.email)")
	flags.BoolVar(&cfg.push, "push", false, "force push the rewritten refs (apply only)")
	flags.BoolVar(&cfg.verbose, "verbose", false, "report every commit rather than a summary")
	flags.Usage = func() {
		_, _ = fmt.Fprint(flags.Output(), usage)
		printFlags(flags.Output(), flags)
	}
	if err := flags.Parse(argv); err != nil {
		return cfg, nil, err
	}

	switch {
	case cfg.push && !cfg.applying():
		return cfg, nil, fmt.Errorf("--push belongs to apply; there is nothing to push until the history is rewritten")
	case cfg.exitCode && cfg.command != "scan":
		return cfg, nil, fmt.Errorf("--exit-code belongs to scan, which reports without changing anything")
	}
	return cfg, flags.Args(), nil
}

// printFlags writes the flag list with the double dashes the documentation
// uses. The standard printer hardcodes one.
func printFlags(out io.Writer, flags *flag.FlagSet) {
	flags.VisitAll(func(f *flag.Flag) {
		kind, usage := flag.UnquoteUsage(f)
		label := "--" + f.Name
		if kind != "" {
			label += " " + kind
		}
		_, _ = fmt.Fprintf(out, "  %-20s %s\n", label, usage)
	})
}

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "devel"
}

func scan(repo *gitexec.Repo, cfg config) error {
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
	// Trailers are the point of the tool. Emdashes are asked for: they move no
	// commit by themselves, so leaving them costs nothing.
	opts := clean.Options{Trailers: true, Emdashes: cfg.emdashes}

	found := inspect(opts, who, commits)
	found.report(cfg.verbose, refs)
	if err := found.reportRadius(repo, refs); err != nil {
		return err
	}
	if err := reportRemoteOnly(repo, cfg, opts, who, refs); err != nil {
		return err
	}

	if found.flagged == 0 {
		return nil
	}
	if !cfg.applying() {
		if len(found.changes) > 0 {
			fmt.Println("\nnothing was rewritten. Run apply to rewrite the history")
		}
		if cfg.exitCode {
			return errFound
		}
		return nil
	}
	return apply(repo, cfg, refs, found.changes)
}

// forkUpstream returns the remote through which a fork tracks the project it
// was forked from. A remote named upstream is the convention that git and gh
// set up; a remote that has been fetched from and points at another project is
// the general case. Both are measured against own, the remote the current
// branch tracks.
func forkUpstream(repo *gitexec.Repo, own string) (gitexec.Remote, bool, error) {
	remotes, err := repo.Remotes()
	if err != nil {
		return gitexec.Remote{}, false, err
	}
	if len(remotes) < 2 {
		return gitexec.Remote{}, false, nil
	}
	mine := ownProject(remotes, own)

	for _, remote := range remotes {
		if remote.Name == "upstream" && remote.Project != mine {
			return remote, true, nil
		}
	}
	for _, remote := range remotes {
		if remote.Project == mine {
			continue
		}
		// A remote that has never been fetched from, a deploy target for
		// instance, has brought no history here.
		refs, err := repo.RemoteRefs(remote.Name)
		if err != nil {
			return gitexec.Remote{}, false, err
		}
		if len(refs) > 0 {
			return remote, true, nil
		}
	}
	return gitexec.Remote{}, false, nil
}

// ownProject returns the project the named remote points at, which the other
// remotes are compared against. A repository whose branch tracks nothing falls
// back to the first remote.
func ownProject(remotes []gitexec.Remote, own string) string {
	for _, remote := range remotes {
		if remote.Name == own {
			return remote.Project
		}
	}
	return remotes[0].Project
}

func reportFork(repo *gitexec.Repo, upstream gitexec.Remote) {
	fmt.Printf("skipping %s: a fork, tracking %s through the %s remote\n",
		repo.Dir(), upstream.Project, upstream.Name)
	fmt.Println("history that arrives from another project is not this repository's to rewrite")
}

// targetRemote is the remote the current branch tracks. A branch pushed
// somewhere other than origin would otherwise be leased and published against
// the wrong remote.
func targetRemote(repo *gitexec.Repo) string {
	if ref, err := repo.CurrentBranch(); err == nil {
		if branch, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
			if remote := repo.Config("branch." + branch + ".remote"); remote != "" {
				return remote
			}
		}
	}
	return "origin"
}

// targetIdentity resolves who agent-authored commits are re-attributed to. A
// bad --identity is always an error, but an unset git identity is only one when
// there is a rewrite to do.
func targetIdentity(repo *gitexec.Repo, cfg config) (identity, error) {
	if cfg.identity == identityNone {
		return identity{}, nil
	}

	if cfg.identity != "" {
		name, address, found := strings.Cut(strings.TrimSuffix(strings.TrimSpace(cfg.identity), ">"), "<")
		who := identity{name: strings.TrimSpace(name), address: strings.TrimSpace(address), enabled: true}
		if !found || !who.resolved() {
			return identity{}, fmt.Errorf("--identity should look like \"Name <email>\" with both parts set, or none, got %q", cfg.identity)
		}
		return who, nil
	}

	who := identity{name: repo.Config("user.name"), address: repo.Config("user.email"), enabled: true}
	if !who.resolved() {
		if cfg.applying() {
			return identity{}, fmt.Errorf("the repository has no user.name and user.email to re-attribute to; pass --identity or --identity=none")
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

// matches tests each pattern against the full ref and its short forms, so
// --exclude dev covers refs/tags/dev and --exclude agent-work covers
// refs/remotes/origin/agent-work.
func (p refPatterns) matches(ref string) (bool, error) {
	candidates := []string{ref}
	for _, prefix := range []string{"refs/heads/", "refs/tags/", "refs/remotes/"} {
		if short, ok := strings.CutPrefix(ref, prefix); ok {
			candidates = append(candidates, short)
			// A remote-tracking ref is still qualified by its remote.
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
				return false, fmt.Errorf("bad --exclude pattern %q: %w", pattern, err)
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
	// Checked before the rewrite rather than after, so a missing remote does
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
// the remote value to hold the push lease against.
func collectTargets(repo *gitexec.Repo, cfg config, refs []string) ([]target, error) {
	stamp := time.Now().UTC().Format("20060102T150405Z")
	targets := make([]target, 0, len(refs))
	for _, ref := range refs {
		hash, err := repo.Resolve(ref)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target{ref: ref, hash: hash, lease: leaseFor(repo, cfg.remote, ref)})

		if err := repo.UpdateRef(hash, backupPrefix+stamp+"/"+strings.TrimPrefix(ref, "refs/")); err != nil {
			return nil, err
		}
	}
	fmt.Printf("saved the pre-rewrite refs under %s%s/\n", backupPrefix, stamp)
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
		fmt.Printf("%s %s -> %s\n", t.ref, gitexec.Short(t.hash), gitexec.Short(hash))
	}
}

// reportUnleased names the refs that will be pushed without a lease, so the
// weaker guarantee is visible rather than implied.
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
// it; a ref without one is forced, since there is no value to compare to.
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
	fmt.Printf("\nput one run back with: ai-attributions restore <timestamp>\n")
	return nil
}

func restoreBackup(repo *gitexec.Repo, stamp string) error {
	// Ref completion offers a trailing slash, which would build a prefix that
	// matches nothing.
	stamp = strings.Trim(stamp, "/")
	if stamp == "" {
		return fmt.Errorf("restore needs a backup timestamp; ai-attributions backups lists them")
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
		fmt.Printf("%s -> %s\n", original, gitexec.Short(hash))
		restored++
	}
	if restored == 0 {
		return fmt.Errorf("no backup saved under %s%s", backupPrefix, stamp)
	}
	fmt.Println("\nrestored. A published rewrite still needs a force push to undo on the remote")
	return nil
}

// sortedByCount orders a tally's keys by descending count.
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
