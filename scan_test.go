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
// attribution is already moving the commit, so that the blast radius of a
// rewrite is decided by attributions alone.
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
			wantMessage: "tidy the parser: it was unreadable\n",
		},
		{
			name:        "emdash rides along with an agent identity",
			opts:        all,
			who:         who,
			commit:      commit(agent, "a3", "tidy the parser — it was unreadable\n"),
			wantFlagged: 1,
			wantMessage: "tidy the parser: it was unreadable\n",
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
			name:        "emdash is not touched when -no-emdashes is set",
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
