package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/andornaut/ai-attributions/internal/gitexec"
)

// snapshots adds n commits and returns one snapshot per commit, each naming the
// branch at a different hash, which is what a run hands the backup.
func snapshots(t *testing.T, repo *gitexec.Repo, git func(args ...string), n int) []map[string]string {
	t.Helper()
	saving := make([]map[string]string, 0, n)
	for i := range n {
		git("commit", "--quiet", "--allow-empty", fmt.Sprintf("--message=change %d", i))
		hash, err := repo.Resolve("refs/heads/main")
		if err != nil {
			t.Fatal(err)
		}
		saving = append(saving, map[string]string{"refs/heads/main": hash})
	}
	return saving
}

// saveAll records each snapshot in turn and returns the timestamps they were
// saved under.
func saveAll(t *testing.T, repo *gitexec.Repo, saving []map[string]string) []string {
	t.Helper()
	var stamps []string
	capture(t, func() {
		for _, refs := range saving {
			saved, err := saveBackup(repo, refs)
			if err != nil {
				t.Fatal(err)
			}
			stamps = append(stamps, saved.stamp)
		}
	})
	return stamps
}

func savedStamps(t *testing.T, repo *gitexec.Repo) []string {
	t.Helper()
	runs, err := savedRuns(repo)
	if err != nil {
		t.Fatal(err)
	}
	stamps := make([]string, 0, len(runs))
	for _, run := range runs {
		stamps = append(stamps, run.stamp)
	}
	return stamps
}

// A rewrite leaves a bounded namespace behind without being asked to: it prunes
// the runs before it to the bound and saves its own above it, so a repository
// rewritten every day does not accumulate every ref it ever held.
func TestSaveBackupKeepsTheLastRuns(t *testing.T) {
	repo, git := gitRepo(t)
	stamps := saveAll(t, repo, snapshots(t, repo, git, defaultKeepRuns+2))

	got := savedStamps(t, repo)
	want := stamps[len(stamps)-(defaultKeepRuns+1):]
	if !slices.Equal(got, want) {
		t.Errorf("saved runs are %q, want the last %d: %q", got, defaultKeepRuns+1, want)
	}
}

