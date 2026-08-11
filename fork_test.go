package main

import (
	"os/exec"
	"testing"

	"github.com/andornaut/ai-attributions/internal/gitexec"
)

// gitRepo returns a repository with one commit, and a function that runs git
// in it, for arranging the remotes a fork is recognized by.
func gitRepo(t *testing.T) (*gitexec.Repo, func(args ...string)) {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "--quiet", "--initial-branch=main")
	git("config", "user.name", "Ada")
	git("config", "user.email", "ada@example.com")
	git("commit", "--quiet", "--allow-empty", "--message=init")

	repo, err := gitexec.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo, git
}

// fetched gives a remote a remote-tracking ref, which is what a repository has
// after fetching from it.
func fetched(git func(args ...string), remote string) {
	git("update-ref", "refs/remotes/"+remote+"/main", "HEAD")
}

func TestForkUpstream(t *testing.T) {
	tests := []struct {
		name  string
		setup func(git func(args ...string))
		// own is the remote the current branch tracks.
		own        string
		wantFork   bool
		wantRemote string
	}{
		{
			// The common case for a repository never pushed anywhere, which has
			// to scan rather than report a failure to read the remotes.
			name:  "no remotes is never a fork",
			setup: func(func(args ...string)) {},
			own:   "origin",
		},
		{
			name: "one remote is never a fork",
			setup: func(git func(args ...string)) {
				git("remote", "add", "origin", "git@github.com:andornaut/gog.git")
			},
			own: "origin",
		},
		{
			name: "a second URL on one remote is a mirror, not a fork",
			setup: func(git func(args ...string)) {
				git("remote", "add", "origin", "git@github.com:andornaut/gog.git")
				git("remote", "set-url", "--add", "origin", "https://gitlab.com/andornaut/gog.git")
			},
			own: "origin",
		},
		{
			name: "an upstream remote pointing at the same project is a mirror",
			setup: func(git func(args ...string)) {
				git("remote", "add", "origin", "git@github.com:andornaut/gog.git")
				git("remote", "add", "upstream", "https://github.com/andornaut/gog.git")
				fetched(git, "upstream")
			},
			own: "origin",
		},
		{
			name: "a remote never fetched from is a deploy target, not a fork",
			setup: func(git func(args ...string)) {
				git("remote", "add", "origin", "git@github.com:andornaut/gog.git")
				git("remote", "add", "dokku", "dokku@apps.example.com:gog")
			},
			own: "origin",
		},
		{
			name: "an upstream remote pointing at another project is a fork",
			setup: func(git func(args ...string)) {
				git("remote", "add", "origin", "git@github.com:andornaut/qmk_firmware.git")
				git("remote", "add", "upstream", "https://github.com/qmk/qmk_firmware.git")
				fetched(git, "upstream")
			},
			own:        "origin",
			wantFork:   true,
			wantRemote: "upstream",
		},
		{
			name: "the project compared against is the one the branch tracks",
			setup: func(git func(args ...string)) {
				git("remote", "add", "origin", "https://github.com/qmk/qmk_firmware.git")
				git("remote", "add", "myfork", "git@github.com:andornaut/qmk_firmware.git")
				fetched(git, "origin")
				fetched(git, "myfork")
			},
			own:        "myfork",
			wantFork:   true,
			wantRemote: "origin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, git := gitRepo(t)
			tt.setup(git)

			upstream, isFork, err := forkUpstream(repo, tt.own)
			if err != nil {
				t.Fatal(err)
			}
			if isFork != tt.wantFork {
				t.Fatalf("forkUpstream reported fork=%v, want %v (upstream %q)", isFork, tt.wantFork, upstream.Name)
			}
			if isFork && upstream.Name != tt.wantRemote {
				t.Errorf("forkUpstream named the %s remote, want %s", upstream.Name, tt.wantRemote)
			}
		})
	}
}
