package main

import (
	"strings"
	"testing"
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

			cfg := config{command: "scan", quiet: true, exitCode: true}
			var status int
			report := captureReport(t, func() {
				var err error
				if status, err = quietRepo(cfg, "", repo.Dir()); err != nil {
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

	loud := config{command: "scan", exitCode: true}
	var want int
	captureReport(t, func() {
		found, err := runRepo(loud, "", repo.Dir())
		if err != nil {
			t.Fatal(err)
		}
		want = found.status(loud)
	})

	var got int
	captureReport(t, func() {
		var err error
		if got, err = quietRepo(config{command: "scan", quiet: true, exitCode: true}, "", repo.Dir()); err != nil {
			t.Fatal(err)
		}
	})
	if got != want {
		t.Errorf("--quiet exited %d, the same run without it exited %d", got, want)
	}
}

func TestQuietBelongsToTheCommandsThatLook(t *testing.T) {
	if _, _, err := parseArgs([]string{"scan", "--quiet"}); err != nil {
		t.Errorf("scan rejected --quiet: %v", err)
	}
	_, _, err := parseArgs([]string{"backups", "--quiet"})
	if err == nil {
		t.Fatal("backups accepted --quiet, which would hide the listing it exists to print")
	}
	if !strings.Contains(err.Error(), "--quiet") {
		t.Errorf("the error does not name the flag: %v", err)
	}
}
