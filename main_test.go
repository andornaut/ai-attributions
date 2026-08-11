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

// --base measures the refs in scope against the history they were cut from, so
// a run answers for the commits they added rather than the ones they inherited.
func TestCommitsInScope(t *testing.T) {
	repo, git := gitRepo(t)
	git("commit", "--quiet", "--allow-empty", "--message=second")
	git("switch", "--quiet", "--create", "agent-work")
	git("commit", "--quiet", "--allow-empty", "--message=third")
	refs := []string{"refs/heads/agent-work"}

	tests := []struct {
		name    string
		cfg     config
		want    []string
		wantErr bool
	}{
		{
			name: "no base walks everything the branch reaches",
			want: []string{"third", "second", "init"},
		},
		{
			name: "a base leaves the inherited commits out",
			cfg:  config{base: "refs/heads/main"},
			want: []string{"third"},
		},
		{
			name: "a base the branch has not moved past leaves nothing",
			cfg:  config{base: "refs/heads/agent-work"},
		},
		{
			name:    "a base that names no commit is an error, not an empty scope",
			cfg:     config{base: "refs/heads/absent"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commits, err := commitsInScope(repo, tt.cfg, refs)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("commitsInScope() accepted a base naming no commit, returning %d commits", len(commits))
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}

			var got []string
			for _, c := range commits {
				got = append(got, c.Subject())
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("commitsInScope() = %v, want %v", got, tt.want)
			}
		})
	}
}

// The scope a report names is the history it answers for, base included, so a
// count cannot be read as covering more than was walked.
func TestScopeLabel(t *testing.T) {
	refs := []string{"refs/heads/agent-work"}
	if got := scopeLabel(config{}, refs); got != "refs/heads/agent-work" {
		t.Errorf("scopeLabel() = %q", got)
	}
	want := "refs/heads/agent-work since origin/main"
	if got := scopeLabel(config{base: "origin/main"}, refs); got != want {
		t.Errorf("scopeLabel() = %q, want %q", got, want)
	}
}

// A tag naming a commit the rewrite moves is repointed with it, whatever refs
// the run was pointed at, since a tag left behind would go on naming history
// nothing else references. --exclude keeps a tag out of the push, not out of
// the rewrite.
func TestCarriedTags(t *testing.T) {
	repo, git := gitRepo(t)
	git("tag", "--annotate", "v1", "--message=release")
	git("tag", "light")
	git("tag", "dev")
	git("commit", "--quiet", "--allow-empty", "--message=second")
	git("tag", "unmoved")

	first, err := repo.Resolve("HEAD~1")
	if err != nil {
		t.Fatal(err)
	}
	moved := map[string]bool{first: true}

	tests := []struct {
		name   string
		cfg    config
		refs   []string
		moved  map[string]bool
		want   []string
		noPush []string
	}{
		{
			name:  "a tag naming a moved commit is carried, an annotated one included",
			refs:  []string{"refs/heads/main"},
			moved: moved,
			want:  []string{"refs/tags/dev", "refs/tags/light", "refs/tags/v1"},
		},
		{
			name:   "an excluded tag is carried, and left out of the push",
			cfg:    config{exclude: refPatterns{"dev"}},
			refs:   []string{"refs/heads/main"},
			moved:  moved,
			want:   []string{"refs/tags/dev", "refs/tags/light", "refs/tags/v1"},
			noPush: []string{"refs/tags/dev"},
		},
		{
			name:  "a tag already in scope is not carried twice",
			refs:  []string{"refs/heads/main", "refs/tags/v1"},
			moved: moved,
			want:  []string{"refs/tags/dev", "refs/tags/light"},
		},
		{
			name:  "nothing moving carries nothing",
			refs:  []string{"refs/heads/main"},
			moved: map[string]bool{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			carried, err := carriedTags(repo, tt.cfg, tt.refs, tt.moved)
			if err != nil {
				t.Fatal(err)
			}

			var got, noPush []string
			for _, c := range carried {
				got = append(got, c.ref)
				if !c.publish {
					noPush = append(noPush, c.ref)
				}
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("carriedTags() = %v, want %v", got, tt.want)
			}
			if !slices.Equal(noPush, tt.noPush) {
				t.Errorf("carriedTags() kept %v out of the push, want %v", noPush, tt.noPush)
			}
		})
	}
}

// A rewrite with a base is pointed at the range each ref adds rather than at
// the ref. git-filter-repo re-exports everything it is given, and a re-exported
// commit loses its signature, so a signed commit the base already carries would
// come out with a new hash and leave the branch sharing no ancestry with it.
func TestRefSpecs(t *testing.T) {
	targets := []target{{ref: "refs/heads/agent-work"}, {ref: "refs/tags/v1"}}

	want := []string{"refs/heads/agent-work", "refs/tags/v1"}
	if got := refSpecs(config{}, targets); !slices.Equal(got, want) {
		t.Errorf("refSpecs() = %v, want %v", got, want)
	}

	want = []string{"origin/main..refs/heads/agent-work", "origin/main..refs/tags/v1"}
	if got := refSpecs(config{base: "origin/main"}, targets); !slices.Equal(got, want) {
		t.Errorf("refSpecs() = %v, want %v", got, want)
	}
}

// The push covers what the rewrite moved and the run owns. Forcing a ref that
// did not move would put a value this run never produced on the remote.
func TestPublishable(t *testing.T) {
	targets := []target{
		{ref: "refs/heads/main", hash: "old", after: "new", publish: true},
		{ref: "refs/heads/untouched", hash: "same", after: "same", publish: true},
		{ref: "refs/tags/v1", hash: "old", after: "new", publish: true},
		{ref: "refs/tags/dev", hash: "old", after: "new"},
		{ref: "refs/tags/unresolvable", hash: "old", publish: true},
	}
	want := []string{"refs/heads/main", "refs/tags/v1"}

	var got []string
	for _, t := range publishable(targets) {
		got = append(got, t.ref)
	}
	if !slices.Equal(got, want) {
		t.Errorf("publishable() = %v, want %v", got, want)
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
