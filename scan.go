package main

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/andornaut/ai-attributions/internal/clean"
	"github.com/andornaut/ai-attributions/internal/gitexec"
	"github.com/andornaut/ai-attributions/internal/rewrite"
)

// findings is what a scan turned up over the commits it walked.
type findings struct {
	changes    map[string]rewrite.Change
	removed    map[string]int
	identities map[string]int
	details    []detail
	emdashes   int
	commits    int
	skipped    int
	// flagged counts commits carrying an attribution, which is not the same as
	// len(changes): an agent identity with nowhere to move to is reported and
	// counted, but produces no change.
	flagged int
	// emdashesLeft counts commits whose only finding is an emdash, which is
	// not enough on its own to move a commit.
	emdashesLeft int
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
		changes:    map[string]rewrite.Change{},
		removed:    map[string]int{},
		identities: map[string]int{},
		commits:    len(commits),
	}

	// An emdash is never a reason to rewrite a commit. It is cleaned up only on
	// a commit that an attribution is already moving, so that the number of
	// commits changing hash is decided by attributions alone.
	attribution := clean.Options{Trailers: opts.Trailers}

	for _, commit := range commits {
		var change rewrite.Change
		item := detail{commit: commit}
		flagged := false

		// The rewrite carries messages as JSON, which cannot hold bytes that
		// are not valid UTF-8 without replacing them. Such a message is left
		// exactly as it is, though the identities beside it can still be fixed.
		readable := utf8.ValidString(commit.Message)
		if !readable {
			found.skipped++
		}

		if readable {
			if got := clean.Inspect(attribution, commit.Message); !got.Empty() {
				flagged = true
				item.removedLines = got.RemovedLines
				for _, line := range got.RemovedLines {
					found.removed[strings.TrimSpace(line)]++
				}
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

		// The message is rewritten with the full set of transformations only
		// once something else about the commit has earned the rewrite.
		if readable {
			if flagged {
				if got := clean.Inspect(opts, commit.Message); !got.Empty() {
					change.Message = clean.Message(opts, commit.Message)
					item.changedLines = got.ChangedLines
					found.emdashes += len(got.ChangedLines)
				}
			} else if opts.Emdashes && !clean.Inspect(opts, commit.Message).Empty() {
				found.emdashesLeft++
			}
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
func (f findings) report(verbose bool, refs []string) {
	scope := strings.Join(refs, ", ")
	if f.flagged == 0 {
		fmt.Printf("no AI attributions in %d commits, across %s\n", f.commits, scope)
		f.reportEmdashesLeft()
		f.reportSkipped()
		return
	}

	if verbose {
		for _, item := range f.details {
			fmt.Printf("%s %s\n", item.commit.Short(), item.commit.Subject())
			for _, line := range item.removedLines {
				fmt.Printf("    - %s\n", line)
			}
			for _, change := range item.changedLines {
				fmt.Printf("    - %s\n    + %s\n", change.Old, change.New)
			}
			for _, label := range item.identities {
				fmt.Printf("    ~ %s\n", label)
			}
		}
		fmt.Println()
	}

	fmt.Printf("%d of %d commits carry AI attributions, across %s\n", f.flagged, f.commits, scope)
	f.reportTally("removed lines", f.removed)
	f.reportTally("identities", f.identities)
	if f.emdashes > 0 {
		fmt.Printf("\nemdash rewrites, on commits an attribution is already moving\n%6d  lines\n", f.emdashes)
	}
	f.reportEmdashesLeft()
	f.reportSkipped()
	if !verbose {
		fmt.Println("\npass --verbose to list the commits behind these counts")
	}
}

func (f findings) reportTally(title string, tally map[string]int) {
	if len(tally) == 0 {
		return
	}
	fmt.Printf("\n%s\n", title)
	for _, key := range sortedByCount(tally) {
		fmt.Printf("%6d  %s\n", tally[key], key)
	}
}

// reportEmdashesLeft accounts for the emdashes the run deliberately did not
// touch, so that leaving them reads as a decision rather than an oversight.
func (f findings) reportEmdashesLeft() {
	switch f.emdashesLeft {
	case 0:
		return
	case 1:
		fmt.Print("\n1 commit carries an emdash and no attribution, so it is left alone;\n")
	default:
		fmt.Printf("\n%d commits carry an emdash and no attribution, so they are left alone;\n", f.emdashesLeft)
	}
	fmt.Println("rewriting for a typographic fix would move commits that nothing else moves")
}

func (f findings) reportSkipped() {
	switch {
	case f.skipped == 1:
		fmt.Println("\n1 commit message was skipped because it is not valid UTF-8")
	case f.skipped > 1:
		fmt.Printf("\n%d commit messages were skipped because they are not valid UTF-8\n", f.skipped)
	}
}

// reportRadius names how many commits the rewrite moves. Every descendant of a
// changed commit gets a new hash, which is the number that decides whether a
// rewrite is worth doing.
func (f findings) reportRadius(repo *gitexec.Repo, refs []string) error {
	if len(f.changes) == 0 {
		return nil
	}
	fmt.Println()
	for _, ref := range refs {
		graph, err := repo.Graph(ref)
		if err != nil {
			return err
		}

		dirty := make(map[string]bool, len(graph))
		earliest := ""
		// Reversed, so that a commit is decided after its parents are.
		for i := len(graph) - 1; i >= 0; i-- {
			hash, parents := graph[i][0], graph[i][1:]
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
			if earliest == "" {
				earliest = hash
			}
		}
		if earliest == "" {
			continue
		}
		fmt.Printf("%s: %d of %d commits will change hash, starting at %s %s\n",
			ref, len(dirty), len(graph), shorten(earliest), repo.Describe(earliest))
	}
	return nil
}

// reportRemoteOnly names remote branches that carry attributions and are not
// covered by the refs in scope. They hold work that was never checked out here,
// so the tool reports them rather than rewriting refs it has not been pointed
// at. It reads remote-tracking refs rather than the remote itself, so that a
// scan needs no network.
// Nothing it finds counts toward the run's own findings or its exit code, which
// answer for the refs in scope, the same set apply rewrites.
func reportRemoteOnly(repo *gitexec.Repo, cfg config, opts clean.Options, who identity, localRefs []string) error {
	if !repo.HasRemote(cfg.remote) {
		return nil
	}
	remoteRefs, err := repo.RemoteRefs(cfg.remote)
	if err != nil {
		return err
	}

	var lines []string
	for _, ref := range remoteRefs {
		excluded, err := cfg.exclude.matches(ref)
		if err != nil {
			return err
		}
		if excluded {
			continue
		}
		// A ref that cannot be walked is called out rather than skipped, so
		// that an unreadable branch does not read as a clean one.
		commits, err := repo.CommitsNotIn(localRefs, ref)
		if err != nil {
			lines = append(lines, fmt.Sprintf("%6s  %s (%v)", "?", ref, err))
			continue
		}
		if len(commits) == 0 {
			continue
		}
		if found := inspect(opts, who, commits); found.flagged > 0 {
			lines = append(lines, fmt.Sprintf("%6d of %d commits  %s",
				found.flagged, len(commits), ref))
		}
	}
	if len(lines) == 0 {
		return nil
	}

	fmt.Printf("\nnot in scope, and not counted above: remote branches carrying attributions\n")
	for _, line := range lines {
		fmt.Println(line)
	}
	fmt.Printf("check one out to bring it into scope: git switch -c <name> %s/<name>\n", cfg.remote)
	fmt.Printf("these are remote-tracking refs, which still list a branch deleted upstream; git fetch --prune settles that\n")
	return nil
}
