package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/andornaut/ai-attributions/internal/clean"
	"github.com/andornaut/ai-attributions/internal/gitexec"
)

var (
	person = gitexec.Commit{
		AuthorName: "Ada", AuthorEmail: "ada@example.com",
		CommitterName: "Ada", CommitterEmail: "ada@example.com",
	}
	agent = gitexec.Commit{
		AuthorName: "Claude", AuthorEmail: "noreply@anthropic.com",
		CommitterName: "Claude", CommitterEmail: "noreply@anthropic.com",
	}
	who = identity{name: "Ada", address: "ada@example.com", enabled: true}
	all = clean.Options{Trailers: true, Emdashes: true}
)

// commit returns c with hash and message set.
func commit(c gitexec.Commit, hash, message string) gitexec.Commit {
	c.Hash = hash
	c.Message = message
	return c
}

// An emdash is a finding only where --emdashes asks for one, and then it moves
// a commit of its own accord: what a scan reports is what an apply takes back
// out. Without the flag, an emdash is not looked at.
func TestEmdashesAreFoundOnlyWhenAskedFor(t *testing.T) {
	tests := []struct {
		name        string
		opts        clean.Options
		who         identity
		commit      gitexec.Commit
		wantFlagged int
		wantMessage string
	}{
		{
			name:        "emdash alone moves a commit",
			opts:        all,
			who:         who,
			commit:      commit(person, "a1", "tidy the parser — it was unreadable\n"),
			wantFlagged: 1,
			wantMessage: "tidy the parser - it was unreadable\n",
		},
		{
			name:        "emdash alone is left alone without --emdashes",
			opts:        clean.Options{Trailers: true},
			who:         who,
			commit:      commit(person, "a0", "tidy the parser — it was unreadable\n"),
			wantFlagged: 0,
		},
		{
			name:        "emdash rides along with a trailer",
			opts:        all,
			who:         who,
			commit:      commit(person, "a2", "tidy the parser — it was unreadable\n\nCo-Authored-By: Claude <noreply@anthropic.com>\n"),
			wantFlagged: 1,
			wantMessage: "tidy the parser - it was unreadable\n",
		},
		{
			name:        "emdash rides along with an agent identity",
			opts:        all,
			who:         who,
			commit:      commit(agent, "a3", "tidy the parser — it was unreadable\n"),
			wantFlagged: 1,
			wantMessage: "tidy the parser - it was unreadable\n",
		},
		{
			name:        "emdash is still found when identity rewriting is off",
			opts:        all,
			who:         identity{},
			commit:      commit(agent, "a4", "tidy the parser — it was unreadable\n"),
			wantFlagged: 1,
			wantMessage: "tidy the parser - it was unreadable\n",
		},
		{
			name:        "trailer alone still moves a commit",
			opts:        all,
			who:         who,
			commit:      commit(person, "a5", "tidy the parser\n\nCo-Authored-By: Claude <noreply@anthropic.com>\n"),
			wantFlagged: 1,
			wantMessage: "tidy the parser\n",
		},
		{
			name:        "emdash is not touched unless --emdashes asks for it",
			opts:        clean.Options{Trailers: true},
			who:         who,
			commit:      commit(person, "a6", "tidy the parser — it was unreadable\n\nCo-Authored-By: Claude <noreply@anthropic.com>\n"),
			wantFlagged: 1,
			wantMessage: "tidy the parser — it was unreadable\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found := inspect(tt.opts, tt.who, []gitexec.Commit{tt.commit})

			if found.flagged != tt.wantFlagged {
				t.Fatalf("flagged = %d, want %d", found.flagged, tt.wantFlagged)
			}
			if tt.wantFlagged == 0 {
				if len(found.changes) != 0 {
					t.Errorf("changes = %v, want none", found.changes)
				}
				return
			}
			if got := found.changes[tt.commit.Hash].Message; got != tt.wantMessage {
				t.Errorf("message = %q, want %q", got, tt.wantMessage)
			}
		})
	}
}

// A commit whose only finding is an emdash is rewritten like any other, so a
// build that --emdashes failed has an apply that fixes it.
func TestEmdashOnlyCommitsAreRewritten(t *testing.T) {
	commits := []gitexec.Commit{
		commit(person, "b1", "tidy the parser — it was unreadable\n"),
		commit(person, "b2", "rename the field — it was misleading\n"),
		commit(person, "b3", "no dashes here\n"),
	}

	found := inspect(all, who, commits)
	if found.flagged != 2 {
		t.Errorf("flagged = %d, want 2", found.flagged)
	}
	if len(found.changes) != 2 {
		t.Errorf("changes = %v, want the two commits carrying an emdash", found.changes)
	}
	if found.emdashes != 2 {
		t.Errorf("emdashes = %d, want 2", found.emdashes)
	}

	// The same commits without the flag: nothing looked at is nothing found.
	quiet := inspect(clean.Options{Trailers: true}, who, commits)
	if quiet.flagged != 0 {
		t.Errorf("flagged = %d without --emdashes, want 0", quiet.flagged)
	}
	if len(quiet.changes) != 0 {
		t.Errorf("changes = %v without --emdashes, want none", quiet.changes)
	}
}

