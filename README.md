# ai-attributions

[![Release](https://github.com/andornaut/ai-attributions/actions/workflows/release.yml/badge.svg)](https://github.com/andornaut/ai-attributions/actions/workflows/release.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/license/MIT)

AI attributions in commits are ads, remove them!

Strips AI attributions out of a repository's git history: co-author and session trailers, "generated with" footers, and the agent identities on the commits themselves.

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

- [Scanning](#scanning-and-applying) is the default: nothing is rewritten unless `apply` asks
- [Trailers and footers](#trailers-and-footers) naming an agent are dropped, and human co-authors kept
- [Identities](#identities) on agent-authored commits are re-attributed, which is what GitHub's contributor list reads
- [Emdashes](#emdashes-and-endashes) and [agent instruction files](#agent-instruction-files) are reported where their flags ask
- [Forks](#forks) are skipped, and [remote branches](#refs-in-scope) reported but never rewritten
- [Several repositories](#sweeping-several-repositories) in one run, with [`--quiet`](#quiet-runs) for a scheduled sweep
- [`--exit-code`](#exit-status) for CI and pre-push hooks, and a [GitHub Action](#github-action)
- [Backups](#backups) of the pre-rewrite refs, `restore` to put one run back, and `clean` to take them away

## Installation

```bash
go install github.com/andornaut/ai-attributions@latest
```

Or unpack a release archive, which needs no Go. A release tag goes where `latest` is, and `dev` is the rolling one, re-cut on every push to `main`.

```bash
curl -fsSL https://github.com/andornaut/ai-attributions/releases/latest/download/ai-attributions_linux_x86_64.tar.gz \
    | tar -xzf - -C ~/.local/bin ai-attributions
```

`git-filter-repo` has to be on `PATH` for `apply`. Scanning works without it.

```bash
pip install --user git-filter-repo   # or: apt install git-filter-repo
```

## Usage

`repo-path` defaults to the current directory; more than one path runs each in turn. A command is optional: with none, `scan` runs. Every flag takes one dash or two.

Command | What it does
--- | ---
`scan` | Report what would change, without changing it (the default)
`apply` | Rewrite the history, and push it where `--push` asks
`backups` | List the pre-rewrite refs saved by earlier runs
`clean` | Remove the pre-rewrite refs saved by earlier runs
`restore` | Put the refs saved by one run back
`version` | Print the version

`scan` and `apply` take the flags below, and `apply` adds `--push`. `backups` and `restore` take none of them and `clean` takes `--keep-last` alone, so a flag that means nothing to a command is refused there rather than ignored.

Flag | What it does
--- | ---
`--agents-files` | also report the agent instruction files the refs in scope carry
`--base ref` | only the commits the refs in scope add over this ref
`--current-branch` | only the branch that is checked out
`--emdashes` | also report emdashes and endashes, and rewrite them
`--exclude glob` | skip refs matching this glob (repeatable)
`--exit-code` | exit 1 when anything is found, as `git diff` does
`--identity identity` | identity for agent-authored commits, or `none` to leave them alone (default: the repository's `user.name` and `user.email`)
`--quiet` | print nothing unless a repository found something
`--verbose` | report every commit rather than a summary

```bash
ai-attributions ~/src/example                            # report only
ai-attributions ~/src/*                                  # sweep a directory of repositories
ai-attributions apply --current-branch ~/src/example     # rewrite the branch that is checked out
ai-attributions apply --push ~/src/example               # rewrite every branch and tag, then push
```

### Exit status

Status | Meaning
--- | ---
0 | nothing found
1 | something found, with `--exit-code`
2 | the run could not complete, or was invoked wrongly
3 | nothing was examined, a fork for instance, with `--exit-code`

`--exit-code` needs no git identity configured and answers for the refs in scope. On `apply` it tells a run that had to rewrite something from one that had nothing to do.

```bash
ai-attributions --exit-code --current-branch --base origin/main # only the commits this branch adds
```

## Scanning and applying

`scan` writes nothing and needs neither `git-filter-repo` nor a configured git identity. `apply` rewrites the refs in scope with [git-filter-repo](https://github.com/newren/git-filter-repo), saving the pre-rewrite refs first, and reports the push it did not run unless `--push` is passed.

> [!IMPORTANT]
> Rewriting history changes every commit hash from the earliest rewritten commit onward. Anyone else working from those branches has to reset onto the new history. The count is printed before anything is rewritten.

`apply` closes with a `done:` line, so a report ending without one stopped part way. A failure goes to stderr as `ai-attributions: error: <what went wrong>` and exits 2. Both markers are colored on a terminal (green finished, yellow found, blue not examined, red failed); a piped report, or one with `NO_COLOR` set, stays plain.

## Trailers and footers

These trailer keys are dropped when the value names an agent: `Co-authored-by`, `Coauthored-by`, `Assisted-by`, `AI-assisted-by`, `Generated-by`, `Generated-with`, `Signed-off-by`. A `<agent>-Session` key (`claude`, `codex`, `cursor`, `devin`, `agent`, `ai`) is dropped on the key alone. A line is also dropped when it opens with generated, created, written, authored or produced followed by with, by or using and names an agent, and when it holds nothing but agent decoration (🤖, 🧠, ✨).

```text
Co-Authored-By: Claude <noreply@anthropic.com>
Assisted-by: Copilot
Claude-Session: https://claude.ai/chat/abc123
🤖 Generated with [Claude Code](https://claude.ai/code)
```

The subject line is never dropped, blank lines left by a removed trailer block are closed up, and prose mentioning an agent in passing is left alone, so `The changelog is generated with Copilot from the commit log.` survives.

`claude`, `anthropic`, `chatgpt`, `openai`, `copilot`, `codeium`, `windsurf`, `aider`, `tabnine` and `codewhisperer` are evidence on their own. `devin`, `jules`, `cursor`, `codex`, `gemini` and `amp` are also human names, so they count only next to a bot account, a vendor domain or a product word.

Value | Verdict
--- | ---
`Claude <noreply@anthropic.com>` | Dropped: vendor domain
`google-labs-jules[bot] <jules@users.noreply.github.com>` | Dropped: an ambiguous name next to a product word
`Devin Smith <devin@example.com>` | Kept: an ambiguous name and nothing else
`Ada <ada@openai-research.example.com>` | Kept: a vendor name inside someone else's host
`dependabot[bot] <49699333+dependabot[bot]@…>` | Kept: a bot account that names no agent

## Identities

A commit whose author or committer is an agent is re-attributed to the repository's `user.name` and `user.email`. `--identity "Name <email>"` overrides that and `--identity=none` turns it off. The same test decides an identity as decides a trailer.

This is the half GitHub reads: the contributor list is built from commit authorship, not from trailers. Only `apply` requires an identity to be configured.

## Emdashes and endashes

Off unless `--emdashes` asks for it: a typographic mark is a house style rather than an ad. With the flag, one is a finding of its own, counted toward `--exit-code` and rewritten wherever it appears.

One becomes a hyphen, a run of them becomes one, and the spacing is left as it was: `the parser — which is old — broke` becomes `the parser - which is old - broke`, and `read—write` becomes `read-write`. A dash inside a URL is left alone, as are the figure dash and the horizontal bar.

## Agent instruction files

Off unless `--agents-files` asks for it. The tip of every ref in scope is then checked for the files an agent reads its instructions from, and each counts toward `--exit-code` on its own. `--base` does not narrow this: the tip of a branch ships what it ships.

```text
agent instruction files, counted by the refs in scope that carry them
     3  .cursor/rules
     3  AGENTS.md
     2  CLAUDE.md
```

The paths checked are `.aider.conf.yml`, `.clinerules`, `.cursor/rules`, `.cursorrules`, `.github/copilot-instructions.md`, `.junie/guidelines.md`, `.roorules`, `.windsurfrules`, `AGENT.md`, `AGENTS.md`, `CLAUDE.md`, `GEMINI.md` and `QWEN.md`. A directory is reported as the directory, and each path is looked up at a ref rather than searched for.

Nothing here is rewritten. The report prints `git rm -r --cached <path>` for each path found, which takes one out of the branch that carries it; every other ref keeps its copy, and so does the history.

## Refs in scope

`scan` and `apply` cover the same refs: every local branch and tag, or the branch that is checked out under `--current-branch`, minus anything `--exclude` matches. `--exclude` takes a repeatable glob and matches the full ref or its short name, so `--exclude dev` covers `refs/tags/dev`.

```bash
ai-attributions apply --exclude dev --exclude 'release/*' ~/src/example
ai-attributions apply --base origin/main ~/src/example  # rewrite the commits this branch adds
```

- `--base ref` narrows those refs to the commits they add over `ref`, leaving the rest out of the walk, the counts and `--exit-code`. A commit the base already carries keeps its message, identity and hash.
- A tag naming a commit that changes hash is carried into the rewrite whatever the scope, so no tag is left naming history nothing else references. A tag `--exclude` matched is repointed locally and left out of the push.
- Remote branches sit outside the set: any carrying attributions are named below the findings, counted in no status, and rewriting one means checking it out first. A sweep ends such a repository on `out of scope` rather than `clean`. Remote-tracking refs are read rather than the remote, so a ref is only as current as the last fetch, and `git fetch --prune` drops any whose branch is gone. One naming history an `apply` already rewrote is named separately, as a push that has not happened. `--base` leaves that report out.

## Forks

A fork is skipped before anything is scanned, since most of its history belongs to the project it was forked from.

```console
$ ai-attributions ~/src/qmk_firmware
skipping /home/andornaut/src/github.com/andornaut/qmk_firmware: a fork, tracking github.com/qmk/qmk_firmware through the upstream remote
history that arrives from another project is not this repository's to rewrite
```

A repository counts as a fork when it has a remote named `upstream` pointing at a different project, or another remote pointing at a different project that has been fetched from, measured against what the current branch's remote points at. URLs are compared as `host/owner/repo` with case folded.

Nothing is reported, and under `--exit-code` the status is 3 rather than 0. `backups`, `clean` and `restore` still work.

## Sweeping several repositories

More than one `repo-path` prints a line per repository as it finishes, then the full report for each one with something to say. A repository that fails does not end the sweep, and its failure goes to stderr.

```console
$ ai-attributions ~/src/github.com/andornaut/*
clean        /home/andornaut/src/github.com/andornaut/gog
found        /home/andornaut/src/github.com/andornaut/cloudflare-starter
out of scope /home/andornaut/src/github.com/andornaut/mrs
skipped      /home/andornaut/src/github.com/andornaut/qmk_firmware

=== /home/andornaut/src/github.com/andornaut/cloudflare-starter
2 of 5 commits carry AI attributions, across refs/heads/main

=== /home/andornaut/src/github.com/andornaut/mrs
no AI attributions in 160 commits, across refs/heads/main

not in scope, and not counted above: remote branches carrying AI attributions
     3 of 3 commits  refs/remotes/origin/agent-work
```

Each word names a different thing to go and do.

Word | Meaning | Status
--- | --- | ---
`clean` | nothing to do | 0
`found` | attributions on the refs in scope, or an agent instruction file | 1
`rewrote` | an `apply` gave a ref a new hash, so this repository's history has changed | 1
`out of scope` | a finding on a ref the run did not answer for, a remote branch for instance | 0
`skipped` | a fork, whose history is not this repository's to rewrite | 3
`failed` | the run could not finish, and the error is on stderr | 2

Every status but 2 needs `--exit-code`, and the sweep exits on the worst it saw, whatever order the paths came in. `backups`, `clean` and `restore` have no finding to summarize, so they print under the `=== <path>` heading only.

## Quiet runs

`--quiet` prints the line and the report only for a repository with something to answer for, so a sweep of thirty clean checkouts writes nothing and exits 0.

```bash
0 4 * * * ai-attributions scan --quiet ~/src/github.com/andornaut/*
```

A fork counts as nothing to answer for. A failure always prints, as does a rewrite, and so does `out of scope`: its finding moves no status, so weighing the report by one would drop it. `--quiet` decides what is written, not what is reported, and `backups`, `clean` and `restore` reject it.

## GitHub Action

[`action.yml`](./action.yml) is a composite action that scans the commits a branch adds and fails the job when it finds any, naming each commit and the commands that take the attributions back off. It scans with `--current-branch`, so a job fails on what the push added, not on a tag that came with it.

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

- `emdashes` and `agents-files` both landed in v1.3.0, and a `version` naming a release older than that ends the step rather than scanning with it. The two are refused for different reasons: an older binary exits 2 on `--agents-files`, which never existed, and accepts `--emdashes` with the meaning it had then, a tidy-up riding along, which would pass a dash-only commit green under an input whose description says it fails. A `version` that is not a release, `dev` for instance, is taken at its word.
- `v1` follows the newest `v1.x.y`, `@v1.0.0` pins one release, and a sha pins the action's code. `version` defaults to the release its `action.yml` was cut with, so `@v1` moves both halves together; `version: dev` installs the rolling build.
- A base the action guessed and cannot resolve widens to the whole history with a warning; one passed as `base` fails the step. A push to the default branch has no base, so it reads the whole history.
- Nothing is rewritten in CI, so `identity` only decides what the report says. `identity: none` turns identity reporting off, and an agent-authored commit with no trailer then passes.
- The linux archive is checked against `checksums.txt`, so the runner needs no Go and no `git-filter-repo`; other platforms fail saying so. `fetch-depth: 0` is optional, the action deepening a shallow checkout itself, and a pull request is scanned at `pull_request.head.sha` rather than at the detached merge commit.

## Backups

`apply` refuses to run while tracked files have uncommitted changes, and saves each ref under `refs/ai-attributions-backup/<timestamp>/` before rewriting.

```console
$ ai-attributions backups
20260811T054927Z  refs/heads/main  812479bfcbdf
20260811T061340Z  refs/heads/main  4f0ac91d2b6e

put one run back with: ai-attributions restore <timestamp>
take them away with: ai-attributions clean, or clean --keep-last <n>

$ ai-attributions restore 20260811T054927Z
refs/heads/main -> 812479bfcbdf

restored. A published rewrite still needs a force push to undo on the remote
```

A rewrite prunes the runs before it to the last 3 and saves its own above them, so the namespace is bounded with no flag to remember and holds four saved runs at most. It prunes at the start of a run rather than at the end of one: a backup is what `restore` puts back after a rewrite that turned out to be wrong, and publishing that rewrite is what opens the window in which someone reaches for it, so the next run is what takes it away.

A run whose refs stand where the newest backup saved them reuses that one instead of writing a second copy, and a rewrite that moves nothing takes its own snapshot away again. The bound counts earlier runs alone for that reason: a snapshot the run itself may take back would otherwise have cost an earlier one to save.

`clean` removes backups: the refs one run saved where a timestamp names it, every run but the newest with `--keep-last`, and every backup the repository holds where neither says so.

```console
$ ai-attributions clean --keep-last 1
removed 20260811T054927Z (1 ref)

removed 1 of 2 saved runs
```

The backups are also what a scan reads to tell a rewrite that has not been pushed from a remote branch carrying attributions of its own, so it answers for as far back as they reach: once a run is pruned or cleaned away, its unpushed rewrite is reported as a remote branch instead.

Removing a backup takes the refs away and not the objects. A branch's pre-rewrite tip is still in its reflog, which `git gc` expires on its own schedule; `git reflog expire --expire=now --all && git gc --prune=now` takes both. A tag has no reflog, so its backup is the only record of where it stood.

The push covers the refs the rewrite moved and only those, so a ref whose commits carried no change keeps its hash. Each is pushed with `--force-with-lease`: a branch leases against its remote-tracking ref, and a tag, having none, against its pre-rewrite commit read from `git ls-remote`. The push is `--atomic`, since half a published rewrite is a branch on new history beside a tag still naming the old.

## Limitations

- Only commit messages and identities are rewritten. File contents are untouched, so an agent instruction file is reported and never removed. The paths checked are a fixed list, so one nested elsewhere is not found.
- Annotated tag messages are not scanned, though the tags are repointed, locally: publishing that move is still a force push.
- Commits reachable only from a stash are not reported, and an attribution on a commit of your own in a fork is left alone with the rest of it.
- A commit message that is not valid UTF-8 is reported and left as it is, the rewrite carrying messages through JSON. The identities on that commit are still rewritten.
- GPG signatures are dropped from every commit the rewrite re-exports: `git-filter-repo` works through `git fast-export`, which does not carry the `gpgsig` header.

## Developing

- [andornaut@github /til/go](https://github.com/andornaut/til/blob/main/docs/go.md)
- See [go.mod](./go.mod) for dependencies.

```bash
make test           # go test ./...
make coverage       # the suite under -race, then the per-function report
make lint           # the golangci-lint run CI does, and make fmt applies
make build          # a stripped static binary at bin/ai-attributions
make install        # build, then copy it to /usr/local/bin with sudo
make uninstall      # remove what install copied
make publish VERSION=x.y.z  # bump action.yml's version default, tag, push both
```

CI runs the tests, `golangci-lint`, and a cross-compile for each release platform. A push to `main` re-cuts the `dev` release; a `vX.Y.Z` tag publishes a release with [GoReleaser](https://goreleaser.com/) and re-points the major tag at it. The release refuses a tag whose `action.yml` names a different version, which is what `make publish` keeps together.

Package | Role
--- | ---
[`cmd`](./cmd) | the command line, built with [cobra](https://github.com/spf13/cobra): each command declares the flags it takes
[`internal/cli`](./internal/cli) | does the work a command asks for, and turns a walk of the history into the report
[`internal/clean`](./internal/clean) | the message and identity tests, as pure functions
[`internal/gitexec`](./internal/gitexec) | wraps the git commands
[`internal/rewrite`](./internal/rewrite) | drives `git-filter-repo`
[`internal/version`](./internal/version) | the one version string: the linker stamps a release build, `go install` recovers it from the module version, and a build from a working tree stays `dev`
`main.go` | hands cobra the arguments and exits on what comes back

Every decision is made in Go: the rewrite writes a map of original commit hash to replacement fields, then hands `git-filter-repo` a `--commit-callback` that looks each commit up by `commit.original_id`.
