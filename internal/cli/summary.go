// What a run ended on, and the block that closes it: the counts across every
// repository, and the one thing to do about what they carry.

package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// result is how one repository's run ended and what it contributes to the
// summary. The three flags are properties of the run rather than of the
// repository, which is why they are carried here instead of printed where they
// were noticed: a sweep of thirty would otherwise repeat the same advice
// thirty times, and the repetition is what buries the findings between it.
type result struct {
	ended outcome

	// walked and flagged are the commits read and the commits carrying a
	// finding, which the summary sums across the run.
	walked  int
	flagged int

	// heldCommits and heldRefs record counts whose detail --verbose would have
	// spelled out, and rewritable a scan that found changes an apply would
	// make.
	heldCommits bool
	heldRefs    bool
	rewritable  bool

	// agentPaths are the instruction files the refs in scope carry. The
	// summary names their union, the advice being the same wherever they sit.
	agentPaths []string
}

// summary is what the whole run found. It closes every scan and apply,
// including a run pointed at one repository, so a caller reads the same block
// whatever it was given.
type summary struct {
	op  Op
	cfg Config

	repos  map[outcome]int
	failed int

	walked  int
	flagged int

	// spoke records whether any repository reached the stream. A --quiet run
	// that held all of them back closes on nothing rather than on a summary
	// saying so.
	spoke bool

	heldCommits bool
	heldRefs    bool
	rewritable  bool
	agentPaths  map[string]bool
}

func newSummary(op Op, cfg Config) *summary {
	return &summary{op: op, cfg: cfg, repos: map[outcome]int{}, agentPaths: map[string]bool{}}
}

// add records one repository. spoke is whether its line and report were
// written, which --quiet decides and the counts do not: a repository held back
// is still one the run walked.
func (s *summary) add(got result, spoke bool) {
	s.repos[got.ended]++
	s.walked += got.walked
	s.flagged += got.flagged
	s.heldCommits = s.heldCommits || got.heldCommits
	s.heldRefs = s.heldRefs || got.heldRefs
	s.rewritable = s.rewritable || got.rewritable
	for _, path := range got.agentPaths {
		s.agentPaths[path] = true
	}
	s.spoke = s.spoke || spoke
}

// addFailure records a repository the run could not finish. A failure always
// reaches the stream, so it always closes the run with a summary naming it.
func (s *summary) addFailure() {
	s.failed++
	s.spoke = true
}

// count is the repositories the run walked, however each one ended.
func (s *summary) count() int {
	total := s.failed
	for _, n := range s.repos {
		total += n
	}
	return total
}

// report closes the run. backups, clean and restore have no finding to
// summarize, and a --quiet run that wrote nothing stays that way.
func (s *summary) report() {
	if !s.op.walksHistory() || !s.spoke {
		return
	}
	count := s.count()
	sayBlockf("=== summary\n")
	sayf("%d %s: %s\n", count, plural(count, "repository", "repositories"), s.outcomes())
	// Left out for a single repository, whose report opens on this same count
	// against the refs it answers for, so the summary would repeat it a blank
	// line later.
	if count > 1 {
		sayf("%d of %d commits carry %s\n", s.flagged, s.walked, subject(s.cfg.Emdashes))
	}
	s.advise()
}

// outcomes spells the tally, worst first and leaving out what did not happen,
// so the interesting count is the one the line opens on.
func (s *summary) outcomes() string {
	var parts []string
	if s.failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", s.failed))
	}
	for _, o := range []outcome{outcomeRewrote, outcomeFound, outcomeOutOfScope, outcomeSkipped, outcomeClean} {
		if n := s.repos[o]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, o))
		}
	}
	if len(parts) == 0 {
		return "nothing to report"
	}
	return strings.Join(parts, ", ")
}

// advise says what to do about what the run found, once for the run. The short
// lines close it together, rather than each holding a blank line of its own.
func (s *summary) advise() {
	s.adviseAgentFiles()

	var lines []string
	if s.rewritable {
		lines = append(lines, "nothing was rewritten. Run apply to rewrite the history")
	}
	switch {
	case s.heldCommits && s.heldRefs:
		lines = append(lines, "pass --verbose to list the commits and refs behind these counts")
	case s.heldCommits:
		lines = append(lines, "pass --verbose to list the commits behind these counts")
	case s.heldRefs:
		lines = append(lines, "pass --verbose to list the refs behind these counts")
	}
	if len(lines) == 0 {
		return
	}
	sayf("\n")
	for _, line := range lines {
		sayf("%s\n", line)
	}
}

// adviseAgentFiles names what to do with a committed instruction file. Said
// here rather than beside the counts because no rewrite this tool makes takes
// one out, so the advice answers for the run rather than for a repository.
func (s *summary) adviseAgentFiles() {
	if len(s.agentPaths) == 0 {
		return
	}
	sayf("\nan agent instruction file is a finding of its own: --exit-code exits 1\n")
	sayf("whatever the commit walk reported. These configure a contributor's agent\n")
	sayf("rather than the project, so they belong in a global ignore file. Take one\n")
	sayf("out of the branch that carries it, in each repository counted above:\n")
	for _, path := range slices.Sorted(maps.Keys(s.agentPaths)) {
		sayf("  git rm -r --cached %s\n", path)
	}
	// Said plainly, because the counts name tags as readily as branches and
	// the command above cannot do anything about a tag.
	sayf("\nevery other ref keeps its copy, and so does the history: this tool\n")
	sayf("rewrites messages and identities, never trees\n")
}
