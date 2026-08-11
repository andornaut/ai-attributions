package main

import (
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

// An emdash never moves a commit on its own. It is cleaned up only where an
// attribution is already moving the commit, so the number of commits changing
// hash is decided by attributions alone.
func TestEmdashesRideAlongOnly(t *testing.T) {
	tests := []struct {
		name        string
		opts        clean.Options
		who         identity
		commit      gitexec.Commit
		wantFlagged int
		wantMessage string
	}{
		{
			name:        "emdash alone does not move a commit",
			opts:        all,
			who:         who,
			commit:      commit(person, "a1", "tidy the parser — it was unreadable\n"),
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
			name:        "emdash is left alone when identity rewriting is off",
			opts:        all,
			who:         identity{},
			commit:      commit(agent, "a4", "tidy the parser — it was unreadable\n"),
			wantFlagged: 0,
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

// A commit left alone for having only an emdash is still counted, so that the
// report can say it was a decision.
func TestEmdashesLeftAreCounted(t *testing.T) {
	commits := []gitexec.Commit{
		commit(person, "b1", "tidy the parser — it was unreadable\n"),
		commit(person, "b2", "rename the field — it was misleading\n"),
		commit(person, "b3", "no dashes here\n"),
	}

	found := inspect(all, who, commits)
	if found.emdashesLeft != 2 {
		t.Errorf("emdashesLeft = %d, want 2", found.emdashesLeft)
	}
	if found.flagged != 0 {
		t.Errorf("flagged = %d, want 0", found.flagged)
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
