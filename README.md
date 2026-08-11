# ai-attributions

Strips AI attributions out of a repository's git history: co-author and session trailers, "generated with" footers, the agent identities on the commits themselves, and the emdashes that ride along on those same commits.

```console
$ ai-attributions ~/src/example
2 of 5 commits carry AI attributions, across refs/heads/main

removed lines
     2  Co-Authored-By: Claude <noreply@anthropic.com>
     1  Claude-Session: https://claude.ai/code/session_01abc

identities
     1  author Claude <noreply@anthropic.com> -> andornaut <andornaut@users.noreply.github.com>
     1  committer Claude <noreply@anthropic.com> -> andornaut <andornaut@users.noreply.github.com>

emdash rewrites, on commits an attribution is already moving
     2  lines

1 commit carries an emdash and no attribution, so it is left alone;
rewriting for a typographic fix would move commits that nothing else moves

pass --verbose to list the commits behind these counts

refs/heads/main: 4 of 5 commits will change hash, starting at e241d699c072 2026-08-11 feat(parser): accept empty payloads

not in scope, and not counted above: remote branches carrying attributions
     1 of 1 commits  refs/remotes/origin/agent-work
check one out to bring it into scope: git switch -c <name> origin/<name>
these are remote-tracking refs, which still list a branch deleted upstream; git fetch --prune settles that

nothing was rewritten. Run apply to rewrite the history
```

