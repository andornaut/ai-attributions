// Command ai-attributions removes AI attributions from a repository's git
// history: co-author and session trailers, "generated with" footers, and the
// emdashes that AI-written commit messages tend to leave behind.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/andornaut/ai-attributions/internal/clean"
	"github.com/andornaut/ai-attributions/internal/gitexec"
	"github.com/andornaut/ai-attributions/internal/rewrite"
)

const usage = `usage: ai-attributions [flags] [repo-path]

Rewrites the commit messages of the current branch, dropping AI attribution
trailers and normalizing emdashes. repo-path defaults to the current directory.

flags:
`

type config struct {
	all        bool
	dryRun     bool
	push       bool
	remote     string
	noBackup   bool
	noTrailers bool
	noEmdashes bool
}

// target is a ref to rewrite, the commit it pointed at beforehand, and the
// value to expect on the remote when pushing. The lease is empty for a ref with
// no remote-tracking counterpart, such as a tag or a branch never pushed.
type target struct {
	ref   string
	hash  string
	lease string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "ai-attributions: %v\n", err)
		os.Exit(1)
	}
}

func run(argv []string) error {
	var cfg config
	flags := flag.NewFlagSet("ai-attributions", flag.ExitOnError)
	flags.BoolVar(&cfg.all, "all", false, "rewrite every local branch and tag, not just the current branch")
	flags.BoolVar(&cfg.dryRun, "dry-run", false, "report what would change without rewriting anything")
	flags.BoolVar(&cfg.push, "push", false, "force push the rewritten refs after a successful rewrite")
	flags.StringVar(&cfg.remote, "remote", "origin", "remote to push to")
	flags.BoolVar(&cfg.noBackup, "no-backup", false, "skip saving the pre-rewrite refs under refs/ai-attributions-backup/")
	flags.BoolVar(&cfg.noTrailers, "no-trailers", false, "leave attribution trailers and footers alone")
	flags.BoolVar(&cfg.noEmdashes, "no-emdashes", false, "leave emdashes alone")
	flags.Usage = func() {
		fmt.Fprint(flags.Output(), usage)
		flags.PrintDefaults()
	}
	if err := flags.Parse(argv); err != nil {
		return err
	}
	if flags.NArg() > 1 {
		return fmt.Errorf("expected at most one repo-path, got %d arguments", flags.NArg())
	}

	opts := clean.Options{Trailers: !cfg.noTrailers, Emdashes: !cfg.noEmdashes}
	if !opts.Trailers && !opts.Emdashes {
		return fmt.Errorf("-no-trailers and -no-emdashes together leave nothing to do")
	}

	path := "."
	if flags.NArg() == 1 {
		path = flags.Arg(0)
	}
	repo, err := gitexec.Open(path)
	if err != nil {
		return err
	}

	refs, err := targetRefs(repo, cfg.all)
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
	if len(commits) == 0 {
		fmt.Println("no commits to inspect")
		return nil
	}

	messages, skipped := report(opts, commits)
	fmt.Printf("\n%d of %d commits need rewriting, across %s\n",
		len(messages), len(commits), strings.Join(refs, ", "))
	if skipped == 1 {
		fmt.Println("1 commit was skipped because its message is not valid UTF-8")
	} else if skipped > 1 {
		fmt.Printf("%d commits were skipped because their messages are not valid UTF-8\n", skipped)
	}
	if len(messages) == 0 {
		return nil
	}
	if cfg.dryRun {
		fmt.Println("dry run: nothing was rewritten")
		return nil
	}

	if err := checkRewritable(repo, cfg); err != nil {
		return err
	}
	targets, err := collectTargets(repo, cfg, refs)
	if err != nil {
		return err
	}
	if err := rewrite.Run(repo, refs, messages); err != nil {
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

// targetRefs returns the refs to scan and rewrite, or nothing when the
// repository has no commits to walk.
func targetRefs(repo *gitexec.Repo, all bool) ([]string, error) {
	if all {
		return repo.ListRefs()
	}
	branch, err := repo.CurrentBranch()
	if err != nil {
		return nil, err
	}
	// A branch with no commits yet is a valid symbolic ref that nothing
	// resolves to, which git log cannot walk.
	if _, err := repo.Resolve(branch); err != nil {
		return nil, nil
	}
	return []string{branch}, nil
}

// report prints the findings for each commit and returns the replacement
// message for every commit that changes, keyed by commit hash, along with the
// number of commits it could not consider.
func report(opts clean.Options, commits []gitexec.Commit) (map[string]string, int) {
	messages := make(map[string]string)
	skipped := 0
	for _, commit := range commits {
		// The rewrite hands messages to git-filter-repo as JSON, which cannot
		// carry bytes that are not valid UTF-8 without replacing them. A
		// legacy-encoded message is left exactly as it is rather than mangled.
		if !utf8.ValidString(commit.Message) {
			fmt.Printf("%s %q\n    ! skipped: the message is not valid UTF-8\n", commit.Short(), commit.Subject())
			skipped++
			continue
		}

		findings := clean.Inspect(opts, commit.Message)
		if findings.Empty() {
			continue
		}
		messages[commit.Hash] = clean.Message(opts, commit.Message)

		fmt.Printf("%s %s\n", commit.Short(), commit.Subject())
		for _, line := range findings.RemovedLines {
			fmt.Printf("    - %s\n", line)
		}
		for _, change := range findings.ChangedLines {
			fmt.Printf("    - %s\n    + %s\n", change.Old, change.New)
		}
	}
	return messages, skipped
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
		saved := fmt.Sprintf("refs/ai-attributions-backup/%s/%s", stamp, strings.TrimPrefix(ref, "refs/"))
		if err := repo.UpdateRef(hash, saved); err != nil {
			return nil, err
		}
	}
	if !cfg.noBackup {
		fmt.Printf("saved the pre-rewrite refs under refs/ai-attributions-backup/%s/\n", stamp)
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

func shorten(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}
