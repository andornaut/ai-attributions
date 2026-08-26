package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/andornaut/ai-attributions/internal/clean"
	"github.com/andornaut/ai-attributions/internal/gitexec"
	"github.com/andornaut/ai-attributions/internal/rewrite"
)

type findings struct {
	changes    map[string]rewrite.Change
	removed    map[string]int
	identities map[string]int
	details    []detail
	emdashes   int
	commits    int
	skipped    int
	// flagged counts commits carrying a finding, which is not the same as
	// len(changes): an agent identity with nowhere to move to is reported and
	// counted, but produces no change.
	flagged int
	// emdashesAsked records whether --emdashes put the dashes it covers in
	// scope, which is what the counts have to name themselves after: the same
	// tally answers a different question with the flag on.
	emdashesAsked bool
}

// detail is one commit's findings, kept for -verbose.
type detail struct {
	commit       gitexec.Commit
	removedLines []string
	changedLines []clean.LineChange
	identities   []string
}

// inspect works out what each commit needs, without changing anything.
func inspect(opts clean.Options, who identity, commits []gitexec.Commit) findings {
	found := findings{
		changes:       map[string]rewrite.Change{},
		removed:       map[string]int{},
		identities:    map[string]int{},
		commits:       len(commits),
		emdashesAsked: opts.Emdashes,
	}

	for _, commit := range commits {
		var change rewrite.Change
		item := detail{commit: commit}
		flagged := false

		// The rewrite carries messages as JSON, which cannot hold bytes that are
		// not valid UTF-8. Such a message is left as it is, though the
		// identities beside it can still be fixed.
		var message string
		var got clean.Findings
		if utf8.ValidString(commit.Message) {
			message, got = clean.Apply(opts, commit.Message)
		} else {
			found.skipped++
		}

		// A trailer moves a commit whatever else the message carries. Dashes
		// are taken separately, below, because they are only there to be taken
		// where --emdashes asked.
		if len(got.RemovedLines) > 0 {
			flagged = true
			item.removedLines = got.RemovedLines
			for _, line := range got.RemovedLines {
				found.removed[strings.TrimSpace(line)]++
			}
		}

		if who.enabled {
			if clean.Identity(commit.AuthorName, commit.AuthorEmail) {
				flagged = true
				item.identities = append(item.identities,
					mapping("author", commit.AuthorName, commit.AuthorEmail, who))
				if who.resolved() {
					change.AuthorName, change.AuthorEmail = who.name, who.address
				}
			}
			if clean.Identity(commit.CommitterName, commit.CommitterEmail) {
				flagged = true
				item.identities = append(item.identities,
					mapping("committer", commit.CommitterName, commit.CommitterEmail, who))
				if who.resolved() {
					change.CommitterName, change.CommitterEmail = who.name, who.address
				}
			}
			for _, label := range item.identities {
				found.identities[label]++
			}
		}

		// An emdash or an endash is a reason to rewrite a commit only where
		// --emdashes asked for one: a message carries no changed lines
		// otherwise. Asking makes it a finding of its own rather than a tidy-up
		// riding along on a commit an attribution already moves, so a scan can
		// fail on a dash and the apply it names is what takes it back out.
		if len(got.ChangedLines) > 0 {
			flagged = true
		}
		if flagged && !got.Empty() {
			change.Message = message
			item.changedLines = got.ChangedLines
			found.emdashes += len(got.ChangedLines)
		}

		if change != (rewrite.Change{}) {
			found.changes[commit.Hash] = change
		}
		if flagged {
			found.flagged++
			found.details = append(found.details, item)
		}
	}
	return found
}

func mapping(field, name, address string, who identity) string {
	if !who.resolved() {
		return fmt.Sprintf("%s %s <%s> (no identity to move it to)", field, name, address)
	}
	return fmt.Sprintf("%s %s <%s> -> %s", field, name, address, who)
}

