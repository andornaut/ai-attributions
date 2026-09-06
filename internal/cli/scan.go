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

func scan(repo *gitexec.Repo, op Op, cfg Config) (result, error) {
	who, err := targetIdentity(repo, op, cfg)
	if err != nil {
		return result{}, err
	}
	refs, err := targetRefs(repo, cfg)
	if err != nil {
		return result{}, err
	}
	// A question about the trees at the tips, so it is asked of the refs
	// themselves rather than of the commits in range: a base that narrows the
	// history to one pull request narrows nothing about what the branch ships.
	// Asked only where --agents-files asks for it, and a run that never looked
	// reports nothing and finds nothing, which an empty set already says.
	var agents agentFiles
	if cfg.AgentsFiles {
		agents, err = inspectAgentFiles(repo, refs)
		if err != nil {
			return result{}, err
		}
	}

	commits, err := commitsInScope(repo, cfg, refs)
	if err != nil {
		return result{}, err
	}
	if len(commits) == 0 {
		if cfg.Base == "" {
			sayf("no commits to inspect\n")
		} else {
			sayf("no commits to inspect: %s adds nothing over %s\n",
				strings.Join(refs, ", "), cfg.Base)
		}
		agents.report(cfg.Verbose)
		reportNothingToRewrite(op)
		return result{ended: agents.outcome(), heldRefs: agents.held(cfg.Verbose), agentPaths: agents.paths()}, nil
	}
	// Trailers are the point of the tool. Emdashes and endashes are asked for,
	// and asking is what makes one a finding: a run that reports a dash rewrites
	// it too, so what fails a build is what apply takes back out.
	opts := clean.Options{Trailers: true, Emdashes: cfg.Emdashes}

	found := inspect(opts, who, commits)
	// Read before anything is printed, because what the remote carries decides
	// whether a clean walk here is worth a line of its own. A remote branch
	// sits outside a range rather than beside it, so a run given a base does
	// not measure one against it.
	var elsewhere remoteOnly
	if cfg.Base == "" {
		elsewhere, err = readRemoteOnly(repo, cfg, opts, who, refs)
		if err != nil {
			return result{}, err
		}
	}

	found.report(cfg.Verbose, elsewhere.carries(), scopeLabel(cfg, refs, cfg.Verbose))
	agents.report(cfg.Verbose)
	moved, err := found.reportRadius(repo, cfg, refs)
	if err != nil {
		return result{}, err
	}
	carried, err := carriedTags(repo, cfg, refs, moved)
	if err != nil {
		return result{}, err
	}
	reportCarried(carried)
	elsewhere.report()

	// What the run walked and what it held back, which the summary says once
	// for the run rather than once per repository.
	got := result{
		walked:      found.commits,
		flagged:     found.flagged,
		heldCommits: found.held(cfg.Verbose),
		heldRefs:    agents.held(cfg.Verbose),
		agentPaths:  agents.paths(),
	}

	if found.flagged == 0 {
		reportNothingToRewrite(op)
		got.ended = agents.outcome()
		// Nothing on the refs in scope, and a ref out of scope carrying
		// something: the run ends on that rather than on clean, so a sweep's
		// line names the repository and prints the report under it.
		if got.ended == outcomeClean && elsewhere.any() {
			got.ended = outcomeOutOfScope
		}
		return got, nil
	}
	if !op.rewrites() {
		got.ended = outcomeFound
		got.rewritable = len(found.changes) > 0
		return got, nil
	}
	// A rewrite that succeeded still reports what it found, so that a job can
	// tell a run that had to change something from one that had nothing to do.
	// A finding the rewrite does not touch, an agent instruction file, leaves
	// the history where it was and stays found.
	rewrote, err := apply(repo, cfg, refs, carried, found.changes)
	if err != nil {
		return result{}, err
	}
	got.ended = outcomeFound
	if rewrote {
		got.ended = outcomeRewrote
	}
	return got, nil
}

