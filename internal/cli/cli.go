// Package cli is the work behind the commands: the scan and rewrite they drive,
// and the report they print. The command line itself is cmd's; it calls Execute
// and turns the status into the exit code main leaves on.
package cli

import (
	"bytes"
	"io"
	"strings"

	"github.com/andornaut/ai-attributions/internal/gitexec"
	"github.com/andornaut/ai-attributions/internal/version"
)

// Execute runs cfg against every path given and returns the status to exit
// with. stamp names one saved run, which restore puts back and clean takes
// away, and is empty for every other command. No path means the current
// directory, which is what makes the tool answerable from inside a checkout
// with nothing to name.
func Execute(op Op, cfg Config, stamp string, paths []string) (int, error) {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	run := newSummary(op, cfg)
	if len(paths) > 1 {
		status := sweep(run, op, cfg, stamp, paths)
		run.report()
		return status, nil
	}

	status, err := single(run, op, cfg, stamp, paths[0])
	if err != nil {
		// The failure is the report, and the caller prints it. A summary
		// saying one repository failed would only be in the way of it.
		return 0, err
	}
	run.report()
	return status, nil
}

// Version is the release this binary was built from, or "dev" for one built
// any other way.
func Version() string { return version.Version }

// ValidStamp reports whether s names a backup the way restore expects, which is
// what the timestamp in a `backups` listing looks like.
func ValidStamp(s string) bool { return stampRe.MatchString(strings.Trim(s, "/")) }

// worthSaying reports whether a --quiet run has to print what it buffered. A
// finding and a failure are the obvious two, and a rewrite is the third:
// history that has changed is reported whether or not a status was asked for.
// The fourth is a finding on a ref that was out of scope, which moves no status
// and would otherwise be weighed by one, and the fifth a remote that could not
// be fetched: a scheduled sweep whose network was down has to say it did not
// look, since a silent run reads as an estate with nothing to find. The last is
// a status the caller will read: a non-zero exit with nothing above it names no
// repository and no reason, which is the one answer a scheduled run cannot act
// on.
func worthSaying(cfg Config, ended outcome, err error) bool {
	if err != nil || ended.status(cfg) != 0 {
		return true
	}
	switch ended {
	case outcomeFound, outcomeRewrote, outcomeOutOfScope, outcomeNotFetched:
		return true
	case outcomeClean, outcomeSkipped:
		return false
	}
	return false
}

// single runs one repository. Under --quiet the report is held in a buffer and
// written out only when there is something to answer for. A failure prints what
// the run got as far as: a run that could not look is not a run that found
// nothing.
func single(run *summary, op Op, cfg Config, stamp, repoPath string) (int, error) {
	if !cfg.Quiet {
		got, err := runRepo(op, cfg, stamp, repoPath)
		if err != nil {
			return 0, err
		}
		run.add(got, true)
		return got.ended.status(cfg), nil
	}

	previous, saidBefore := out, said
	buf := &bytes.Buffer{}
	out = buf
	got, err := runRepo(op, cfg, stamp, repoPath)
	out = previous

	speak := worthSaying(cfg, got.ended, err)
	if speak {
		_, _ = io.Copy(out, buf)
	} else {
		// Nothing reached the stream after all, so a failure printed later must
		// not be spaced away from a report nobody saw.
		said = saidBefore
	}
	if err != nil {
		return 0, err
	}
	run.add(got, speak)
	return got.ended.status(cfg), nil
}

// runRepo does one repository's work and reports how it ended, leaving the exit
// status to the caller, which is the only part that differs between a single
// run and a sweep.
func runRepo(op Op, cfg Config, stamp, repoPath string) (result, error) {
	repo, err := gitexec.Open(repoPath)
	if err != nil {
		return result{}, err
	}
	cfg.Remote = targetRemote(repo)

	// backups, clean and restore only read or move refs this tool saved, so
	// they stay available whatever the repository is.
	switch op {
	case OpBackups:
		return result{}, listBackups(repo)
	case OpClean:
		return result{}, cleanBackups(repo, cfg, stamp)
	case OpRestore:
		return result{}, restoreBackup(repo, stamp)
	case OpScan, OpApply:
		// Handled below, once the fork check has had its say.
	}

	upstream, isFork, err := forkUpstream(repo, cfg.Remote)
	if err != nil {
		return result{}, err
	}
	if isFork {
		reportFork(repo, upstream)
		if op.rewrites() {
			reportDonef("nothing examined, a fork is not this repository's to rewrite")
		}
		return result{ended: outcomeSkipped}, nil
	}
	return scan(repo, op, cfg)
}