Scanning is what it does by default. `apply` rewrites the history with [git-filter-repo](https://github.com/newren/git-filter-repo), and `--push` publishes it.

> [!IMPORTANT]
> Rewriting history changes every commit hash from the earliest rewritten commit onward. Anyone else working from those branches has to reset onto the new history. The count is printed before anything is rewritten.

A repository with no attributions is left alone, whatever its punctuation. A tool that rewrote 289 of 482 commits to fix ten emdashes would cost more than it fixed, and on a released project it would orphan every version tag.

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
`google-labs-jules[bot] <jules@users.noreply.github.com>` | Dropped: an ambiguous name next to a product word
`Devin Smith <devin@example.com>` | Kept: an ambiguous name and nothing else
`Ada <ada@openai-research.example.com>` | Kept: a vendor name inside someone else's host
`dependabot[bot] <49699333+dependabot[bot]@…>` | Kept: a bot account that names no agent

A bot account is not evidence on its own. `dependabot`, `renovate`, `github-actions` and `pre-commit-ci` are bots that no agent wrote, so an account has to name an agent as well: `claude[bot]` and `gemini-code-assist[bot]` qualify, and the four above do not.

### Footers

A line is dropped when the line itself is the statement that an agent produced the commit, and when it holds nothing but agent decoration.

```
🤖 Generated with [Claude Code](https://claude.ai/code)
🤖
```

Body prose that mentions an agent in passing is left alone, so `The changelog is generated with Copilot from the commit log.` survives.

### Identities

A trailer is not the only place an agent is named. A commit whose author or committer is an agent is re-attributed to the repository's `user.name` and `user.email`, which `--identity "Name <email>"` overrides and `--identity=none` turns off.

This is the half that GitHub reads. The contributor list on a repository is built from commit authorship, not from trailers, so stripping trailers alone leaves the agent listed as a contributor.

The same test decides an identity as decides a trailer, so a committer named Devin Smith is left alone for the same reason a co-author is, and a Dependabot commit keeps its author.

Re-attribution is the one part that needs an identity to move a commit to. Scanning reports agent identities without one, so a CI job that never configures git still works.

### Emdashes

An emdash is never a reason to rewrite a commit. It is cleaned up only on a commit that an attribution is already moving, so the number of commits changing hash is decided by attributions alone and a typographic fix never widens it. A commit whose only blemish is an emdash keeps it, and the report says how many were left that way.

Where a commit has earned the rewrite, emdashes, endashes, figure dashes and horizontal bars are replaced by what the dash is doing.

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

Every decision is made in Go. The rewrite writes a map of original commit hash to replacement fields, then hands `git-filter-repo` a callback that looks each commit up by `commit.original_id` and assigns them. Nothing is reimplemented in the callback, so what the scan prints is what gets written.

`--partial` scopes the run to the named refs, which is why remotes and remote-tracking refs are left in place.

## Installation

```bash
go install github.com/andornaut/ai-attributions@latest
```

`git-filter-repo` has to be on `PATH` for `apply`. Scanning works without it.

```bash
pip install --user git-filter-repo   # or: apt install git-filter-repo
```

## Usage

```console
$ ai-attributions --help
usage: ai-attributions [command] [flags] [repo-path]

Reports the AI attributions in a repository's history. Nothing is rewritten
unless the apply command asks for it. repo-path defaults to the current
directory.

commands:
  scan                 report what would change (default)
  apply                rewrite the history
  backups              list the pre-rewrite refs saved by earlier runs
  restore <timestamp>  put the refs saved by one run back
  version              print the version

flags:
  --all                every local branch and tag, not just the current branch
  --exclude glob       skip refs matching this glob (repeatable)
  --exit-code          exit 1 when attributions are found, as git diff does (scan only)
  --identity identity  identity to put on agent-authored commits, or none to leave them alone (default: the repository's user.name and user.email)
  --push               force push the rewritten refs (apply only)
  --verbose            report every commit rather than a summary
```

```bash
ai-attributions ~/src/example                     # report only
ai-attributions --verbose ~/src/example           # report, commit by commit
ai-attributions apply ~/src/example               # rewrite the current branch
ai-attributions apply --all --push ~/src/example  # rewrite every branch and tag, then push
```

Every flag takes one dash or two. The documentation uses two.

A rewrite reports where each ref moved and prints the push it did not run:

```console
$ ai-attributions apply ~/src/example
...
saved the pre-rewrite refs under refs/ai-attributions-backup/20260811T052635Z/

refs/heads/main cb44f5294958 -> 4e85ec3c65bc

not pushed. To publish the rewrite:

    git push origin --force-with-lease=refs/heads/main:cb44f5294958d994f1638fe065a9cae4cfcdd5f7 refs/heads/main:refs/heads/main
```

### Excluding refs

`--exclude` takes a glob, is repeatable, and matches the full ref or its short name. For a remote-tracking ref the branch name alone works too, so `--exclude agent-work` covers `refs/remotes/origin/agent-work`. A tag a release workflow owns should not be rewritten by hand:

```bash
ai-attributions apply --all --exclude dev --exclude 'release/*' ~/src/example
```

### Catching them before they land

Rewriting published history is the expensive fix. `--exit-code` reports as usual and exits 1 when anything is found, the way `git diff --exit-code` does, which is what a CI job or a pre-push hook wants:

```bash
ai-attributions --exit-code
```

It needs no git identity configured, and it answers for the refs in scope, the same set `apply` rewrites. A remote branch the report names is not counted, since no `apply` in this repository would fix it.

### Forks

A fork is skipped whole, before anything is scanned. Most of its history belongs to the project it was forked from, and those commits are that project's record: the attributions in them name the people who wrote them, and re-attributing one would claim their work.

```console
$ ai-attributions ~/src/qmk_firmware
skipping /home/andornaut/src/github.com/andornaut/qmk_firmware: a fork, tracking github.com/qmk/qmk_firmware through the upstream remote
history that arrives from another project is not this repository's to rewrite
```

A repository counts as a fork when it has a remote named `upstream` pointing at a different project, which is what `git` and `gh repo fork` set up, or when another remote it has fetched from points at a different project. The project those are measured against is the one the current branch's remote points at, `origin` by default. Remote URLs are compared as `host/owner/repo` with case folded, so the same project over ssh and https is one project and a mirror of your own repository is not a fork. A remote that has never been fetched from, a deploy target for instance, has brought no history here, so it does not count either.

Nothing is reported and the exit status is 0, including under `--exit-code`, so a fork does not fail a CI gate over commits it was never going to fix. `backups` and `restore` still work, since they only put back refs this tool saved.

### Remote branches

`scan` and `apply` cover the same refs. `--all` and `--exclude` move that set, and they move it for both, so what a scan reports is what an apply rewrites.

Remote branches sit outside it. The scan reads `refs/remotes/<remote>/*` and names any branch carrying attributions that the refs in scope do not cover, which is where the branches an agent pushed and you never checked out show up. Those are reported below the findings and counted in none of them, including `--exit-code`: rewriting one means checking it out first, so the tool never force pushes a ref it was not pointed at, and never fails a run over something the run could not have fixed.

It reads remote-tracking refs rather than the remote, so a scan needs no network. The cost is that a branch deleted upstream is still listed until `git fetch --prune` clears it, which the report says.

## Safety

Nothing is rewritten without `apply`, and nothing is rewritten while tracked files have uncommitted changes. Untracked files do not count, since they cannot affect a rewrite of this kind.

The pre-rewrite refs are saved under `refs/ai-attributions-backup/<timestamp>/`.

```console
$ ai-attributions backups
20260811T054927Z  refs/heads/main  812479b

put one run back with: ai-attributions restore <timestamp>

$ ai-attributions restore 20260811T054927Z
refs/heads/main -> 812479bfcbdf

restored. A published rewrite still needs a force push to undo on the remote
```

A branch is pushed with `--force-with-lease` against its remote-tracking ref, which holds what the remote had at the last fetch. A remote that has moved since then rejects the push instead of losing the work, and unpushed local commits do not get in the way. A ref with no remote-tracking counterpart, a tag for instance, has no observed remote value to compare against, so it is forced; those refs are named in the output rather than forced quietly.

A commit whose message is not valid UTF-8 is reported and skipped, because rewriting it would mean carrying the bytes through JSON, which cannot hold them.

## Limitations

- Only commit messages and identities are rewritten. File contents are untouched.
- Annotated tag messages are not scanned, though tags are repointed at the rewritten commits.
- Remote-only branches are reported, never rewritten. Commits reachable only from a stash are not reported either.
- A fork is skipped without inspection, so an attribution on a commit of your own that happens to live in a fork is left alone with the rest.
- GPG signatures are dropped across the rewritten range. `git-filter-repo` works through `git fast-export`, which does not carry the `gpgsig` header. Rewriting a message invalidates that commit's signature anyway, but commits whose messages did not change lose theirs too.

## Developing

- [andornaut@github /til/go](https://github.com/andornaut/til/blob/master/docs/go.md)

```bash
go test ./...
go build .
```

The message and identity tests are pure functions in [`internal/clean`](./internal/clean) and carry the test suite. [`internal/gitexec`](./internal/gitexec) wraps the git commands, [`internal/rewrite`](./internal/rewrite) drives `git-filter-repo`, and [`scan.go`](./scan.go) turns a walk of the history into the report.
