package gitexec

import "testing"

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

func TestProjectMatchesAcrossProtocols(t *testing.T) {
	ssh := project("git@github.com:andornaut/gog.git")
	https := project("https://github.com/andornaut/gog")
	if ssh != https {
		t.Errorf("ssh %q and https %q describe one project but did not match", ssh, https)
	}
}
