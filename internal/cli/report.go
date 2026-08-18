// Report writing: where output goes, and the outcome a run ends on.

package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/andornaut/ai-attributions/internal/gitexec"
)

// out is where the reports are written. A sweep swaps in a buffer for each
// repository, so that a clean one can be summarized in a line rather than
// printed in full.
var out io.Writer = os.Stdout

// said records whether any report has been written, so that a failure with
// nothing above it does not open the output with a blank line.
var said bool

// say writes one piece of the report. The write error is dropped: a report that
// cannot reach a closed stdout is not a reason to fail a rewrite that worked,
// and every caller would otherwise carry a check it has no answer for.
func sayf(format string, args ...any) {
	said = true
	_, _ = fmt.Fprintf(out, format, args...)
}

// reportDone closes an apply that completed, so that a report ending without
// this line is a run that stopped part way rather than one that finished with
// little to say.
func reportDonef(format string, args ...any) {
	sayf("\n%s %s\n", paint(colorOut, green, "done:"), fmt.Sprintf(format, args...))
}

// reportNothingToRewrite closes an apply that had nothing to do, so that every
// apply that ran to the end says so, whether or not it changed anything.
func reportNothingToRewrite(op Op) {
	if op.rewrites() {
		reportDonef("nothing to rewrite")
	}
}

// reportFailure writes the line that says a run could not complete. It goes to
// stderr, so a caller can redirect the report and still see what went wrong.
func reportFailuref(format string, args ...any) {
	// The blank line separates the failure from the report above it, which it
	// can only do when both land in the same place. A report redirected
	// elsewhere leaves the failure alone on the stream, with nothing above it
	// to be separated from.
	if said && interleaved {
		fmt.Fprintln(os.Stderr)
	}
	fmt.Fprintf(os.Stderr, "%s %s\n",
		paint(colorErr, red, "ai-attributions: error:"), fmt.Sprintf(format, args...))
}

// outcome is how one repository's run ended. It is what a sweep lists per
// repository, and what the exit status is reduced from. Each word names a
// different thing to go and do, which is what a sweep of thirty is read for:
//
//	clean         nothing to do
//	found         attributions on the refs in scope, so apply rewrites them
//	rewrote       an apply moved a ref, so this repository's history has changed
//	out of scope  a finding on a ref the run did not answer for
//	skipped       a fork, which is nobody's to rewrite here
//
// A repository the run declined to examine is deliberately not clean: reporting
// "I did not look" as "nothing to find" is the one answer a sweep must not
// give. Neither is one whose only finding sits on a ref that was out of scope,
// which no status can carry and the line has to name.
type outcome int

const (
	outcomeClean outcome = iota
	outcomeFound
	outcomeRewrote
	outcomeOutOfScope
	outcomeSkipped
)

// outcomes is every word a sweep can print in its first column, which is what
// the column is sized from.
func outcomes() []outcome {
	return []outcome{outcomeClean, outcomeFound, outcomeRewrote, outcomeOutOfScope, outcomeSkipped}
}

func (o outcome) String() string {
	switch o {
	case outcomeFound:
		return "found"
	case outcomeRewrote:
		return "rewrote"
	case outcomeOutOfScope:
		return "out of scope"
	case outcomeSkipped:
		return "skipped"
	default:
		return "clean"
	}
}

// color marks what a sweep's line means without reading it.
func (o outcome) color() string {
	switch o {
	case outcomeFound:
		return yellow
	case outcomeRewrote:
		return cyan
	case outcomeOutOfScope:
		return magenta
	case outcomeSkipped:
		return blue
	default:
		return green
	}
}

// status is the exit status an outcome reduces to. Like git diff --exit-code, a
// finding is reported by the status only when it was asked for; a failure is
// not routed through here, and always exits 2.
func (o outcome) status(cfg Config) int {
	if !cfg.ExitCode {
		return 0
	}
	switch o {
	case outcomeFound, outcomeRewrote:
		return 1
	case outcomeSkipped:
		return 3
	default:
		// outcomeOutOfScope is here with outcomeClean: a ref the run did not
		// answer for cannot move the status, which reports on the refs in
		// scope, the same set apply rewrites. It is named in the report and in
		// the sweep's line instead.
		return 0
	}
}

// reportCarried names the tags that move without being in scope, which the refs
// the run was pointed at do not say by themselves.
func reportCarried(carried []target) {
	if len(carried) == 0 {
		return
	}
	sayf("\ntags naming a commit that changes hash, repointed along with it\n")
	for _, t := range carried {
		if t.publish {
			sayf("  %s\n", t.ref)
			continue
		}
		sayf("  %s (excluded, so it is repointed here and not pushed)\n", t.ref)
	}
}

func reportFork(repo *gitexec.Repo, upstream gitexec.Remote) {
	sayf("%s %s: a fork, tracking %s through the %s remote\n",
		paint(colorOut, blue, "skipping"), repo.Dir(), upstream.Project, upstream.Name)
	sayf("history that arrives from another project is not this repository's to rewrite\n")
}

func reportRewritten(targets []target) {
	sayf("\n")
	for _, t := range targets {
		if !t.moved() {
			continue
		}
		sayf("%s %s -> %s\n", t.ref, gitexec.Short(t.hash), gitexec.Short(t.after))
	}
}

// reportUnleased names the refs that will be pushed without a lease, so the
// weaker guarantee is visible rather than implied.
func reportUnleased(targets []target) {
	unleased := unleasedRefs(targets)
	if len(unleased) > 0 {
		sayf("\nno value on the remote to hold %s to, so these are forced\n",
			strings.Join(unleased, ", "))
	}
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
