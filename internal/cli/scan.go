// The scan command, and resolving what a run is pointed at.

package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/andornaut/ai-attributions/internal/clean"
	"github.com/andornaut/ai-attributions/internal/gitexec"
)

func scan(repo *gitexec.Repo, cfg config) (outcome, error) {
	who, err := targetIdentity(repo, cfg)
	if err != nil {
		return outcomeClean, err
	}
	refs, err := targetRefs(repo, cfg)
	if err != nil {
		return outcomeClean, err
	}
	// A question about the trees at the tips, so it is asked of the refs
	// themselves rather than of the commits in range: a base that narrows the
	// history to one pull request narrows nothing about what the branch ships.
	// Asked only where --agents-files asks for it, and a run that never looked
	// reports nothing and finds nothing, which an empty set already says.
	var agents agentFiles
	if cfg.agentsFiles {
		agents, err = inspectAgentFiles(repo, refs)
		if err != nil {
			return outcomeClean, err
		}
	}

	commits, err := commitsInScope(repo, cfg, refs)
	if err != nil {
		return outcomeClean, err
	}
	if len(commits) == 0 {
		if cfg.base == "" {
			sayf("no commits to inspect\n")
		} else {
			sayf("no commits to inspect: %s adds nothing over %s\n",
				strings.Join(refs, ", "), cfg.base)
		}
		agents.report(cfg.verbose)
		reportNothingToRewrite(cfg)
		return agents.outcome(), nil
	}
	// Trailers are the point of the tool. Emdashes and endashes are asked for,
	// and asking is what makes one a finding: a run that reports a dash rewrites
	// it too, so what fails a build is what apply takes back out.
	opts := clean.Options{Trailers: true, Emdashes: cfg.emdashes}

	found := inspect(opts, who, commits)
	found.report(cfg.verbose, scopeLabel(cfg, refs))
	agents.report(cfg.verbose)
	moved, err := found.reportRadius(repo, cfg, refs)
	if err != nil {
		return outcomeClean, err
	}
	carried, err := carriedTags(repo, cfg, refs, moved)
	if err != nil {
		return outcomeClean, err
	}
	reportCarried(carried)
	// A remote branch sits outside a range rather than beside it, so a run
	// given a base does not measure one against it.
	if cfg.base == "" {
		if err := reportRemoteOnly(repo, cfg, opts, who, refs); err != nil {
			return outcomeClean, err
		}
	}

	if found.flagged == 0 {
		reportNothingToRewrite(cfg)
		return agents.outcome(), nil
	}
	if !cfg.applying() {
		if len(found.changes) > 0 {
			sayf("\nnothing was rewritten. Run apply to rewrite the history\n")
		}
		return outcomeFound, nil
	}
	// A rewrite that succeeded still reports what it found, so that a job can
	// tell a run that had to change something from one that had nothing to do.
	if err := apply(repo, cfg, refs, carried, found.changes); err != nil {
		return outcomeClean, err
	}
	return outcomeFound, nil
}

// carriedTags returns the tags that name a commit the rewrite moves and are not
// in scope already. A rewritten commit gets a new hash, so a tag left out would
// go on naming history nothing else references. Their commits are in the
// rewrite either way, so carrying the tags repoints them without widening what
// is rewritten.
func carriedTags(repo *gitexec.Repo, cfg config, refs []string, moved map[string]bool) ([]target, error) {
	if len(moved) == 0 {
		return nil, nil
	}
	tags, err := repo.Tags()
	if err != nil {
		return nil, err
	}

	var carried []target
	for _, tag := range tags {
		if !moved[tag.Commit] || slices.Contains(refs, tag.Ref) {
			continue
		}
		excluded, err := cfg.exclude.matches(tag.Ref)
		if err != nil {
			return nil, err
		}
		carried = append(carried, target{ref: tag.Ref, publish: !excluded})
	}
	return carried, nil
}

// forkUpstream returns the remote through which a fork tracks the project it
// was forked from. A remote named upstream is the convention that git and gh
// set up; a remote that has been fetched from and points at another project is
// the general case. Both are measured against own, the remote the current
// branch tracks.
func forkUpstream(repo *gitexec.Repo, own string) (gitexec.Remote, bool, error) {
	remotes, err := repo.Remotes()
	if err != nil {
		return gitexec.Remote{}, false, err
	}
	if len(remotes) < 2 {
		return gitexec.Remote{}, false, nil
	}
	mine := ownProject(remotes, own)

	for _, remote := range remotes {
		if remote.Name == "upstream" && remote.Project != mine {
			return remote, true, nil
		}
	}
	for _, remote := range remotes {
		if remote.Project == mine {
			continue
		}
		// A remote that has never been fetched from, a deploy target for
		// instance, has brought no history here.
		refs, err := repo.RemoteRefs(remote.Name)
		if err != nil {
			return gitexec.Remote{}, false, err
		}
		if len(refs) > 0 {
			return remote, true, nil
		}
	}
	return gitexec.Remote{}, false, nil
}