// report prints the tallies, and every commit behind them under -verbose.
// scope names the history the counts answer for. elsewhere drops the line a
// clean walk would otherwise print: a run whose only finding sits on a ref out
// of scope opens on that finding, rather than on a clean line the reader has to
// get past to reach it.
func (f findings) report(verbose, elsewhere bool, scope string) {
	if f.flagged == 0 {
		if !elsewhere {
			sayf("no %s in %d commits, across %s\n", subject(f.emdashesAsked), f.commits, scope)
		}
		f.reportSkipped()
		return
	}

	if verbose {
		for _, item := range f.details {
			sayf("%s %s\n", item.commit.Short(), item.commit.Subject())
			for _, line := range item.removedLines {
				sayf("    - %s\n", line)
			}
			for _, change := range item.changedLines {
				sayf("    - %s\n    + %s\n", change.Old, change.New)
			}
			for _, label := range item.identities {
				sayf("    ~ %s\n", label)
			}
		}
		sayf("\n")
	}

	sayf("%d of %d commits carry %s, across %s\n", f.flagged, f.commits, subject(f.emdashesAsked), scope)
	f.reportTally("removed lines", f.removed)
	f.reportTally("identities", f.identities)
	if f.emdashes > 0 {
		sayf("\ndash rewrites\n%6d  lines\n", f.emdashes)
	}
	f.reportSkipped()
	if !verbose {
		sayf("\npass --verbose to list the commits behind these counts\n")
	}
}

func (f findings) reportTally(title string, tally map[string]int) {
	if len(tally) == 0 {
		return
	}
	sayf("\n%s\n", title)
	for _, key := range sortedByCount(tally) {
		sayf("%6d  %s\n", tally[key], key)
	}
}

// subject names what a count is counting. --emdashes widens it: an emdash or an
// endash is a finding in its own right once it is asked for, so a report that
// went on saying "AI attributions" would be naming a cause the counts no longer
// have. "dashes" rather than either mark by name, since the flag covers both.
func subject(emdashes bool) string {
	if emdashes {
		return "AI attributions or dashes"
	}
	return "AI attributions"
}

func (f findings) reportSkipped() {
	switch {
	case f.skipped == 1:
		sayf("\n1 commit message was skipped because it is not valid UTF-8\n")
	case f.skipped > 1:
		sayf("\n%d commit messages were skipped because they are not valid UTF-8\n", f.skipped)
	}
}

// agentFiles are the instruction files the refs in scope carry, as a path to
// the refs holding it. Keyed by path rather than by ref, because the same file
// on twenty tags is one file to take out, not twenty findings.
type agentFiles map[string][]string

// inspectAgentFiles looks for an agent's instruction files at the tip of every
// ref in scope. Nothing here is ever rewritten: these are files in a tree, and
// the rewrite replaces messages and identities.
func inspectAgentFiles(repo *gitexec.Repo, refs []string) (agentFiles, error) {
	byRef, err := repo.PathsAtRefs(refs, clean.AgentFiles())
	if err != nil {
		return nil, err
	}

	found := agentFiles{}
	for ref, paths := range byRef {
		for _, path := range paths {
			found[path] = append(found[path], ref)
		}
	}
	for _, refs := range found {
		slices.Sort(refs)
	}
	return found, nil
}

// outcome is how a run with nothing to rewrite ended. A committed instruction
// file --agents-files asked about is a finding in its own right: no rewrite
// this tool makes takes it back out, so a run that stayed quiet about it would
// leave the one thing it found to be noticed by hand.
func (a agentFiles) outcome() outcome {
	if len(a) > 0 {
		return outcomeFound
	}
	return outcomeClean
}

