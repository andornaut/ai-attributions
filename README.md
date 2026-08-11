# ai-attributions

Strips AI attributions out of a repository's git history: co-author and session trailers, "generated with" footers, and the emdashes that AI-written commit messages leave behind.

```console
$ ai-attributions -dry-run ~/src/example
090a3502fff0 refactor the read—write split
    - 🤖 Generated with [Claude Code](https://claude.ai/code)
    - Co-Authored-By: Claude <noreply@anthropic.com>
    - Claude-Session: https://claude.ai/chat/abc123
    - refactor the read—write split
    + refactor the read-write split
33c0e3e3e6a7 feat(parser): accept empty payloads
    - Co-Authored-By: Claude <noreply@anthropic.com>
    - The handler assumed a body — it did not always get one.
    + The handler assumed a body: it did not always get one.

2 of 4 commits need rewriting, across refs/heads/main
dry run: nothing was rewritten
```

It scans the commit messages in scope, prints what it will change, rewrites the history with [git-filter-repo](https://github.com/newren/git-filter-repo), and force pushes only when asked.

> [!IMPORTANT]
> Rewriting history changes every commit hash from the earliest rewritten commit onward. Anyone else working from those branches has to reset onto the new history.

## What it removes

### Attribution trailers

A trailer is dropped when its value names an AI agent, so a human co-author survives.

```
Co-Authored-By: Claude <noreply@anthropic.com>
Signed-off-by: Claude <claude@anthropic.com>
Assisted-by: Copilot
Co-authored-by: google-labs-jules[bot] <jules@users.noreply.github.com>
Claude-Session: https://claude.ai/chat/abc123
```

Several agents are named after people, so `devin`, `jules`, `cursor`, `codex`, `gemini` and `amp` are not evidence on their own. They count only next to a bot account, a vendor domain or a product word, which is what keeps a co-author named Devin Smith or Jules Verne out of the rewrite. The display name and the address are weighed separately, so a vendor name sitting inside an unrelated host does not qualify either.

Value | Verdict
--- | ---
`Claude <noreply@anthropic.com>` | Dropped: vendor domain
`Cursor Agent <cursoragent@cursor.com>` | Dropped: product word and vendor domain
`google-labs-jules[bot] <jules@users.noreply.github.com>` | Dropped: bot account
`Devin Smith <devin@example.com>` | Kept: an ambiguous name and nothing else
`Ada <ada@openai-research.example.com>` | Kept: a vendor name inside someone else's host

### Footers

A line is dropped when the line itself is the statement that an agent produced the commit, and when it holds nothing but agent decoration.

```
🤖 Generated with [Claude Code](https://claude.ai/code)
🤖
```

Body prose that mentions an agent in passing is left alone, so `The changelog is generated with Copilot from the commit log.` survives.

### Emdashes

Emdashes, endashes, figure dashes and horizontal bars are replaced by what the dash is doing.

Before | After | Rule
--- | --- | ---
`refactor the parser — it was unreadable` | `refactor the parser: it was unreadable` | A lone spaced dash introduces a clause
`fix(api): reject empties — it assumed a body` | `fix(api): reject empties, it assumed a body` | A colon is already in use, so a comma reads better
`the parser — which is old — broke` | `the parser, which is old, broke` | Paired dashes are parenthetical
`the read—write split over 3–5 nodes` | `the read-write split over 3-5 nodes` | An unspaced dash joins two words or numbers
`— drop the cache` | `- drop the cache` | A leading dash is a list bullet
`see https://example.com/a—b` | unchanged | A dash inside a URL is part of the address

The subject line is never dropped, only rewritten, so no commit is left without one. Blank lines left behind by a removed trailer block are closed up.

## How it works

Every decision about a message is made in Go. The rewrite writes a map of original commit hash to new message, then hands `git-filter-repo` a callback that looks each commit up by `commit.original_id` and assigns the replacement. Nothing is reimplemented in the callback, so what the scan prints is what gets written.

`--partial` scopes the run to the named refs, which is why remotes and remote-tracking refs are left in place.

## Installation

```bash
go install github.com/andornaut/ai-attributions@latest
```

`git-filter-repo` has to be on `PATH` for the rewrite. The scan and `-dry-run` work without it.

```bash
pip install --user git-filter-repo   # or: apt install git-filter-repo
```

## Usage

```console
$ ai-attributions --help
usage: ai-attributions [flags] [repo-path]

Rewrites the commit messages of the current branch, dropping AI attribution
trailers and normalizing emdashes. repo-path defaults to the current directory.

flags:
  -all
    	rewrite every local branch and tag, not just the current branch
  -dry-run
    	report what would change without rewriting anything
  -no-backup
    	skip saving the pre-rewrite refs under refs/ai-attributions-backup/
  -no-emdashes
    	leave emdashes alone
  -no-trailers
    	leave attribution trailers and footers alone
  -push
    	force push the rewritten refs after a successful rewrite
  -remote string
    	remote to push to (default "origin")
```

Without flags, the current branch is scanned and rewritten and nothing is pushed.

```bash
ai-attributions -dry-run ~/src/example    # report only
ai-attributions ~/src/example             # rewrite the current branch
ai-attributions -all -push ~/src/example  # rewrite every branch and tag, then push
```

A rewrite reports where each ref moved, and prints the push it did not run:

```console
$ ai-attributions ~/src/example
87edff378fc6 feat(parser): accept empty payloads
    - Co-Authored-By: Claude <noreply@anthropic.com>

1 of 3 commits need rewriting, across refs/heads/main
saved the pre-rewrite refs under refs/ai-attributions-backup/20260811T052635Z/
[git-filter-repo progress]

refs/heads/main cb44f5294958 -> 4e85ec3c65bc

not pushed. To publish the rewrite:

    git push origin --force-with-lease=refs/heads/main:cb44f5294958d994f1638fe065a9cae4cfcdd5f7 refs/heads/main:refs/heads/main
```

## Safety

Nothing is rewritten while tracked files have uncommitted changes. Untracked files do not count, since they cannot affect a message-only rewrite.

The pre-rewrite refs are saved under `refs/ai-attributions-backup/<timestamp>/`, which `-no-backup` turns off.

```bash
# Put a branch back
git update-ref refs/heads/main refs/ai-attributions-backup/<timestamp>/heads/main

# Throw the backups away
git for-each-ref --format='%(refname)' refs/ai-attributions-backup | xargs -n1 git update-ref -d
```

A branch is pushed with `--force-with-lease` against its remote-tracking ref, which holds what the remote had at the last fetch. A remote that has moved since then rejects the push instead of losing the work, and unpushed local commits do not get in the way. A ref with no remote-tracking counterpart, a tag for instance, has no observed remote value to compare against, so it is forced; those refs are named in the output rather than forced quietly.

A commit whose message is not valid UTF-8 is reported and skipped, because rewriting it would mean carrying the bytes through JSON, which cannot hold them.

## Limitations

- Only commit messages are rewritten. File contents are untouched.
- Annotated tag messages are not scanned, though tags are repointed at the rewritten commits.
- Commits reachable only from a remote-tracking ref or a stash are out of scope.
- GPG signatures are dropped across the rewritten range. `git-filter-repo` works through `git fast-export`, which does not carry the `gpgsig` header. Rewriting a message invalidates that commit's signature anyway, but commits whose messages did not change lose theirs too.

## Developing

- [andornaut@github /til/go](https://github.com/andornaut/til/blob/master/docs/go.md)

```bash
go test ./...
go build .
```

The message transforms are pure functions in [`internal/clean`](./internal/clean) and carry the test suite. [`internal/gitexec`](./internal/gitexec) wraps the git commands, and [`internal/rewrite`](./internal/rewrite) drives `git-filter-repo`.
