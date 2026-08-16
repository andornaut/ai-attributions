# ai-attributions

[![CI](https://github.com/andornaut/ai-attributions/actions/workflows/release.yml/badge.svg)](https://github.com/andornaut/ai-attributions/actions/workflows/release.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

AI attributions in commits are ads, remove them!

Strips AI attributions out of a repository's git history: co-author and session trailers, "generated with" footers, and the agent identities on the commits themselves. `--emdashes` adds the emdashes and endashes in the messages in scope, wherever they appear, and `--agents-files` adds the instruction files a ref ships.

```console
$ ai-attributions --emdashes ~/src/example
2 of 5 commits carry AI attributions or dashes, across refs/heads/main

removed lines
     2  Co-Authored-By: Claude <noreply@anthropic.com>
     1  Claude-Session: https://claude.ai/code/session_01abc

identities
     1  author Claude <noreply@anthropic.com> -> andornaut <andornaut@users.noreply.github.com>

dash rewrites
     2  lines

pass --verbose to list the commits behind these counts

refs/heads/main: 4 of 5 commits will change hash, starting at e241d699c072 2026-08-11 feat(parser): accept empty payloads

nothing was rewritten. Run apply to rewrite the history
```

## Features

- [Scanning](#scanning) is the default - nothing is rewritten unless `apply` asks for it
- [Trailers and footers](#trailers-and-footers) that name an agent are dropped, and human co-authors are kept
- [Identities](#identities) - agent-authored commits are re-attributed, which is what GitHub's contributor list reads
- [Emdashes and endashes](#emdashes-and-endashes) are left alone unless `--emdashes` asks for them, and then they are a finding of their own
- [Agent instruction files](#agent-instruction-files) a ref ships are reported where `--agents-files` asks for them
- [Forks](#forks) are skipped, and [remote branches](#refs-in-scope) are reported but never rewritten
- [Several repositories](#sweeping-several-repositories) in one run, summarized a line each, and [`--quiet`](#quiet-runs) so a scheduled sweep speaks only when it finds something
- [`--exit-code`](#catching-them-before-they-land) exits 1 when anything is found, for CI and pre-push hooks, and a [GitHub Action](#github-action) that fails a branch carrying them
- [Backups](#backups) - the pre-rewrite refs are saved, and `restore` puts one run back

## Installation

```bash
go install github.com/andornaut/ai-attributions@latest
```

Or unpack a release archive, which needs no Go. `latest` is the newest version; a release tag goes in the same position, and `dev` is the rolling one, re-cut on every push to `main`.

```bash
curl -fsSL https://github.com/andornaut/ai-attributions/releases/latest/download/ai-attributions_linux_x86_64.tar.gz \
    | tar -xzf - -C ~/.local/bin ai-attributions
```

`git-filter-repo` has to be on `PATH` for `apply`. Scanning works without it.

```bash
pip install --user git-filter-repo   # or: apt install git-filter-repo
```

## Usage

Run `ai-attributions --help` to view the commands, and
`ai-attributions <command> --help` for the flags a command takes:

```text
AI attributions in commits are ads, remove them!

Reports the AI attributions in a repository's history, and, where the
flags for them ask, its emdashes and endashes and the agent instruction
files its refs carry. Nothing is rewritten unless the apply command asks
for it. repo-path defaults to the current directory; more than one path
runs each in turn and summarizes them.

A command is optional: with none, scan runs, so `ai-attributions .` and
`ai-attributions scan .` are the same run.

Exit status:
  0  nothing found
  1  attributions, or the dashes and instruction files the flags for them
     put in scope, found with --exit-code
  2  the run could not complete, or was invoked wrongly
  3  nothing was examined, a fork for instance, with --exit-code

Usage:
  ai-attributions [flags]
  ai-attributions [command]

Available Commands:
  apply       Rewrite the history, and push it where --push asks
  backups     List the pre-rewrite refs saved by earlier runs
  help        Help about any command
  restore     Put the refs saved by one run back
  scan        Report what would change, without changing it
  version     Print the version

Flags:
  -h, --help   help for ai-attributions

Use "ai-attributions [command] --help" for more information about a command.
```

Every flag belongs to the commands that use it, so `scan --help` and
`apply --help` list them and `backups` takes none:

```text
Report the AI attributions in a repository's history, and, where the
flags for them ask, its emdashes and endashes and the agent instruction
files its refs carry. Nothing is rewritten: apply is the command that
does that.

repo-path defaults to the current directory; more than one path runs
each in turn and summarizes them.

Usage:
  ai-attributions scan [repo-path...] [flags]

Flags:
      --agents-files        also report the agent instruction files the refs in scope carry
      --base ref            only the commits the refs in scope add over this ref
      --current-branch      only the branch that is checked out, not every local branch and tag
      --emdashes            also report emdashes and endashes, and rewrite them, rather than leaving them alone
      --exclude glob        skip refs matching this glob (repeatable)
      --exit-code           exit 1 when anything is found, as git diff does
  -h, --help                help for scan
      --identity identity   identity to put on agent-authored commits, or none to leave them alone (default: the repository's user.name and user.email)
      --quiet               print nothing unless a repository found something, for a scheduled run
      --verbose             report every commit rather than a summary
```

```bash
ai-attributions ~/src/example                            # report only
ai-attributions --verbose ~/src/example                  # report, commit by commit
ai-attributions ~/src/*                                  # sweep a directory of repositories
ai-attributions apply --current-branch ~/src/example     # rewrite the branch that is checked out
ai-attributions apply --push ~/src/example               # rewrite every branch and tag, then push
```

Every flag takes one dash or two. The documentation uses two.

### Scanning

`scan` is the default command and writes nothing. It needs neither `git-filter-repo` nor a configured git identity.

`apply` rewrites the refs in scope with [git-filter-repo](https://github.com/newren/git-filter-repo), saving the pre-rewrite refs first, and reports the push it did not run unless `--push` is passed.

> [!IMPORTANT]
> Rewriting history changes every commit hash from the earliest rewritten commit onward. Anyone else working from those branches has to reset onto the new history. The count is printed before anything is rewritten.

### Reading the report

`apply` closes with a `done:` line naming what it did, so a report that ends without one is a run that stopped part way. A failure goes to stderr as `ai-attributions: error: <what went wrong>`, separated from the report above it, and exits 2.

```console
$ ai-attributions apply --push ~/src/example
...
done: the history is rewritten and pushed to origin

$ ai-attributions apply --push ~/src/example
...
ai-attributions: error: the working tree has uncommitted changes; commit or stash them first
```

Those markers are colored when the stream is a terminal: green for a run that finished, yellow for a repository carrying attributions, blue for one that was not examined, red for a failure. A report that is piped or redirected stays plain text, a CI log included, as does a run with `NO_COLOR` set.

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

### Emdashes and endashes

Off unless `--emdashes` asks for it: a typographic mark is a house style rather than an ad, so attributions alone decide what a run finds by default. Asking makes an emdash or an endash a finding in its own right, counted toward `--exit-code` and rewritten wherever it appears, so a build the flag fails is a build the `apply` it names fixes. Without the flag, neither is looked at.

An emdash or an endash becomes a hyphen, a run of them becomes one, and the spacing around them is left as it was: `the parser — which is old — broke` becomes `the parser - which is old - broke`, and `read—write` becomes `read-write`. The figure dash and the horizontal bar are not touched, being typography for a numeric span and for quoted speech rather than punctuation a hyphen stands in for. A dash inside a URL is part of the address, so it is left alone.

### Agent instruction files

Off unless `--agents-files` asks for it. The tip of every ref in scope is then checked for the files an agent reads its instructions from. These configure the tools a contributor happens to run rather than describe the project, so a repository that ships one hands its prompts to everyone who clones it, and a repository that would rather not says so with the flag.

Where the flag asks, an instruction file counts toward `--exit-code` on its own. It is not cosmetic, and no rewrite this tool makes takes it back out, so a run that stayed quiet about one would leave the one thing it found to be noticed by hand.

`--base` does not narrow this the way it narrows the commit walk. A base bounds the history a branch answers for; the tip of that branch ships what it ships, whichever commit put it there.

```
agent instruction files, counted by the refs in scope that carry them
     3  .cursor/rules
     3  AGENTS.md
     2  CLAUDE.md
```

The paths checked are `.aider.conf.yml`, `.clinerules`, `.cursor/rules`, `.cursorrules`, `.github/copilot-instructions.md`, `.junie/guidelines.md`, `.roorules`, `.windsurfrules`, `AGENT.md`, `AGENTS.md`, `CLAUDE.md`, `GEMINI.md` and `QWEN.md`. A directory is reported as the directory, not as each file under it.

Each is looked up at a ref rather than searched for, so the check costs a descent to a known path per ref rather than a walk of every tree in scope: a repository with a thousand tags does not pay for its whole history to answer a question about its tips.

Nothing here is rewritten. `git rm -r --cached <path>` takes one out of the branch that carries it, and the report prints that command for each path it found. Every other ref keeps its copy, and so does the history.

### Refs in scope

`scan` and `apply` cover the same refs: every local branch and tag, or the branch that is checked out under `--current-branch`, minus anything `--exclude` matches. Covering the whole repository is the default because "is this repository clean" is the question the tool answers; `--current-branch` is for a caller asking about one branch, such as a CI job answering for what a push added. `--exclude` takes a glob, is repeatable, and matches the full ref or its short name, so `--exclude dev` covers `refs/tags/dev` and `--exclude agent-work` covers `refs/remotes/origin/agent-work`.

```bash
ai-attributions apply --exclude dev --exclude 'release/*' ~/src/example
```

`--base ref` narrows those refs to the commits they add over `ref`, which is what a branch answers for. The history it was cut from is left out of the walk, the counts and `--exit-code`, and the report names the base beside the refs.

```bash
ai-attributions --base origin/main ~/src/example        # the commits this branch adds
ai-attributions apply --base origin/main ~/src/example  # rewrite those and nothing earlier
```

A commit the base already carries keeps its message, its identity and its hash, so rewriting a branch does not move the history it shares with anyone else.

A tag naming a commit that changes hash is carried into the rewrite whatever the scope, so no tag is left naming history nothing else references. Its commits are in the rewrite either way, so carrying the tag repoints it without widening what is rewritten. The scan lists the tags this covers, and a tag `--exclude` matched is repointed locally and left out of the push: exclusion decides what is scanned and published, not whether a local ref is left behind.

Remote branches sit outside that set. The scan names any that carry attributions and are not already covered, below the findings and counted in none of them, including `--exit-code`. Rewriting one means checking it out first. It reads remote-tracking refs rather than the remote, so no network is needed and a ref is only as current as the last fetch; `git fetch --prune` drops any whose branch is gone upstream. A ref still naming history an `apply` has already rewritten here is named separately, as a push that has not happened rather than a branch to go and clean, which the backup the rewrite saved is what makes knowable. A run given `--base` leaves that report out, since a remote branch sits outside the range rather than beside it.

### Forks

A fork is skipped before anything is scanned, since most of its history belongs to the project it was forked from.

```console
$ ai-attributions ~/src/qmk_firmware
skipping /home/andornaut/src/github.com/andornaut/qmk_firmware: a fork, tracking github.com/qmk/qmk_firmware through the upstream remote
history that arrives from another project is not this repository's to rewrite
```

A repository counts as a fork when it has a remote named `upstream` pointing at a different project, or another remote that points at a different project and has been fetched from. Both are measured against the project the current branch's remote points at, `origin` by default. Remote URLs are compared as `host/owner/repo` with case folded, so the same project over ssh and https is one project.

Nothing is reported, and under `--exit-code` the status is 3: nothing was found because nothing was looked at, which is not the same answer as a clean repository and is not reported as one. Without `--exit-code` the status is 0. `backups` and `restore` still work.

### Sweeping several repositories

More than one `repo-path` runs each in turn and prints one line per repository as it finishes, followed by the full report for each one that found something. A repository that fails does not end the sweep, and keeps whatever it had already reported: an `apply` that failed at the push has still named the backup it saved. The failure itself goes to stderr, where a single run puts it, so a caller can redirect the summary and still see what went wrong.

```console
$ ai-attributions ~/src/github.com/andornaut/*
clean    /home/andornaut/src/github.com/andornaut/gog
found    /home/andornaut/src/github.com/andornaut/cloudflare-starter
skipped  /home/andornaut/src/github.com/andornaut/qmk_firmware

=== /home/andornaut/src/github.com/andornaut/cloudflare-starter
2 of 5 commits carry AI attributions, across refs/heads/main
...
```

The status is the worst of what the sweep saw: 2 if any repository failed, then 1 if any found something, then 3 if any was skipped. It does not depend on the order the paths were given in.

`backups` and `restore` report rather than scan, so they have no finding to summarize and print under a `=== <path>` heading instead.

### Quiet runs

`--quiet` holds the report back and prints it only for a repository that found something. A sweep of thirty clean checkouts writes nothing at all and exits 0, which is what a cron job wants: mail arrives on the days that need an answer, and no others.

```bash
0 4 * * * ai-attributions scan --quiet ~/src/github.com/andornaut/*
```

A fork counts as nothing to answer for, since it is the same fork every day. A failure always prints, both the summary line and whatever the run got as far as, because a run that could not look is not a run that found nothing. So does a remote branch carrying attributions, which is counted in no status and would otherwise be the one finding `--quiet` dropped, the refs in scope being what the status answers for.

A non-zero status always comes with the report that explains it, which is why the line above leaves `--exit-code` off. Add it for a caller that reads the status rather than the mail, and a fork then prints the notice behind its 3 rather than exiting silently. The status itself is unaffected either way: `--quiet` decides what is written, not what is reported.

It belongs to `scan` and `apply`. What `backups` and `restore` print is the whole point of running them, so they reject it.

### Catching them before they land

`--exit-code` reports as usual and exits 1 when anything is found, the way `git diff --exit-code` does. It needs no git identity configured, and it answers for the refs in scope. On `apply` it answers the same question, so a job can tell a run that had to rewrite something from one that had nothing to do. Any other failure exits 2, so a caller can tell a run that found something from one that could not look.

```bash
ai-attributions --exit-code                                     # every branch and tag
ai-attributions --exit-code --current-branch                    # the whole history of this branch
ai-attributions --exit-code --current-branch --base origin/main # only the commits this branch adds
```

### GitHub Action

[`action.yml`](./action.yml) is a composite action that runs the scan over the commits a branch adds and fails the job when it finds any. It is meant for branches an agent writes: the failure names each commit and the commands that take the attributions back off, in the job log and on the run summary.

It scans with `--current-branch`, so it answers for the branch being built rather than for every ref the checkout happens to carry. A job fails on what the push added, not on a tag or an old branch that came along with it.

```yaml
name: AI attributions

on:
  pull_request:
  push:
    branches: [main]

permissions:
  contents: read

jobs:
  ai-attributions:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7.0.1
        with:
          fetch-depth: 0
      - uses: andornaut/ai-attributions@v1
```

Input | Default | What it is
--- | --- | ---
`base` | the pull request's base branch, the commit a push started from, or the default branch | the ref the branch is measured against
`version` | the release the action was cut with | the release to install
`path` | `.` | the repository to scan, relative to the workspace
`identity` | whoever pushed, at their GitHub address | the identity the report names for an agent-authored commit
`emdashes` | `false` | fail the run on the emdashes and endashes in the commit messages it reads
`agents-files` | `false` | fail the run on the agent instruction files the branch carries

`emdashes` and `agents-files` are off for the same reason the flags behind them are: a house style and a contributor's tooling are a repository's own call, and a scan that failed a build over either unasked would be answering a question nobody put to it. Turn one on and the failure names it like any other finding, and the `apply` the job offers is given the same flags, so the command it prints answers for what failed the build.

Each needs a `version` carrying the check behind it, and the two fail differently on one that does not. `agents-files` ends the step, the binary having no such flag to parse. `emdashes` is quieter: every release has had that flag, so an older one accepts it and applies what it meant at the time, which was a tidy-up riding along on commits an attribution already moved. A dash-only commit then passes green under an input that says it fails. A release carrying `--agents-files` is one where `--emdashes` means what this document says it means; pin or move `version` with these inputs.

```yaml
      - uses: andornaut/ai-attributions@v1
        with:
          emdashes: true
          agents-files: true
```

A base the action guessed and cannot resolve widens to the whole history with a warning; one passed as `base` fails the step, since widening would scan history that predates whatever it was pointing at. A push to the default branch has no base to measure against, its own previous tip not being in the checkout, so it reads the whole history too.

A runner has no git identity, and a scan that cannot name one says so on every run. Nothing is rewritten in CI, so `identity` only decides what the report says. `identity: none` is not the way to quiet it: that turns identity reporting off, and an agent-authored commit with no trailer on it then passes.

`v1` follows the newest `v1.x.y`, re-pointed by the job that publishes one. Pinning it rather than `main` decides when a change in what counts as an attribution reaches a workflow, which is the change that turns a passing repository into a failing one without a flag moving. `@v1.0.0` pins one release, and a commit sha pins the action's code exactly. `version` names the binary, and defaults to the release its `action.yml` was cut with, so `@v1` moves both halves together; `version: dev` installs the build re-cut on every push to `main`.

The action downloads the linux archive, checks it against `checksums.txt` and puts it on `PATH`, so the runner needs no Go and no `git-filter-repo`. Other platforms have no archive and fail saying so.

`fetch-depth: 0` is worth setting but not required: a shallow walk would report fewer commits than the branch adds rather than fail, so the action deepens a shallow checkout itself, at the cost of a second fetch.

A pull request is checked out at a merge commit with `HEAD` detached, which is neither a branch to scan nor the history under review. The action scans the branch the pull request asks to merge, at `pull_request.head.sha`, and names it in the failure.

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

Every ref is pushed with `--force-with-lease`, so a remote holding anything other than what the rewrite started from rejects the push. A branch leases against its remote-tracking ref. A tag has none, so `git ls-remote` reads the remote once and a tag it carries is leased against the pre-rewrite commit, `--force-with-lease=<ref>:<value>` taking that value explicitly. A tag the remote does not carry is created by the push, and is named in the output.

The push is `--atomic`, so a ref whose lease fails takes the rest down with it: half a published rewrite is a branch on new history beside a tag still naming the old.

A carried tag is published with the rewrite rather than held back, since a tag left on the remote naming a commit no branch there reaches is the state the local repoint exists to avoid, and leaving the remote in it would need a second command.

## Limitations

- Only commit messages and identities are rewritten. File contents are untouched, so an agent instruction file is reported and never removed.
- The instruction files are a fixed list of paths, checked where they are conventionally kept. One nested somewhere else, a monorepo's per-package `AGENTS.md` for instance, is not found even with `--agents-files`.
- Annotated tag messages are not scanned, though the tags themselves are repointed at the rewritten commits.
- A tag is repointed locally. Publishing that move is still a force push, which changes what a release built from the tag is built from.
- Remote-only branches are reported, never rewritten. Commits reachable only from a stash are not reported.
- A fork is skipped without inspection, so an attribution on a commit of your own in a fork is left alone.
- A commit message that is not valid UTF-8 is reported and left as it is, because the rewrite carries messages through JSON. The identities on that commit are still rewritten.
- GPG signatures are dropped from every commit the rewrite re-exports, `git-filter-repo` working through `git fast-export`, which does not carry the `gpgsig` header. `--base` bounds that to the range.

## Developing

- [andornaut@github /til/go](https://github.com/andornaut/til/blob/main/docs/go.md)
- See [go.mod](./go.mod) for dependencies.

```bash
make test           # go test ./...
make coverage       # the suite under -race, then the per-function report
make lint           # the golangci-lint run CI does, and make fmt applies
make build          # a stripped static binary at bin/ai-attributions
```

CI runs the tests, `golangci-lint`, and a cross-compile for each release platform on every push and pull request. A push to `main` re-cuts the `dev` release from those builds; a `vX.Y.Z` tag publishes a release with [GoReleaser](https://goreleaser.com/) and re-points the major tag at it, so `v1` is a pointer CI moves rather than a tag to publish by hand.

Every decision is made in Go. The rewrite writes a map of original commit hash to replacement fields, then hands `git-filter-repo` a `--commit-callback` that looks each commit up by `commit.original_id`, so what the scan prints is what gets written.

[`cmd`](./cmd) is the command line, built with [cobra](https://github.com/spf13/cobra): each command declares the flags it takes, so a flag that means nothing to a command is refused there rather than accepted and ignored. [`internal/cli`](./internal/cli) does the work a command asks for and turns a walk of the history into the report, driving [`internal/clean`](./internal/clean), which holds the message and identity tests as pure functions and carries the test suite, [`internal/gitexec`](./internal/gitexec), which wraps the git commands, and [`internal/rewrite`](./internal/rewrite), which drives `git-filter-repo`. `main.go` hands cobra the arguments and exits on what comes back, and holds nothing else.

A command is optional, which cobra has no notion of, so the arguments are prepared before it sees them: a first argument naming no command gets `scan` put in front, and a single-dash long flag is widened to two, the flag package this replaced having taken either spelling.
