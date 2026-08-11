package main

import (
	"slices"
	"testing"
)

// A pattern matches the full ref or any of its short forms, so that the name a
// branch or tag is known by works as well as the ref it resolves to.
func TestExcludeMatches(t *testing.T) {
	tests := []struct {
		pattern string
		ref     string
		want    bool
	}{
		{"dev", "refs/heads/dev", true},
		{"dev", "refs/tags/dev", true},
		{"refs/heads/dev", "refs/heads/dev", true},
		{"release/*", "refs/tags/release/1.0", true},
		{"agent-work", "refs/remotes/origin/agent-work", true},
		{"origin/agent-work", "refs/remotes/origin/agent-work", true},
		{"dev", "refs/heads/develop", false},
		{"dev", "refs/heads/feature/dev-notes", false},
		{"agent-work", "refs/heads/agent-work-2", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+" vs "+tt.ref, func(t *testing.T) {
			got, err := refPatterns{tt.pattern}.matches(tt.ref)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("matches(%q) = %v, want %v", tt.ref, got, tt.want)
			}
		})
	}
}

func TestExcludeMatchesRejectsABadPattern(t *testing.T) {
	if _, err := (refPatterns{"["}).matches("refs/heads/main"); err == nil {
		t.Error("matches() accepted an unparseable glob")
	}
}

// A branch is leased against the value the remote held at the last fetch, so a
// remote that has moved since rejects the push. A ref with no remote-tracking
// counterpart has no such value and is forced, which + marks.
func TestPushArgs(t *testing.T) {
	tests := []struct {
		name    string
		targets []target
		want    []string
	}{
		{
			name:    "a branch is leased against its remote-tracking ref",
			targets: []target{{ref: "refs/heads/main", hash: "new", lease: "old"}},
			want: []string{"push", "origin",
				"--force-with-lease=refs/heads/main:old",
				"refs/heads/main:refs/heads/main"},
		},
		{
			name:    "a ref with no lease is forced",
			targets: []target{{ref: "refs/tags/v1", hash: "new"}},
			want:    []string{"push", "origin", "+refs/tags/v1:refs/tags/v1"},
		},
		{
			name: "every lease is stated before any ref is pushed",
			targets: []target{
				{ref: "refs/heads/main", hash: "new", lease: "old"},
				{ref: "refs/tags/v1", hash: "new"},
				{ref: "refs/heads/dev", hash: "new", lease: "older"},
			},
			want: []string{"push", "origin",
				"--force-with-lease=refs/heads/main:old",
				"--force-with-lease=refs/heads/dev:older",
				"refs/heads/main:refs/heads/main",
				"+refs/tags/v1:refs/tags/v1",
				"refs/heads/dev:refs/heads/dev"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pushArgs("origin", tt.targets); !slices.Equal(got, tt.want) {
				t.Errorf("pushArgs() = %v, want %v", got, tt.want)
			}
		})
	}
}
