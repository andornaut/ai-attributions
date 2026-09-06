package cli

import (
	"strings"
	"testing"
)

// The summary answers for every repository the run walked. --quiet decides what
// was printed, not what was counted, so a sweep that held thirty clean
// checkouts back still says there were thirty.
func TestSummaryCountsEveryRepositoryWalked(t *testing.T) {
	run := newSummary(OpScan, Config{})
	run.add(result{ended: outcomeFound, walked: 5, flagged: 2, heldCommits: true, rewritable: true}, true)
	run.add(result{ended: outcomeClean, walked: 36}, false)
	run.add(result{ended: outcomeSkipped}, false)
	run.addFailure()

	report := captureReport(t, run.report)
	for _, want := range []string{
		"=== summary\n",
		"4 repositories: 1 failed, 1 found, 1 skipped, 1 clean\n",
		"2 of 41 commits carry AI attributions\n",
		"nothing was rewritten. Run apply to rewrite the history\n",
		"pass --verbose to list the commits behind these counts\n",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the summary is missing %q:\n%s", want, report)
		}
	}
}

// A run whose findings are already spelled out has nothing left to advise, and
// a single repository's report opens on the count the summary would repeat.
func TestSummaryOfOneRepositoryDoesNotRepeatItsCount(t *testing.T) {
	run := newSummary(OpScan, Config{Verbose: true})
	run.add(result{ended: outcomeFound, walked: 5, flagged: 2}, true)

	report := captureReport(t, run.report)
	if !strings.Contains(report, "1 repository: 1 found\n") {
		t.Errorf("the summary did not name the repository's outcome:\n%s", report)
	}
	if strings.Contains(report, "of 5 commits") {
		t.Errorf("the summary repeated the report's own count:\n%s", report)
	}
	if strings.Contains(report, "pass --verbose") {
		t.Errorf("--verbose asked for itself:\n%s", report)
	}
}

// Nothing reached the stream, so nothing closes it: a --quiet sweep of clean
// checkouts is what mails no report at all.
func TestSummaryIsSilentWhenNothingSpoke(t *testing.T) {
	run := newSummary(OpScan, Config{Quiet: true})
	run.add(result{ended: outcomeClean, walked: 12}, false)

	if report := captureReport(t, run.report); report != "" {
		t.Errorf("a --quiet run with nothing to answer for closed on:\n%s", report)
	}
}

// backups, clean and restore have no finding to summarize, and what they print
// is the whole point of running them.
func TestSummaryStaysOutOfTheReportingCommands(t *testing.T) {
	commands := map[string]Op{"backups": OpBackups, "clean": OpClean, "restore": OpRestore}
	for name, op := range commands {
		run := newSummary(op, Config{})
		run.add(result{}, true)
		if report := captureReport(t, run.report); report != "" {
			t.Errorf("%s closed on a summary:\n%s", name, report)
		}
	}
}