// Pruning happens before the rewrite rather than after the push, so a backup
// outlives the run that published the rewrite it undoes.
func TestSaveBackupPrunesBeforeTheRewrite(t *testing.T) {
	repo, git := gitRepo(t)
	saving := snapshots(t, repo, git, defaultKeepRuns+2)
	stamps := saveAll(t, repo, saving[:defaultKeepRuns+1])

	report := capture(t, func() {
		if _, err := saveBackup(repo, saving[defaultKeepRuns+1]); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(report, "removed "+stamps[0]) {
		t.Errorf("the run reported %q, want the oldest run removed as it saved its own", report)
	}
}

// A rewrite that reuses a snapshot prunes what one that writes its own would.
// The reused snapshot is this run's, so the bound counts the runs before it
// either way and restore reaches back the same distance.
func TestSaveBackupReusePrunesLikeAWrite(t *testing.T) {
	repo, git := gitRepo(t)
	saving := snapshots(t, repo, git, defaultKeepRuns+1)
	before := saveAll(t, repo, saving)

	capture(t, func() {
		saved, err := saveBackup(repo, saving[len(saving)-1])
		if err != nil {
			t.Fatal(err)
		}
		if saved.wrote {
			t.Error("a snapshot already saved was written a second time")
		}
	})
	if got := savedStamps(t, repo); !slices.Equal(got, before) {
		t.Errorf("saved runs are %q, want the %q a write would have left", got, before)
	}
}

// A rewrite that moves nothing leaves the runs before it exactly where they
// were. Its own snapshot goes, and it cost none of theirs to save: a run that
// recorded nothing must not shorten how far back restore reaches.
func TestSaveBackupKeepsEarlierRunsWhenNothingMoves(t *testing.T) {
	repo, git := gitRepo(t)
	saving := snapshots(t, repo, git, defaultKeepRuns+1)
	before := saveAll(t, repo, saving[:defaultKeepRuns])

	capture(t, func() {
		saved, err := saveBackup(repo, saving[defaultKeepRuns])
		if err != nil {
			t.Fatal(err)
		}
		if err := dropBackupIfUnused(repo, false, saved); err != nil {
			t.Fatal(err)
		}
	})
	if got := savedStamps(t, repo); !slices.Equal(got, before) {
		t.Errorf("saved runs are %q, want the %q that were there before", got, before)
	}
}

// A run whose refs stand where the newest backup saved them writes no second
// copy of it, and the run that did write it keeps it: a snapshot two runs share
// is not the later one's to take away.
func TestSaveBackupReusesAnIdenticalSnapshot(t *testing.T) {
	repo, git := gitRepo(t)
	refs := snapshots(t, repo, git, 1)[0]

	var first, second backup
	report := capture(t, func() {
		var err error
		if first, err = saveBackup(repo, refs); err != nil {
			t.Fatal(err)
		}
		if second, err = saveBackup(repo, refs); err != nil {
			t.Fatal(err)
		}
	})

	if second.stamp != first.stamp || second.wrote {
		t.Errorf("the second run saved %+v, want it to reuse %s without writing", second, first.stamp)
	}
	if !strings.Contains(report, "no second copy is written") {
		t.Errorf("the run reported %q, want it to say the snapshot was reused", report)
	}
	if got := savedStamps(t, repo); !slices.Equal(got, []string{first.stamp}) {
		t.Errorf("saved runs are %q, want the one snapshot %q", got, first.stamp)
	}
}

// A rewrite that moved nothing has nothing to put back, so it leaves no
// snapshot. A reused one belongs to the run that wrote it and stays.
func TestDropBackupIfUnused(t *testing.T) {
	tests := []struct {
		name  string
		moved bool
		wrote bool
		want  int
	}{
		{name: "nothing moved, and this run wrote it", wrote: true, want: 0},
		{name: "a ref moved", moved: true, wrote: true, want: 1},
		{name: "nothing moved, and the snapshot was reused", want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, git := gitRepo(t)
			stamps := saveAll(t, repo, snapshots(t, repo, git, 1))

			capture(t, func() {
				saved := backup{stamp: stamps[0], wrote: tt.wrote}
				if err := dropBackupIfUnused(repo, tt.moved, saved); err != nil {
					t.Fatal(err)
				}
			})
			if got := savedStamps(t, repo); len(got) != tt.want {
				t.Errorf("%d runs saved, want %d", len(got), tt.want)
			}
		})
	}
}

// clean takes a timestamp, a bound, or neither, and a timestamp naming no
// backup is a failure rather than nothing to do.
func TestCleanBackups(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		stamp   func(stamps []string) string
		want    func(stamps []string) []string
		wantErr bool
	}{
		{
			name: "with neither, every backup goes",
			want: func([]string) []string { return nil },
		},
		{
			name: "--keep-last bounds the namespace",
			cfg:  Config{KeepLast: 2},
			want: func(stamps []string) []string { return stamps[1:] },
		},
		{
			name: "--keep-last more than there are removes nothing",
			cfg:  Config{KeepLast: 9},
			want: func(stamps []string) []string { return stamps },
		},
		{
			name:  "a timestamp removes that run alone",
			stamp: func(stamps []string) string { return stamps[1] },
			want:  func(stamps []string) []string { return []string{stamps[0], stamps[2]} },
		},
		{
			// Ref completion offers the trailing slash.
			name:  "a timestamp with a trailing slash",
			stamp: func(stamps []string) string { return stamps[1] + "/" },
			want:  func(stamps []string) []string { return []string{stamps[0], stamps[2]} },
		},
		{
			name:    "a timestamp naming no backup",
			stamp:   func([]string) string { return "20200101T000000Z" },
			want:    func(stamps []string) []string { return stamps },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, git := gitRepo(t)
			// Exactly what a rewrite keeps, so that what is under test is what
			// clean removes rather than what saving pruned.
			stamps := saveAll(t, repo, snapshots(t, repo, git, defaultKeepRuns))

			stamp := ""
			if tt.stamp != nil {
				stamp = tt.stamp(stamps)
			}
			var err error
			capture(t, func() { err = cleanBackups(repo, tt.cfg, stamp) })
			if (err != nil) != tt.wantErr {
				t.Fatalf("cleanBackups() error = %v, want an error: %v", err, tt.wantErr)
			}
			if got, want := savedStamps(t, repo), tt.want(stamps); !slices.Equal(got, want) {
				t.Errorf("saved runs are %q, want %q", got, want)
			}
		})
	}
}