// report names the instruction files, the refs carrying them, and how to take
// one out.
func (a agentFiles) report(verbose bool) {
	if len(a) == 0 {
		return
	}
	paths := slices.Sorted(maps.Keys(a))

	sayf("\nagent instruction files, counted by the refs in scope that carry them\n")
	for _, path := range paths {
		sayf("%6d  %s\n", len(a[path]), path)
		if verbose {
			for _, ref := range a[path] {
				sayf("        %s\n", ref)
			}
		}
	}

	// Said here because the report above this one answers for commits, and a
	// run whose commits were clean would otherwise open on a clean line and
	// close on a status nothing in the report accounted for.
	sayf("\na file here is a finding of its own: --exit-code exits 1 whatever the\n")
	sayf("commit walk above reported\n")

	sayf("\nthese configure a contributor's agent rather than the project, so they\n")
	sayf("belong in a global ignore file. Take one out of the branch that carries it:\n")
	for _, path := range paths {
		sayf("  git rm -r --cached %s\n", path)
	}
	// Said plainly, because the counts above name tags as readily as branches
	// and the command above cannot do anything about a tag.
	sayf("\nevery other ref keeps its copy, and so does the history: this tool\n")
	sayf("rewrites messages and identities, never trees\n")
	if !verbose {
		sayf("\npass --verbose to list the refs behind these counts\n")
	}
}

// reportRadius names how many commits the rewrite moves, and returns every
// commit that changes hash. Every descendant of a changed commit gets a new
// hash, so the set is what a ref pointing into this history has to be measured
// against.
func (f findings) reportRadius(repo *gitexec.Repo, cfg Config, refs []string) (map[string]bool, error) {
	moved := map[string]bool{}
	if len(f.changes) == 0 {
		return moved, nil
	}
	sayf("\n")
	for _, ref := range refs {
		// The same range the commits were read from. A change can only be in
		// scope, so nothing below the base can be dirty, and walking to the
		// root would read a whole history to count a branch's few commits.
		graph, err := repo.Graph(rangeSpec(cfg, ref))
		if err != nil {
			return nil, err
		}

		dirty := make(map[string]bool, len(graph))
		earliest := ""
		// Reversed, so that a commit is decided after its parents are.
		for _, entry := range slices.Backward(graph) {
			hash, parents := entry[0], entry[1:]
			if _, changed := f.changes[hash]; !changed {
				changed = false
				for _, parent := range parents {
					if dirty[parent] {
						changed = true
						break
					}
				}
				if !changed {
					continue
				}
			}
			dirty[hash] = true
			moved[hash] = true
			if earliest == "" {
				earliest = hash
			}
		}
		if earliest == "" {
			continue
		}
		sayf("%s: %d of %d commits will change hash, starting at %s %s\n",
			ref, len(dirty), len(graph), gitexec.Short(earliest), repo.Describe(earliest))
	}
	return moved, nil
}

// remoteOnly is what the remote carries that the refs in scope do not answer
// for. Nothing here counts toward the run's findings or its exit code, which
// answer for the refs in scope, the same set apply rewrites.
type remoteOnly struct {
	// branches carry attributions and are not reached by the refs in scope.
	branches []string
	// stale name history an apply here has already rewritten, which is a push
	// that has not happened rather than a branch of its own to go and clean.
	stale []string
	// fetchErr is the failure that left the refs below as of the last fetch,
	// and nil for a remote that answered.
	fetchErr error

	remote  string
	subject string
}

// any reports whether there is anything to say, a failed fetch included. A run
// whose refs in scope were clean ends on this rather than on clean, so a sweep
// names the repository and prints the report under it: a remote answered from
// the last fetch is as much a thing to go and do as a branch to clean.
func (r remoteOnly) any() bool {
	return r.carries() || r.fetchErr != nil
}

// carries reports whether the remote holds work of its own, which is the case
// a clean line about the refs in scope would be read past rather than read.
func (r remoteOnly) carries() bool {
	return len(r.branches) > 0 || len(r.stale) > 0
}

// fetchRemote refreshes a remote before its refs are read. Held in a variable
// so the tests can exercise the report against remotes that were never meant to
// be reachable.
var fetchRemote = (*gitexec.Repo).Fetch