// carriedTags returns the tags that name a commit the rewrite moves and are not
// in scope already. A rewritten commit gets a new hash, so a tag left out would
// go on naming history nothing else references. Their commits are in the
// rewrite either way, so carrying the tags repoints them without widening what
// is rewritten.
func carriedTags(repo *gitexec.Repo, cfg Config, refs []string, moved map[string]bool) ([]target, error) {
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
		excluded, err := cfg.Exclude.matches(tag.Ref)
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
func targetIdentity(repo *gitexec.Repo, op Op, cfg Config) (identity, error) {
	if cfg.Identity == identityNone {
		return identity{}, nil
	}

	if cfg.Identity != "" {
		name, address, found := strings.Cut(strings.TrimSuffix(strings.TrimSpace(cfg.Identity), ">"), "<")
		who := identity{name: strings.TrimSpace(name), address: strings.TrimSpace(address), enabled: true}
		if !found || !who.resolved() {
			return identity{}, fmt.Errorf("--identity should look like \"Name <email>\" with both parts set, or none, got %q", cfg.Identity)
		}
		return who, nil
	}

	who := identity{name: repo.Config("user.name"), address: repo.Config("user.email"), enabled: true}
	if !who.resolved() {
		if op.rewrites() {
			return identity{}, errors.New("the repository has no user.name and user.email to re-attribute to; pass --identity or --identity=none")
		}
		sayf("note: no user.name and user.email are set, so agent identities are reported but cannot be rewritten\n")
		return identity{enabled: true}, nil
	}
	return who, nil
}

// targetRefs returns the refs to scan and rewrite, or nothing when the
// repository has no commits to walk.
func targetRefs(repo *gitexec.Repo, cfg Config) ([]string, error) {
	var refs []string
	if cfg.CurrentBranch {
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
		if excluded, err := cfg.Exclude.matches(ref); err != nil {
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
func commitsInScope(repo *gitexec.Repo, cfg Config, refs []string) ([]gitexec.Commit, error) {
	// git log with no ref reads HEAD, which is the one thing a repository with
	// no branch does not have.
	if len(refs) == 0 {
		return nil, nil
	}
	if cfg.Base == "" {
		return repo.Commits(refs)
	}
	if _, err := repo.Resolve(cfg.Base); err != nil {
		return nil, fmt.Errorf("--base %q does not name a commit in this repository; fetch it first", cfg.Base)
	}
	return repo.CommitsNotIn([]string{cfg.Base}, refs)
}

// scopeLabel names the refs a report answers for and the base they were
// measured against, so a count cannot be read as covering more than was walked.
func scopeLabel(cfg Config, refs []string, verbose bool) string {
	scope := joinAnd(refScope(refs, verbose))
	if cfg.Base == "" {
		return scope
	}
	return scope + " since " + cfg.Base
}

// maxNamedRefs is how many refs of one kind the scope line names before it
// counts them instead. A repository with twenty releases would otherwise spend
// the line on their names, which is what makes the counts beneath it hard to
// find. --verbose names every one.
const maxNamedRefs = 4

// refScope is how the scope line spells the refs it answers for: branches and
// tags by the names they are known by, and either kind counted once there are
// more of them than the line is worth.
func refScope(refs []string, verbose bool) []string {
	var branches, tags, other []string
	for _, ref := range refs {
		switch {
		case strings.HasPrefix(ref, "refs/heads/"):
			branches = append(branches, shortRef(ref))
		case strings.HasPrefix(ref, "refs/tags/"):
			tags = append(tags, shortRef(ref))
		default:
			other = append(other, ref)
		}
	}
	if verbose {
		return append(append(branches, tags...), other...)
	}
	named := namedOrCounted(branches, "branches")
	named = append(named, namedOrCounted(tags, "tags")...)
	return append(named, namedOrCounted(other, "other refs")...)
}

// namedOrCounted names a handful of refs and counts a crowd of them.
func namedOrCounted(names []string, many string) []string {
	if len(names) > maxNamedRefs {
		return []string{fmt.Sprintf("%d %s", len(names), many)}
	}
	return names
}