// The listing names each saved ref by the ref it was saved for, and abbreviates
// the commit the way every other report does: backups and restore naming one
// commit at two widths would read as two commits.
func TestListBackupsNamesTheRefsARunSaved(t *testing.T) {
	repo, git := gitRepo(t)
	git("tag", "v1")
	hash, err := repo.Resolve("refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	stamps := saveAll(t, repo, []map[string]string{{"refs/heads/main": hash, "refs/tags/v1": hash}})

	report := capture(t, func() {
		if err := listBackups(repo); err != nil {
			t.Fatal(err)
		}
	})
	for _, ref := range []string{"refs/heads/main", "refs/tags/v1"} {
		want := fmt.Sprintf("%s  %s  %s\n", stamps[0], ref, gitexec.Short(hash))
		if !strings.Contains(report, want) {
			t.Errorf("backups reported\n%s\nwant a line %q", report, want)
		}
	}
}

func TestCleanBackupsWithNoneSaved(t *testing.T) {
	repo, _ := gitRepo(t)
	report := capture(t, func() {
		if err := cleanBackups(repo, Config{}, ""); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(report, "no backups saved") {
		t.Errorf("clean reported %q, want it to say there is nothing saved", report)
	}

	// A timestamp naming no backup is a failure whether the repository holds
	// other backups or none at all, as restore's is: a sweep that read one as
	// nothing to do would exit 0 on a timestamp nothing matched anywhere.
	var err error
	capture(t, func() { err = cleanBackups(repo, Config{}, "20200101T000000Z") })
	if err == nil {
		t.Error("clean took a timestamp naming no backup for nothing to do")
	}
}

// Two rewrites in one second would otherwise share a timestamp, and the second
// would overwrite the first's refs in place, leaving one run holding a mixture
// of both. A stamp is moved past the newest saved rather than only off the ones
// still there, so a run cannot sort in among the runs that came before it.
func TestFreeStamp(t *testing.T) {
	now := time.Date(2026, 8, 19, 4, 17, 33, 0, time.UTC)
	tests := []struct {
		name  string
		taken []savedRun
		want  string
	}{
		{
			name: "nothing saved takes the time now",
			want: "20260819T041733Z",
		},
		{
			name:  "the same second as the newest moves on",
			taken: []savedRun{{stamp: "20260819T041732Z"}, {stamp: "20260819T041733Z"}},
			want:  "20260819T041734Z",
		},
		{
			// The newest run was saved by a rewrite the clock has not caught up
			// with, several of them having run inside one second.
			name:  "ahead of the clock moves on from there",
			taken: []savedRun{{stamp: "20260819T041736Z"}},
			want:  "20260819T041737Z",
		},
		{
			name:  "behind the newest takes the time now",
			taken: []savedRun{{stamp: "20260819T041700Z"}},
			want:  "20260819T041733Z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := freeStamp(tt.taken, now)
			if got != tt.want {
				t.Errorf("freeStamp() = %q, want %q", got, tt.want)
			}
			if !ValidStamp(got) {
				t.Errorf("freeStamp() = %q, which restore does not accept", got)
			}
		})
	}
}

// A run's refs are read back under the ref they were saved for, so restore and
// the comparison that reuses a snapshot both work in the names the repository
// uses rather than in the backup's own.
func TestSavedRunsGroupsRefsByRun(t *testing.T) {
	repo, git := gitRepo(t)
	git("tag", "v1")
	hash, err := repo.Resolve("refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	saving := map[string]string{"refs/heads/main": hash, "refs/tags/v1": hash}
	stamps := saveAll(t, repo, []map[string]string{saving})

	runs, err := savedRuns(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("%d runs saved, want 1", len(runs))
	}
	if runs[0].stamp != stamps[0] {
		t.Errorf("the run is saved under %q, want %q", runs[0].stamp, stamps[0])
	}
	if got := runs[0].refs; !maps.Equal(got, saving) {
		t.Errorf("the run holds %v, want %v", got, saving)
	}
}
