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

	for _, commit := range commits {
		var change rewrite.Change
		item := detail{commit: commit}

		// The rewrite carries messages as JSON, which cannot hold bytes that
		// are not valid UTF-8 without replacing them. Such a message is left
		// exactly as it is, though the identities beside it can still be fixed.
		if utf8.ValidString(commit.Message) {
			if got := clean.Inspect(opts, commit.Message); !got.Empty() {
				change.Message = clean.Message(opts, commit.Message)
				item.removedLines = got.RemovedLines
				item.changedLines = got.ChangedLines
				found.emdashes += len(got.ChangedLines)
				for _, line := range got.RemovedLines {
					found.removed[strings.TrimSpace(line)]++
				}
			}
		} else {
			found.skipped++
		}

		if who.name != "" {
			if clean.Identity(commit.AuthorName, commit.AuthorEmail) {
				change.AuthorName, change.AuthorEmail = who.name, who.address
				item.identities = append(item.identities,
					mapping("author", commit.AuthorName, commit.AuthorEmail, who))
			}
			if clean.Identity(commit.CommitterName, commit.CommitterEmail) {
				change.CommitterName, change.CommitterEmail = who.name, who.address
				item.identities = append(item.identities,
					mapping("committer", commit.CommitterName, commit.CommitterEmail, who))
			}
			for _, label := range item.identities {
				found.identities[label]++
			}
		}

		if change != (rewrite.Change{}) {
			found.changes[commit.Hash] = change
			found.details = append(found.details, item)
		}
	}
	return found
}

func mapping(field, name, address string, who identity) string {
	return fmt.Sprintf("%s %s <%s> -> %s", field, name, address, who)
}

// report prints the tallies, and every commit behind them under -verbose.
func (f findings) report(verbose bool, refs []string) {
	scope := strings.Join(refs, ", ")
	if len(f.changes) == 0 {
		fmt.Printf("no AI attributions in %d commits, across %s\n", f.commits, scope)
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

	fmt.Printf("%d of %d commits carry AI attributions, across %s\n", len(f.changes), f.commits, scope)
	f.reportTally("removed lines", f.removed)
	f.reportTally("identities", f.identities)
	if f.emdashes > 0 {
		fmt.Printf("\nemdash rewrites\n%6d  lines\n", f.emdashes)
	}
	f.reportSkipped()
	if !verbose {
		fmt.Println("\npass -verbose to list the commits behind these counts")
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
		if excluded, err := cfg.exclude.matches(ref); err != nil || excluded {
			continue
		}
		commits, err := repo.CommitsNotIn(localRefs, ref)
		if err != nil || len(commits) == 0 {
			continue
		}
		if found := inspect(opts, who, commits); len(found.changes) > 0 {
			lines = append(lines, fmt.Sprintf("%6d of %d commits  %s",
				len(found.changes), len(commits), ref))
		}
	}
	if len(lines) == 0 {
		return nil
	}

	fmt.Printf("\nremote branches carrying attributions that are not in scope\n")
	for _, line := range lines {
		fmt.Println(line)
	}
	fmt.Printf("check one out to rewrite it: git switch -c <name> %s/<name>\n", cfg.remote)
	fmt.Printf("these are remote-tracking refs, which still list a branch deleted upstream; git fetch --prune settles that\n")
	return nil
}