// readRemoteOnly reads what the remote carries beyond the refs in scope, rather
// than rewriting refs the tool was not pointed at. The remote is fetched first,
// so a branch deleted since the last fetch is not reported as one to go and
// clean and one pushed since is. A remote that cannot be reached is reported
// rather than failed on: the refs already here still answer for what was last
// seen, and a rewrite is local work that an unreachable host is no reason to
// refuse.
func readRemoteOnly(repo *gitexec.Repo, cfg Config, opts clean.Options, who identity, localRefs []string) (remoteOnly, error) {
	found := remoteOnly{remote: cfg.Remote, subject: subject(opts.Emdashes)}
	if !repo.HasRemote(cfg.Remote) {
		return remoteOnly{}, nil
	}
	found.fetchErr = fetchRemote(repo, cfg.Remote)
	remoteRefs, err := repo.RemoteRefs(cfg.Remote)
	if err != nil {
		return remoteOnly{}, err
	}

	// A ref left behind by this tool's own rewrite is separated out below, so
	// that the history the run just replaced is not reported as a branch of its
	// own to go and clean.
	saved := rewrittenHere(repo)

	for _, ref := range remoteRefs {
		excluded, err := cfg.Exclude.matches(ref)
		if err != nil {
			return remoteOnly{}, err
		}
		if excluded {
			continue
		}
		// A ref that cannot be walked is called out rather than skipped, so an
		// unreadable branch does not read as a clean one.
		commits, err := repo.CommitsNotIn(localRefs, []string{ref})
		if err != nil {
			found.branches = append(found.branches, fmt.Sprintf("%6s  %s (%v)", "?", ref, err))
			continue
		}
		if len(commits) == 0 {
			continue
		}
		if got := inspect(opts, who, commits); got.flagged > 0 {
			if hash, err := repo.Resolve(ref); err == nil &&
				saved[rewrittenKey(strings.TrimPrefix(ref, "refs/remotes/"+cfg.Remote+"/"), hash)] {
				found.stale = append(found.stale, ref)
				continue
			}
			found.branches = append(found.branches, fmt.Sprintf("%6d of %d commits  %s",
				got.flagged, len(commits), ref))
		}
	}
	return found, nil
}

// report names the work that is still to do on a ref that was not in scope.
// None of it moves the status, so the report is the only place it is said.
func (r remoteOnly) report() {
	if r.fetchErr != nil {
		sayBlockf("could not fetch %s, so the remote is reported as of the last fetch: %v\n",
			r.remote, r.fetchErr)
	}
	if len(r.stale) > 0 {
		sayBlockf("naming history this repository has already rewritten locally: %s\n",
			strings.Join(r.stale, ", "))
		sayf("pushing the rewrite settles these; until then the remote still holds what it started from\n")
	}
	if len(r.branches) == 0 {
		return
	}
	sayBlockf("remote branches carrying %s, outside the refs in scope and counted in no status\n", r.subject)
	for _, line := range r.branches {
		sayf("%s\n", line)
	}
}

// rewrittenHere returns the branches this tool has rewritten, each keyed with
// the commit it pointed at beforehand. A remote-tracking ref still naming its
// own branch's pre-rewrite tip is a push that has not happened; one that merely
// sits on some other ref's old tip is a branch of its own, and pushing this
// rewrite would not move it, so it is left to be reported as one.
//
// The backups are what this reads, so it answers for as far back as they reach:
// once pruning or clean has taken a run away, the rewrite it saved reports as a
// remote branch carrying attributions rather than as one already rewritten
// here.
func rewrittenHere(repo *gitexec.Repo) map[string]bool {
	saved := map[string]bool{}
	// A repository whose backups cannot be read is one this has nothing to say
	// about, and the branches are still reported without it.
	runs, err := savedRuns(repo)
	if err != nil {
		return saved
	}
	for _, run := range runs {
		for ref, hash := range run.refs {
			// A tag has no remote-tracking counterpart to be compared against,
			// so only the branches a run saved are keyed here.
			if branch, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
				saved[rewrittenKey(branch, hash)] = true
			}
		}
	}
	return saved
}

func rewrittenKey(branch, hash string) string { return branch + " " + hash }