// The counts name what put a commit in them, so a report cannot credit an
// emdash to an attribution that is not there.
func TestReportNamesEmdashesOnlyWhenAskedFor(t *testing.T) {
	commits := []gitexec.Commit{commit(person, "e1", "tidy the parser — it was unreadable\n")}

	report := captureReport(t, func() { inspect(all, who, commits).report(false, "refs/heads/main") })
	if !strings.Contains(report, "1 of 1 commits carry AI attributions or dashes") {
		t.Errorf("report did not count the emdash as a finding:\n%s", report)
	}
	if !strings.Contains(report, "dash rewrites\n     1  lines") {
		t.Errorf("report did not tally the emdash rewrite:\n%s", report)
	}

	quiet := captureReport(t, func() {
		inspect(clean.Options{Trailers: true}, who, commits).report(false, "refs/heads/main")
	})
	if !strings.Contains(quiet, "no AI attributions in 1 commits") {
		t.Errorf("report named emdashes without --emdashes:\n%s", quiet)
	}
}

// A message that is not valid UTF-8 cannot be carried through JSON, so it is
// counted and left alone. The identities beside it are still rewritten.
func TestInvalidUTF8MessageIsSkipped(t *testing.T) {
	found := inspect(all, who, []gitexec.Commit{
		commit(agent, "c1", "tidy the parser \xff\xfe — it was unreadable\n\nCo-Authored-By: Claude <noreply@anthropic.com>\n"),
	})

	if found.skipped != 1 {
		t.Errorf("skipped = %d, want 1", found.skipped)
	}
	change := found.changes["c1"]
	if change.Message != "" {
		t.Errorf("message = %q, want it left alone", change.Message)
	}
	if change.AuthorName != who.name || change.AuthorEmail != who.address {
		t.Errorf("author = %q <%q>, want %s", change.AuthorName, change.AuthorEmail, who)
	}
}

// Scanning without a git identity configured reports the agent identities it
// found without anywhere to move them to, so a commit is flagged and counted
// while producing no change to apply. Half an identity is no identity, since a
// rewrite that assigned it would write an empty name or address onto a commit.
func TestIdentityWithoutSomewhereToMoveIt(t *testing.T) {
	tests := []struct {
		name string
		who  identity
	}{
		{"neither name nor address", identity{enabled: true}},
		{"a name and no address", identity{name: "Ada", enabled: true}},
		{"an address and no name", identity{address: "ada@example.com", enabled: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found := inspect(all, tt.who, []gitexec.Commit{commit(agent, "d1", "tidy the parser\n")})

			if found.flagged != 1 {
				t.Errorf("flagged = %d, want 1", found.flagged)
			}
			if len(found.changes) != 0 {
				t.Errorf("changes = %v, want none", found.changes)
			}
			if len(found.identities) != 2 {
				t.Errorf("identities = %v, want the author and the committer", found.identities)
			}
		})
	}
}

// captureReport swaps the report writer for a buffer, so a test can read what a
// run said rather than infer it from what it returned.
func captureReport(t *testing.T, run func()) string {
	t.Helper()
	buf := &bytes.Buffer{}
	previous := out
	out = buf
	defer func() { out = previous }()
	run()
	return buf.String()
}

// A remote-tracking ref left pointing at history this tool already rewrote is a
// push that has not happened, not a branch carrying attributions of its own. The
// cause is only knowable from the backup the rewrite saved, so a run that cannot
// see one states the mechanism rather than guessing at which cause it is.
func TestReportRemoteOnlySeparatesRewrittenHistory(t *testing.T) {
	repo, git := gitRepo(t)
	git("remote", "add", "origin", "git@github.com:andornaut/example.git")
	git("commit", "--quiet", "--allow-empty", "--message=attributed\n\nCo-Authored-By: Claude <noreply@anthropic.com>")

	attributed, err := repo.Resolve("refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	// The remote still holds the commit, as it does until the rewrite is pushed.
	git("update-ref", "refs/remotes/origin/main", attributed)
	// The branch moves off it, as it does once the rewrite has run.
	git("reset", "--quiet", "--hard", "HEAD~1")
	git("commit", "--quiet", "--allow-empty", "--message=rewritten")

	cfg := Config{Remote: "origin"}
	opts := clean.Options{Trailers: true}
	refs := []string{"refs/heads/main"}

	report := captureReport(t, func() {
		if err := reportRemoteOnly(repo, cfg, opts, who, refs); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(report, "not in scope") {
		t.Errorf("with no backup saved, reportRemoteOnly() did not list the branch:\n%s", report)
	}
	if strings.Contains(report, "already rewritten") {
		t.Errorf("with no backup saved, reportRemoteOnly() claimed a rewrite it cannot see:\n%s", report)
	}

	// The record the rewrite leaves behind is what makes the cause knowable.
	git("update-ref", "refs/ai-attributions-backup/20260811T000000Z/heads/main", attributed)

	report = captureReport(t, func() {
		if err := reportRemoteOnly(repo, cfg, opts, who, refs); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(report, "already rewritten") {
		t.Errorf("reportRemoteOnly() did not name the ref as one this repository rewrote:\n%s", report)
	}
	if strings.Contains(report, "not in scope") {
		t.Errorf("reportRemoteOnly() reported rewritten history as a branch to go and clean:\n%s", report)
	}

	// A different branch sitting on main's old tip is not settled by pushing
	// main, so matching the commit alone would suppress a branch nothing cleans.
	git("update-ref", "refs/remotes/origin/topic", attributed)

	report = captureReport(t, func() {
		if err := reportRemoteOnly(repo, cfg, opts, who, refs); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(report, "refs/remotes/origin/topic") {
		t.Errorf("reportRemoteOnly() left out a branch that only shares main's pre-rewrite tip:\n%s", report)
	}
	if !strings.Contains(report, "not in scope") {
		t.Errorf("reportRemoteOnly() did not report the other branch as one to go and clean:\n%s", report)
	}
}
