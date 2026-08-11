# ai-attributions

AI attributions in commits are ads, remove them!

Strips AI attributions out of a repository's git history: co-author and session trailers, "generated with" footers, the agent identities on the commits themselves, and the emdashes on those same commits.

```console
$ ai-attributions ~/src/example
2 of 5 commits carry AI attributions, across refs/heads/main

removed lines
     2  Co-Authored-By: Claude <noreply@anthropic.com>
     1  Claude-Session: https://claude.ai/code/session_01abc

identities
     1  author Claude <noreply@anthropic.com> -> andornaut <andornaut@users.noreply.github.com>

emdash rewrites, on commits an attribution is already moving
     2  lines

pass --verbose to list the commits behind these counts

refs/heads/main: 4 of 5 commits will change hash, starting at e241d699c072 2026-08-11 feat(parser): accept empty payloads

nothing was rewritten. Run apply to rewrite the history
```

## Features

- [Scanning](#scanning) is the default - nothing is rewritten unless `apply` asks for it
- [Trailers and footers](#trailers-and-footers) that name an agent are dropped, and human co-authors are kept
- [Identities](#identities) - agent-authored commits are re-attributed, which is what GitHub's contributor list reads
- [Emdashes](#emdashes) are left alone unless `--emdashes` asks for them, and then only on commits an attribution already moves
- [Forks](#forks) are skipped, and [remote branches](#refs-in-scope) are reported but never rewritten
- [`--exit-code`](#catching-them-before-they-land) exits 1 when attributions are found, for CI and pre-push hooks
- [Backups](#backups) - the pre-rewrite refs are saved, and `restore` puts one run back

## Installation

```bash
go install github.com/andornaut/ai-attributions@latest
```

Or unpack a release archive, which needs no Go. `dev` is a rolling tag that CI re-cuts on every push to `main`; a tagged release is the same URL with `dev` replaced by the tag.

```bash
curl -fsSL https://github.com/andornaut/ai-attributions/releases/download/dev/ai-attributions_linux_x86_64.tar.gz \
    | tar -xzf - -C ~/.local/bin ai-attributions
```

`git-filter-repo` has to be on `PATH` for `apply`. Scanning works without it.

```bash
pip install --user git-filter-repo   # or: apt install git-filter-repo
```

## Usage

Run `ai-attributions --help` to view the available commands and flags:

```text
usage: ai-attributions [command] [flags] [repo-path]

AI attributions in commits are ads, remove them!

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
  --emdashes           also rewrite emdashes, on the commits an attribution is already moving
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

### Scanning

`scan` is the default command and writes nothing. It needs neither `git-filter-repo` nor a configured git identity.

`apply` rewrites the refs in scope with [git-filter-repo](https://github.com/newren/git-filter-repo), saving the pre-rewrite refs first, and reports the push it did not run unless `--push` is passed.

> [!IMPORTANT]
> Rewriting history changes every commit hash from the earliest rewritten commit onward. Anyone else working from those branches has to reset onto the new history. The count is printed before anything is rewritten.

### Trailers and footers

These trailer keys are dropped when the value names an agent: `Co-authored-by`, `Coauthored-by`, `Assisted-by`, `AI-assisted-by`, `Generated-by`, `Generated-with`, `Signed-off-by`. A `<agent>-Session` key (`claude`, `codex`, `cursor`, `devin`, `agent`, `ai`) is dropped on the key alone.

A line is also dropped when it opens with generated, created, written, authored or produced followed by with, by or using and names an agent, and when it holds nothing but agent decoration (🤖, 🧠, ✨).

```text
Co-Authored-By: Claude <noreply@anthropic.com>
Signed-off-by: Claude <claude@anthropic.com>
Assisted-by: Copilot
Claude-Session: https://claude.ai/chat/abc123
🤖 Generated with [Claude Code](https://claude.ai/code)
```

Body prose that mentions an agent in passing is left alone, so `The changelog is generated with Copilot from the commit log.` survives. The subject line is never dropped. Blank lines left behind by a removed trailer block are closed up.

`claude`, `anthropic`, `chatgpt`, `openai`, `copilot`, `codeium`, `windsurf`, `aider`, `tabnine` and `codewhisperer` are evidence on their own. `devin`, `jules`, `cursor`, `codex`, `gemini` and `amp` are also human names, so they count only next to a bot account, a vendor domain or a product word. The display name and the address are weighed separately.

Value | Verdict
--- | ---
`Claude <noreply@anthropic.com>` | Dropped: vendor domain
`Cursor Agent <cursoragent@cursor.com>` | Dropped: product word and vendor domain
`google-labs-jules[bot] <jules@users.noreply.github.com>` | Dropped: an ambiguous name next to a product word
`Devin Smith <devin@example.com>` | Kept: an ambiguous name and nothing else
`Ada <ada@openai-research.example.com>` | Kept: a vendor name inside someone else's host
`dependabot[bot] <49699333+dependabot[bot]@…>` | Kept: a bot account that names no agent

A bot account is not evidence on its own, so `dependabot`, `renovate`, `github-actions` and `pre-commit-ci` are kept while `claude[bot]` and `gemini-code-assist[bot]` are dropped.

### Identities

A commit whose author or committer is an agent is re-attributed to the repository's `user.name` and `user.email`. `--identity "Name <email>"` overrides that and `--identity=none` turns it off. The same test decides an identity as decides a trailer.

This is the half that GitHub reads: the contributor list is built from commit authorship, not from trailers.

Scanning reports agent identities without an identity configured; only `apply` requires one.

### Emdashes

Off unless `--emdashes` asks for it, and even then an emdash never moves a commit on its own: it is rewritten only on a commit an attribution is already moving, so attributions alone decide how many commits change hash. The report says how many commits were left with an emdash.

Emdashes, endashes, figure dashes and horizontal bars become a hyphen, a run of them becomes one, and the spacing around them is left as it was: `the parser — which is old — broke` becomes `the parser - which is old - broke`, and `read—write` becomes `read-write`. A dash inside a URL is part of the address, so it is left alone.

### Refs in scope

`scan` and `apply` cover the same refs: the current branch, or every local branch and tag under `--all`, minus anything `--exclude` matches. `--exclude` takes a glob, is repeatable, and matches the full ref or its short name, so `--exclude dev` covers `refs/tags/dev` and `--exclude agent-work` covers `refs/remotes/origin/agent-work`.

```bash
ai-attributions apply --all --exclude dev --exclude 'release/*' ~/src/example
```

A tag naming a commit that changes hash is carried into the rewrite whatever the scope, so no tag is left naming history nothing else references. Its commits are in the rewrite either way, so carrying the tag repoints it without widening what is rewritten. The scan lists the tags this covers, and a tag `--exclude` matched is repointed locally and left out of the push: exclusion decides what is scanned and published, not whether a local ref is left behind.

Remote branches sit outside that set. The scan names any that carry attributions and are not already covered, below the findings and counted in none of them, including `--exit-code`. Rewriting one means checking it out first. It reads remote-tracking refs rather than the remote, so no network is needed, and a branch deleted upstream is listed until `git fetch --prune` clears it.

### Forks

A fork is skipped before anything is scanned, since most of its history belongs to the project it was forked from.

```console
$ ai-attributions ~/src/qmk_firmware
skipping /home/andornaut/src/github.com/andornaut/qmk_firmware: a fork, tracking github.com/qmk/qmk_firmware through the upstream remote
history that arrives from another project is not this repository's to rewrite
```

A repository counts as a fork when it has a remote named `upstream` pointing at a different project, or another remote that points at a different project and has been fetched from. Both are measured against the project the current branch's remote points at, `origin` by default. Remote URLs are compared as `host/owner/repo` with case folded, so the same project over ssh and https is one project.

Nothing is reported and the exit status is 0, including under `--exit-code`. `backups` and `restore` still work.

### Catching them before they land

`--exit-code` reports as usual and exits 1 when anything is found, the way `git diff --exit-code` does. It needs no git identity configured, and it answers for the refs in scope, the same set `apply` rewrites.

```bash
ai-attributions --exit-code
```

### Backups

`apply` refuses to run while tracked files have uncommitted changes, and saves each ref under `refs/ai-attributions-backup/<timestamp>/` before rewriting.

```console
$ ai-attributions backups
20260811T054927Z  refs/heads/main  812479b

put one run back with: ai-attributions restore <timestamp>

$ ai-attributions restore 20260811T054927Z
refs/heads/main -> 812479bfcbdf

restored. A published rewrite still needs a force push to undo on the remote
```

The push covers the refs the rewrite moved, and only those: a ref whose commits carried no change keeps its hash, and forcing it would put a value this run never produced on the remote.

A branch is pushed with `--force-with-lease` against its remote-tracking ref, so a remote that moved since the last fetch rejects the push. A ref with no remote-tracking counterpart, a tag for instance, has nothing to lease against and is forced; those refs are named in the output.

## Limitations

- Only commit messages and identities are rewritten. File contents are untouched.
- Annotated tag messages are not scanned, though the tags themselves are repointed at the rewritten commits.
- A tag is repointed locally. Publishing that move is still a force push, which changes what a release built from the tag is built from.
- Remote-only branches are reported, never rewritten. Commits reachable only from a stash are not reported.
- A fork is skipped without inspection, so an attribution on a commit of your own in a fork is left alone.
- A commit message that is not valid UTF-8 is reported and left as it is, because the rewrite carries messages through JSON. The identities on that commit are still rewritten.
- GPG signatures are dropped across the rewritten range: `git-filter-repo` works through `git fast-export`, which does not carry the `gpgsig` header.

## Developing

- [andornaut@github /til/go](https://github.com/andornaut/til/blob/master/docs/go.md)
- See [go.mod](./go.mod) for dependencies.

```bash
make test           # go test ./...
make coverage       # the suite under -race, then the per-function report
make lint           # the golangci-lint run CI does, and make fmt applies
make build          # a stripped static binary at bin/ai-attributions
```

CI runs the tests, `golangci-lint`, and a cross-compile for each release platform on every push and pull request. A push to `main` re-cuts the `dev` release from those builds; a `v*` tag publishes a release with [GoReleaser](https://goreleaser.com/).

Every decision is made in Go. The rewrite writes a map of original commit hash to replacement fields, then hands `git-filter-repo` a `--commit-callback` that looks each commit up by `commit.original_id`, so what the scan prints is what gets written.

[`internal/clean`](./internal/clean) holds the message and identity tests as pure functions and carries the test suite, [`internal/gitexec`](./internal/gitexec) wraps the git commands, [`internal/rewrite`](./internal/rewrite) drives `git-filter-repo`, and [`scan.go`](./scan.go) turns a walk of the history into the report.
