// Package cli is the command: argument parsing, the report it prints, and the
// scan and rewrite it drives. Everything but the entry point lives here, so
// what main does is call Main and exit on what it returns.
package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/andornaut/ai-attributions/internal/gitexec"
)

// Main runs the command and returns the status to exit with. It returns rather
// than exiting so that the status is a value a test can assert on, which is
// what the suite beside this file does.
func Main(argv []string) int {
	status, err := run(argv)
	if err != nil {
		// Every failure exits 2, the status the flag package already uses, so a
		// caller can tell a finding from a run that could not look.
		reportFailuref("%v", err)
		return 2
	}
	return status
}

func run(argv []string) (int, error) {
	cfg, args, err := parseArgs(argv)
	if err != nil {
		return 0, err
	}
	if cfg.command == "version" {
		sayf("%s\n", version())
		return 0, nil
	}

	stamp := ""
	if cfg.command == "restore" {
		if len(args) == 0 {
			return 0, errors.New("restore needs a backup timestamp; ai-attributions backups lists them")
		}
		stamp, args = args[0], args[1:]
		// The timestamp comes first, so a lone path would otherwise be read as
		// one.
		if !stampRe.MatchString(strings.Trim(stamp, "/")) {
			return 0, fmt.Errorf("restore expects a timestamp like 20260811T121757Z, got %q; ai-attributions backups lists them", stamp)
		}
	}
	if len(args) == 0 {
		args = []string{"."}
	}
	if len(args) > 1 {
		return sweep(cfg, stamp, args), nil
	}

	if cfg.quiet {
		return quietRepo(cfg, stamp, args[0])
	}
	found, err := runRepo(cfg, stamp, args[0])
	if err != nil {
		return 0, err
	}
	return found.status(cfg), nil
}

// worthSaying reports whether a --quiet run has to print what it buffered. A
// finding and a failure are the obvious two. The third is a status the caller
// will read: a non-zero exit with nothing above it names no repository and no
// reason, which is the one answer a scheduled run cannot act on.
func worthSaying(cfg config, ended outcome, err error) bool {
	return err != nil || ended == outcomeFound || noteworthy || ended.status(cfg) != 0
}

// quietRepo runs one repository with the report held in a buffer, and writes it
// out only when there is something to answer for. A failure prints what the run
// got as far as: a run that could not look is not a run that found nothing.
func quietRepo(cfg config, stamp, repoPath string) (int, error) {
	previous, saidBefore := out, said
	buf := &bytes.Buffer{}
	out, noteworthy = buf, false
	found, err := runRepo(cfg, stamp, repoPath)
	out = previous

	if worthSaying(cfg, found, err) {
		_, _ = io.Copy(out, buf)
	} else {
		// Nothing reached the stream after all, so a failure printed later must
		// not be spaced away from a report nobody saw.
		said = saidBefore
	}
	if err != nil {
		return 0, err
	}
	return found.status(cfg), nil
}

// runRepo does one repository's work and reports how it ended, leaving the exit
// status to the caller, which is the only part that differs between a single
// run and a sweep.
func runRepo(cfg config, stamp, repoPath string) (outcome, error) {
	repo, err := gitexec.Open(repoPath)
	if err != nil {
		return outcomeClean, err
	}
	cfg.remote = targetRemote(repo)

	// backups and restore only put back refs this tool saved, so they stay
	// available whatever the repository is.
	switch cfg.command {
	case "backups":
		return outcomeClean, listBackups(repo)
	case "restore":
		return outcomeClean, restoreBackup(repo, stamp)
	}

	upstream, isFork, err := forkUpstream(repo, cfg.remote)
	if err != nil {
		return outcomeClean, err
	}
	if isFork {
		reportFork(repo, upstream)
		if cfg.applying() {
			reportDonef("nothing examined, a fork is not this repository's to rewrite")
		}
		return outcomeSkipped, nil
	}
	return scan(repo, cfg)
}

// sweep runs every path given and prints one line per repository, followed by
// the full report for each one that found something. A repository that fails
// does not end the sweep: the rest are still worth looking at, and a failure
// nobody sees is the reason to keep going rather than stop.
func sweep(cfg config, stamp string, paths []string) int {
	type result struct {
		path   string
		report string
	}

	previous := out
	defer func() { out = previous }()

	var reports []result
	var failed, found, skipped bool
	for _, repoPath := range paths {
		// backups and restore report rather than scan. There is no finding to
		// summarize, and what they print is the whole point of running them, so
		// it goes straight out under a heading instead of being weighed against
		// an outcome none of them produce.
		if !cfg.scanning() {
			sayf("=== %s\n", repoPath)
			if _, err := runRepo(cfg, stamp, repoPath); err != nil {
				failed = true
				reportFailuref("%s: %v", repoPath, err)
			}
			continue
		}

		// Buffered so that a clean repository costs one line rather than a
		// screen, which is what makes a sweep of many readable at all.
		buf := &bytes.Buffer{}
		out, noteworthy = buf, false
		ended, err := runRepo(cfg, stamp, repoPath)
		out = previous

		// The line goes out as each repository finishes, rather than at the
		// end, so that a long sweep says where it has got to. The failure
		// itself goes to stderr, where a single run puts it.
		if err != nil {
			failed = true
			sayf("%s %s\n", column(colorOut, red, "failed"), repoPath)
			reportFailuref("%s: %v", repoPath, err)
			// Whatever the run reported before it failed is still worth having:
			// an apply that failed at the push has already named the backup it
			// saved and the refs it moved.
			reports = append(reports, result{path: repoPath, report: buf.String()})
			continue
		}
		// --quiet keeps the line and the report for a repository worth
		// answering for, and drops both for one that is not, so a sweep of
		// thirty clean checkouts says nothing at all. A fork is nothing to
		// answer for, being the same fork every day, until --exit-code makes it
		// a status the caller reads.
		speak := !cfg.quiet || worthSaying(cfg, ended, nil)
		if speak {
			sayf("%s %s\n", column(colorOut, ended.color(), ended.String()), repoPath)
		}
		switch ended {
		case outcomeFound:
			found = true
		case outcomeSkipped:
			skipped = true
		case outcomeClean:
			// Neither flag: a clean repository is what the exit status reports
			// when nothing else happened.
		}
		if ended != outcomeClean && speak {
			reports = append(reports, result{path: repoPath, report: buf.String()})
		}
	}

	for _, r := range reports {
		if strings.TrimSpace(r.report) == "" {
			continue
		}
		sayf("\n=== %s\n%s", r.path, r.report)
	}

	// Worst first, and decided by what the sweep saw rather than by which
	// repository it saw it in, so the status does not depend on the order the
	// paths were given in.
	switch {
	case failed:
		return 2
	case !cfg.exitCode:
		return 0
	case found:
		return 1
	case skipped:
		return 3
	}
	return 0
}
