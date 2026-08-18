package cli

import (
	"strings"
	"testing"

	"github.com/andornaut/ai-attributions/internal/gitexec"
)

func TestQuietPrintsOnlyWhatThereIsToAnswerFor(t *testing.T) {
	tests := []struct {
		name       string
		message    string
		wantReport bool
		wantStatus int
	}{
		{
			name:       "a clean repository mails nothing",
			message:    "Add a thing",
			wantReport: false,
			wantStatus: 0,
		},
		{
			name:       "an attribution is what the run is for",
			message:    "Add a thing\n\nCo-Authored-By: Claude <noreply@anthropic.com>\n",
			wantReport: true,
			wantStatus: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, git := gitRepo(t)
			git("commit", "--quiet", "--allow-empty", "--message="+tt.message)

			cfg := Config{Quiet: true, ExitCode: true}
			var status int
			report := captureReport(t, func() {
				var err error
				if status, err = quietRepo(OpScan, cfg, "", repo.Dir()); err != nil {
					t.Fatal(err)
				}
			})

			if got := report != ""; got != tt.wantReport {
				t.Errorf("printed a report=%v, want %v:\n%s", got, tt.wantReport, report)
			}
			if status != tt.wantStatus {
				t.Errorf("exited %d, want %d", status, tt.wantStatus)
			}
		})
	}
}

// The status is what a caller reads when the report is held back, so it has to
// answer the same way with --quiet as without it.
func TestQuietDoesNotChangeTheStatus(t *testing.T) {
	repo, git := gitRepo(t)
	git("commit", "--quiet", "--allow-empty",
		"--message=Add a thing\n\nCo-Authored-By: Claude <noreply@anthropic.com>\n")

	loud := Config{ExitCode: true}
	var want int
	captureReport(t, func() {
		found, err := runRepo(OpScan, loud, "", repo.Dir())
		if err != nil {
			t.Fatal(err)
		}
		want = found.status(loud)
	})

	var got int
	captureReport(t, func() {
		var err error
		if got, err = quietRepo(OpScan, Config{Quiet: true, ExitCode: true}, "", repo.Dir()); err != nil {
			t.Fatal(err)
		}
	})
	if got != want {
		t.Errorf("--quiet exited %d, the same run without it exited %d", got, want)
	}
}

// remoteOnlyRepo is a repository whose refs in scope are clean and whose remote
// carries a branch with an attribution on it.
func remoteOnlyRepo(t *testing.T) *gitexec.Repo {
	t.Helper()
	repo, git := gitRepo(t)
	git("remote", "add", "origin", "https://github.com/example/thing.git")
	git("commit", "--quiet", "--allow-empty",
		"--message=Branch work\n\nCo-Authored-By: Claude <noreply@anthropic.com>\n")
	git("update-ref", "refs/remotes/origin/feature", "HEAD")
	git("reset", "--quiet", "--hard", "HEAD~1")
	return repo
}

// A remote branch carrying attributions is counted in no status, the refs in
// scope being what the status answers for. --quiet weighing the report by its
// outcome would drop the finding entirely, which is the one thing a scheduled
// sweep exists to surface.
func TestQuietKeepsARemoteOnlyFinding(t *testing.T) {
	repo := remoteOnlyRepo(t)

	var status int
	report := captureReport(t, func() {
		var err error
		if status, err = quietRepo(OpScan, Config{Quiet: true, ExitCode: true}, "", repo.Dir()); err != nil {
			t.Fatal(err)
		}
	})

	if !strings.Contains(report, "refs/remotes/origin/feature") {
		t.Errorf("--quiet dropped the remote branch carrying an attribution:\n%s", report)
	}
	// Printing it does not make it count: the status answers for the refs in
	// scope, and a remote branch is not one of them.
	if status != 0 {
		t.Errorf("a remote-only finding exited %d, want 0", status)
	}
}

// A status the caller reads has to name a reason. Without --exit-code there is
// no status to explain, so the same run stays silent.
func TestQuietExplainsANonZeroStatus(t *testing.T) {
	setup := func(t *testing.T) *gitexec.Repo {
		t.Helper()
		repo, git := gitRepo(t)
		git("remote", "add", "origin", "git@github.com:andornaut/qmk_firmware.git")
		git("remote", "add", "upstream", "https://github.com/qmk/qmk_firmware.git")
		fetched(git, "upstream")
		return repo
	}

	repo := setup(t)
	var status int
	report := captureReport(t, func() {
		var err error
		if status, err = quietRepo(OpScan, Config{Quiet: true, ExitCode: true}, "", repo.Dir()); err != nil {
			t.Fatal(err)
		}
	})
	if status != 3 {
		t.Fatalf("a fork exited %d, want 3", status)
	}
	if !strings.Contains(report, "a fork") {
		t.Errorf("exit 3 came with nothing naming the repository or the reason:\n%s", report)
	}

	repo = setup(t)
	silent := captureReport(t, func() {
		if _, err := quietRepo(OpScan, Config{Quiet: true}, "", repo.Dir()); err != nil {
			t.Fatal(err)
		}
	})
	if silent != "" {
		t.Errorf("a fork spoke with no status to explain:\n%s", silent)
	}
}

// A sweep answers for each repository in one line, and a finding on a ref that
// was out of scope has to say so there: an outcome it moves no status for would
// otherwise print "clean" over the only finding the run has, and take the
// report naming the branch down with it.
func TestSweepNamesAFindingOutOfScope(t *testing.T) {
	tidy, _ := gitRepo(t)
	elsewhere := remoteOnlyRepo(t)

	var status int
	report := captureReport(t, func() {
		status = sweep(OpScan, Config{Quiet: true}, "", []string{tidy.Dir(), elsewhere.Dir()})
	})

	if !strings.Contains(report, "out of scope "+elsewhere.Dir()) {
		t.Errorf("the sweep did not name the repository as one carrying a finding out of scope:\n%s", report)
	}
	if !strings.Contains(report, "refs/remotes/origin/feature") {
		t.Errorf("the sweep printed the line and dropped the report naming the branch:\n%s", report)
	}
	if strings.Contains(report, tidy.Dir()) {
		t.Errorf("--quiet named a repository with nothing to answer for:\n%s", report)
	}
	if status != 0 {
		t.Errorf("a finding on a ref out of scope exited %d, want 0", status)
	}
}