// ownProject returns the project the named remote points at, which the other
// remotes are compared against. A repository whose branch tracks nothing falls
// back to the first remote.
func ownProject(remotes []gitexec.Remote, own string) string {
	for _, remote := range remotes {
		if remote.Name == own {
			return remote.Project
		}
	}
	return remotes[0].Project
}

// targetRemote is the remote the current branch tracks. A branch pushed
// somewhere other than origin would otherwise be leased and published against
// the wrong remote.
func targetRemote(repo *gitexec.Repo) string {
	if ref, err := repo.CurrentBranch(); err == nil {
		if branch, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
			if remote := repo.Config("branch." + branch + ".remote"); remote != "" {
				return remote
			}
		}
	}
	return "origin"
}

// targetIdentity resolves who agent-authored commits are re-attributed to. A
// bad --identity is always an error, but an unset git identity is only one when
// there is a rewrite to do.
func targetIdentity(repo *gitexec.Repo, cfg config) (identity, error) {
	if cfg.identity == identityNone {
		return identity{}, nil
	}

	if cfg.identity != "" {
		name, address, found := strings.Cut(strings.TrimSuffix(strings.TrimSpace(cfg.identity), ">"), "<")
		who := identity{name: strings.TrimSpace(name), address: strings.TrimSpace(address), enabled: true}
		if !found || !who.resolved() {
			return identity{}, fmt.Errorf("--identity should look like \"Name <email>\" with both parts set, or none, got %q", cfg.identity)
		}
		return who, nil
	}

	who := identity{name: repo.Config("user.name"), address: repo.Config("user.email"), enabled: true}
	if !who.resolved() {
		if cfg.applying() {
			return identity{}, errors.New("the repository has no user.name and user.email to re-attribute to; pass --identity or --identity=none")
		}
		sayf("note: no user.name and user.email are set, so agent identities are reported but cannot be rewritten\n")
		return identity{enabled: true}, nil
	}
	return who, nil
}

// targetRefs returns the refs to scan and rewrite, or nothing when the
// repository has no commits to walk.
func targetRefs(repo *gitexec.Repo, cfg config) ([]string, error) {
	var refs []string
	if cfg.currentBranch {
		branch, err := repo.CurrentBranch()
		if err != nil {
			return nil, err
		}
		// A branch with no commits yet is a valid symbolic ref that nothing
		// resolves to, which git log cannot walk.
		if _, err := repo.Resolve(branch); err != nil {
			return nil, nil //nolint:nilerr // see above: an unborn branch is not an error
		}
		refs = []string{branch}
	} else {
		found, err := repo.ListRefs()
		if err != nil {
			return nil, err
		}
		refs = found
	}

	var kept []string
	for _, ref := range refs {
		if excluded, err := cfg.exclude.matches(ref); err != nil {
			return nil, err
		} else if excluded {
			sayf("excluding %s\n", ref)
			continue
		}
		kept = append(kept, ref)
	}
	return kept, nil
}

// commitsInScope returns the commits to inspect: everything the refs in scope
// reach, or only what they add over --base, which are the commits whoever
// wrote them can still rewrite.
func commitsInScope(repo *gitexec.Repo, cfg config, refs []string) ([]gitexec.Commit, error) {
	// git log with no ref reads HEAD, which is the one thing a repository with
	// no branch does not have.
	if len(refs) == 0 {
		return nil, nil
	}
	if cfg.base == "" {
		return repo.Commits(refs)
	}
	if _, err := repo.Resolve(cfg.base); err != nil {
		return nil, fmt.Errorf("--base %q does not name a commit in this repository; fetch it first", cfg.base)
	}
	return repo.CommitsNotIn([]string{cfg.base}, refs)
}

// scopeLabel names the refs a report answers for and the base they were
// measured against, so a count cannot be read as covering more than was walked.
func scopeLabel(cfg config, refs []string) string {
	scope := strings.Join(refs, ", ")
	if cfg.base == "" {
		return scope
	}
	return scope + " since " + cfg.base
}
