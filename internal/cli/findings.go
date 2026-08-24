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
// scope names the history the counts answer for.
func (f findings) report(verbose bool, scope string) {
	if f.flagged == 0 {
		sayf("no %s in %d commits, across %s\n", subject(f.emdashesAsked), f.commits, scope)
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

// reportRemoteOnly names remote branches that carry attributions and are not
// covered by the refs in scope, rather than rewriting refs the tool was not
// pointed at. It reads remote-tracking refs, so a scan needs no network.
// Nothing it finds counts toward the run's findings or its exit code, which
// answer for the refs in scope, the same set apply rewrites. It reports whether
// it named anything, which is what the run ends on when the refs in scope had
// nothing to say.
func reportRemoteOnly(repo *gitexec.Repo, cfg Config, opts clean.Options, who identity, localRefs []string) (bool, error) {
	if !repo.HasRemote(cfg.Remote) {
		return false, nil
	}
	remoteRefs, err := repo.RemoteRefs(cfg.Remote)
	if err != nil {
		return false, err
	}

	// A ref left behind by this tool's own rewrite is separated out below, so
	// that the history the run just replaced is not reported as a branch of its
	// own to go and clean.
	saved := rewrittenHere(repo)

	var lines, stale []string
	for _, ref := range remoteRefs {
		excluded, err := cfg.Exclude.matches(ref)
		if err != nil {
			return false, err
		}
		if excluded {
			continue
		}
		// A ref that cannot be walked is called out rather than skipped, so an
		// unreadable branch does not read as a clean one.
		commits, err := repo.CommitsNotIn(localRefs, []string{ref})
		if err != nil {
			lines = append(lines, fmt.Sprintf("%6s  %s (%v)", "?", ref, err))
			continue
		}
		if len(commits) == 0 {
			continue
		}
		if found := inspect(opts, who, commits); found.flagged > 0 {
			if hash, err := repo.Resolve(ref); err == nil &&
				saved[rewrittenKey(strings.TrimPrefix(ref, "refs/remotes/"+cfg.Remote+"/"), hash)] {
				stale = append(stale, ref)
				continue
			}
			lines = append(lines, fmt.Sprintf("%6d of %d commits  %s",
				found.flagged, len(commits), ref))
		}
	}

	// Both blocks below name work that is still to do on a ref that was not in
	// scope, so neither moves the status. Reporting that they said something is
	// what keeps a sweep from printing "clean" over the only finding a run has.
	if len(stale) > 0 {
		sayf("\nnaming history this repository has already rewritten locally: %s\n",
			strings.Join(stale, ", "))
		sayf("pushing the rewrite settles these; until then the remote still holds what it started from\n")
	}
	if len(lines) == 0 {
		return len(stale) > 0, nil
	}

	sayf("\nnot in scope, and not counted above: remote branches carrying %s\n", subject(opts.Emdashes))
	for _, line := range lines {
		sayf("%s\n", line)
	}
	sayf("check one out to bring it into scope: git switch -c <name> %s/<name>\n", cfg.Remote)
	// The cause is not knowable without the network, and a scan does not use
	// it, so the mechanism is stated rather than one guess at which it is.
	sayf("a remote-tracking ref is only as current as the last fetch; git fetch --prune drops any whose branch is gone\n")
	return true, nil
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
	listing, err := repo.Output("for-each-ref", "--format=%(refname) %(objectname)", backupPrefix)
	if err != nil {
		return saved
	}
	for line := range strings.SplitSeq(strings.TrimSpace(listing), "\n") {
		ref, hash, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if branch := backedUpBranch(ref); branch != "" {
			saved[rewrittenKey(branch, hash)] = true
		}
	}
	return saved
}

// backedUpBranch returns the branch a backup ref was saved for, or "" for a tag,
// which has no remote-tracking counterpart to be compared against. A backup is
// saved as refs/ai-attributions-backup/<stamp>/heads/<branch>.
func backedUpBranch(ref string) string {
	saved, ok := strings.CutPrefix(ref, backupPrefix)
	if !ok {
		return ""
	}
	_, saved, ok = strings.Cut(saved, "/")
	if !ok {
		return ""
	}
	branch, ok := strings.CutPrefix(saved, "heads/")
	if !ok {
		return ""
	}
	return branch
}

func rewrittenKey(branch, hash string) string { return branch + " " + hash }
