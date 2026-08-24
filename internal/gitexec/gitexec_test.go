package gitexec

import (
	"os"
	"os/exec"
	"testing"
)

// The same project reached different ways has to compare equal, or every
// repository with two URLs for one remote would look like a fork.
func TestProject(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"git@github.com:andornaut/qmk_firmware.git", "github.com/andornaut/qmk_firmware"},
		{"https://github.com/qmk/qmk_firmware", "github.com/qmk/qmk_firmware"},
		{"https://github.com/qmk/qmk_firmware.git", "github.com/qmk/qmk_firmware"},
		{"ssh://git@github.com/andornaut/gog.git", "github.com/andornaut/gog"},
		{"https://user@example.com/team/repo.git", "example.com/team/repo"},
		{"git@github.com:andornaut/gog.git/", "github.com/andornaut/gog"},
		{"github.com:andornaut/gog.git", "github.com/andornaut/gog"},
		{"ssh://git@github.com:22/andornaut/gog.git", "github.com/andornaut/gog"},
		{"git@github.com:Andornaut/Gog.git", "github.com/andornaut/gog"},
		{"/srv/git/mirror.git", "/srv/git/mirror"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := project(tt.url); got != tt.want {
				t.Errorf("project(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

// gitRepo builds a repository with one commit, for the calls that need refs to
// move rather than a string to parse.
func gitRepo(t *testing.T) *Repo {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "--quiet", "--initial-branch=main")
	git("config", "user.name", "Ada")
	git("config", "user.email", "ada@example.com")
	git("commit", "--quiet", "--allow-empty", "--message=init")

	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

// The refs one call is given land together or not at all. A backup saved half
// way through reads like a whole one to whatever puts it back, so a failure has
// to leave the namespace as it was.
func TestUpdateRefsIsOneTransaction(t *testing.T) {
	repo := gitRepo(t)
	hash, err := repo.Resolve("refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}

	if err := repo.UpdateRefs(map[string]string{
		"refs/saved/one": hash,
		"refs/saved/two": hash,
	}); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"refs/saved/one", "refs/saved/two"} {
		if _, err := repo.Resolve(ref); err != nil {
			t.Errorf("%s was not written: %v", ref, err)
		}
	}

	// The second value names no object, so the update it is part of is refused.
	if err := repo.UpdateRefs(map[string]string{
		"refs/saved/three": hash,
		"refs/saved/four":  "0000000000000000000000000000000000000000000000000000000000000000",
	}); err == nil {
		t.Fatal("UpdateRefs() accepted a hash naming no object")
	}
	if _, err := repo.Resolve("refs/saved/three"); err == nil {
		t.Error("a ref from the refused update was written anyway")
	}
}

// DeleteRefs is the same transaction in reverse, and neither is a failure when
// there is nothing to do.
func TestDeleteRefsTakesTheRefsGiven(t *testing.T) {
	repo := gitRepo(t)
	hash, err := repo.Resolve("refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateRefs(map[string]string{"refs/saved/one": hash}); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteRefs([]string{"refs/saved/one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Resolve("refs/saved/one"); err == nil {
		t.Error("the ref is still there after DeleteRefs()")
	}
	if err := repo.UpdateRefs(nil); err != nil {
		t.Errorf("UpdateRefs(nil) = %v, want nothing to do", err)
	}
	if err := repo.DeleteRefs(nil); err != nil {
		t.Errorf("DeleteRefs(nil) = %v, want nothing to do", err)
	}
}
