package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andornaut/ai-attributions/internal/gitexec"
)

// stage writes each path and commits them, so a fixture reads as the tree it
// produces rather than as the git commands that got there.
func stage(t *testing.T, repo *gitexec.Repo, git func(args ...string), message string, paths ...string) {
	t.Helper()
	for _, path := range paths {
		full := filepath.Join(repo.Dir(), path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("add", "--all")
	git("commit", "--quiet", "--message="+message)
}

func TestAgentFilesAtRefTips(t *testing.T) {
	repo, git := gitRepo(t)
	stage(t, repo, git, "Add the project", "AGENTS.md", ".cursor/rules/style.mdc", "README.md")
	git("tag", "v1.0.0")

	// A branch that took one back out, so that a ref carrying nothing is told
	// apart from a ref that was never looked at.
	git("checkout", "--quiet", "-b", "feature")
	git("rm", "--quiet", "--cached", "AGENTS.md")
	git("commit", "--quiet", "--message=Stop shipping AGENTS.md")

	refs := []string{"refs/heads/main", "refs/heads/feature", "refs/tags/v1.0.0"}
	found, err := inspectAgentFiles(repo, refs)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string][]string{
		// The directory is reported as it was looked up, not as the file under
		// it, so a rules directory is one finding however many rules it holds.
		".cursor/rules": {"refs/heads/feature", "refs/heads/main", "refs/tags/v1.0.0"},
		"AGENTS.md":     {"refs/heads/main", "refs/tags/v1.0.0"},
	}
	if len(found) != len(want) {
		t.Fatalf("found %v, want %v", found, want)
	}
	for path, refs := range want {
		if got := strings.Join(found[path], " "); got != strings.Join(refs, " ") {
			t.Errorf("%s was found on %q, want %q", path, got, strings.Join(refs, " "))
		}
	}
	if got := found.outcome(); got != outcomeFound {
		t.Errorf("outcome is %v, want %v", got, outcomeFound)
	}
}

func TestNoAgentFilesIsClean(t *testing.T) {
	repo, git := gitRepo(t)
	stage(t, repo, git, "Add the project", "README.md", "docs/agents.md")

	found, err := inspectAgentFiles(repo, []string{"refs/heads/main"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("found %v, want nothing", found)
	}
	if got := found.outcome(); got != outcomeClean {
		t.Errorf("outcome is %v, want %v", got, outcomeClean)
	}
	// Nothing found is nothing said: a clean repository's report is the same
	// report it gave before this check existed.
	if report := captureReport(t, func() { found.report(false) }); report != "" {
		t.Errorf("a clean repository reported:\n%s", report)
	}
}

func TestAgentFilesReportCountsRefsAndNamesTheFix(t *testing.T) {
	found := agentFiles{
		"AGENTS.md":     {"refs/heads/main", "refs/tags/v1.0.0"},
		".cursor/rules": {"refs/heads/main"},
	}

	report := captureReport(t, func() { found.report(false) })
	for _, want := range []string{
		"     2  AGENTS.md",
		"     1  .cursor/rules",
		"git rm -r --cached AGENTS.md",
		"pass --verbose to list the refs behind these counts",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report is missing %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "refs/tags/v1.0.0") {
		t.Errorf("a summary named the refs behind a count:\n%s", report)
	}

	verbose := captureReport(t, func() { found.report(true) })
	if !strings.Contains(verbose, "refs/tags/v1.0.0") {
		t.Errorf("--verbose did not name the refs behind a count:\n%s", verbose)
	}
	if strings.Contains(verbose, "pass --verbose") {
		t.Errorf("--verbose asked for itself:\n%s", verbose)
	}
}