// sweep runs every path given and prints one line per repository, followed by
// the full report for each one that found something. A repository that fails
// does not end the sweep: the rest are still worth looking at, and a failure
// nobody sees is the reason to keep going rather than stop.
func sweep(run *summary, op Op, cfg Config, stamp string, paths []string) int {
	// pending is a repository's full report, held back until every line has
	// been printed. Named apart from result, which is what a repository's run
	// returns.
	type pending struct {
		path   string
		report string
	}

	previous := out
	defer func() { out = previous }()

	var reports []pending
	var failed, found, skipped bool
	for _, repoPath := range paths {
		// backups, clean and restore report rather than scan. There is no
		// finding to summarize, and what they print is the whole point of
		// running them, so it goes straight out under a heading instead of
		// being weighed against an outcome none of them produce.
		if !op.walksHistory() {
			sayf("=== %s\n", repoPath)
			if _, err := runRepo(op, cfg, stamp, repoPath); err != nil {
				failed = true
				run.addFailure()
				reportFailuref("%s: %v", repoPath, err)
			}
			continue
		}

		// Buffered so that a clean repository costs one line rather than a
		// screen, which is what makes a sweep of many readable at all.
		buf := &bytes.Buffer{}
		out = buf
		got, err := runRepo(op, cfg, stamp, repoPath)
		out = previous
		ended := got.ended

		// The line goes out as each repository finishes, rather than at the
		// end, so that a long sweep says where it has got to. The failure
		// itself goes to stderr, where a single run puts it.
		if err != nil {
			failed = true
			run.addFailure()
			sayf("%s %s\n", column(colorOut, red, "failed"), repoPath)
			reportFailuref("%s: %v", repoPath, err)
			// Whatever the run reported before it failed is still worth having:
			// an apply that failed at the push has already named the backup it
			// saved and the refs it moved.
			reports = append(reports, pending{path: repoPath, report: buf.String()})
			continue
		}
		// --quiet keeps the line and the report for a repository worth
		// answering for, and drops both for one that is not, so a sweep of
		// thirty clean checkouts says nothing at all. A fork is nothing to
		// answer for, being the same fork every day, until --exit-code makes it
		// a status the caller reads.
		speak := !cfg.Quiet || worthSaying(cfg, ended, nil)
		run.add(got, speak)
		if speak {
			sayf("%s %s\n", column(colorOut, ended.color(), ended.String()), repoPath)
		}
		switch ended {
		case outcomeFound, outcomeRewrote:
			found = true
		case outcomeSkipped:
			skipped = true
		case outcomeClean, outcomeOutOfScope, outcomeNotFetched:
			// Neither flag: a clean repository is what the exit status reports
			// when nothing else happened, and a finding on a ref out of scope
			// or a remote that went unread is named in the report rather than
			// counted in the status.
		}
		if ended != outcomeClean && speak {
			reports = append(reports, pending{path: repoPath, report: buf.String()})
		}
	}

	for _, r := range reports {
		if strings.TrimSpace(r.report) == "" {
			continue
		}
		// Trimmed because a report's first block was separated from the
		// sweep's lines rather than from a block of its own, and under a
		// heading that separation is an empty line.
		sayf("\n=== %s\n%s", r.path, strings.TrimLeft(r.report, "\n"))
	}

	// Worst first, and decided by what the sweep saw rather than by which
	// repository it saw it in, so the status does not depend on the order the
	// paths were given in.
	switch {
	case failed:
		return 2
	case !cfg.ExitCode:
		return 0
	case found:
		return 1
	case skipped:
		return 3
	}
	return 0
}
