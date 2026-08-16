// Argument parsing: the usage text, the commands, and the flags.

package cli

import (
	"errors"
	"flag"
	"fmt"
	"path"
	"runtime/debug"
	"strings"
)

const usage = `usage: ai-attributions [command] [flags] [repo-path...]

AI attributions in commits are ads, remove them!

Reports the AI attributions in a repository's history, and, where the flags for
them ask, its emdashes and endashes and the agent instruction files its refs
carry. Nothing is rewritten unless the apply command asks for it. repo-path
defaults to the current directory; more than one path runs each in turn and
summarizes them.

commands:
  scan                 report what would change (default)
  apply                rewrite the history
  backups              list the pre-rewrite refs saved by earlier runs
  restore <timestamp>  put the refs saved by one run back
  version              print the version

exit status:
  0  nothing found
  1  attributions, or the dashes and instruction files the flags for them
     put in scope, found with --exit-code
  2  the run could not complete
  3  nothing was examined, a fork for instance, with --exit-code

flags:
`

var commands = map[string]bool{
	"scan": true, "apply": true,
	"backups": true, "restore": true, "version": true,
}

// refPatterns collects the repeatable --exclude flag.
type refPatterns []string

func (p *refPatterns) String() string { return strings.Join(*p, ",") }

func (p *refPatterns) Set(v string) error { *p = append(*p, v); return nil }

// parseArgs pulls the command off the front, then parses the flags that follow.
// A first argument that is not a command is a repo-path, so that the common
// case stays "ai-attributions <path>".
func parseArgs(argv []string) (config, []string, error) {
	cfg := config{command: "scan"}
	if len(argv) > 0 && commands[argv[0]] {
		cfg.command, argv = argv[0], argv[1:]
	}

	flags := flag.NewFlagSet("ai-attributions", flag.ExitOnError)
	flags.BoolVar(&cfg.agentsFiles, "agents-files", false, "also report the agent instruction files the refs in scope carry")
	flags.StringVar(&cfg.base, "base", "", "only the commits the refs in scope add over this `ref`")
	flags.BoolVar(&cfg.currentBranch, "current-branch", false, "only the branch that is checked out, not every local branch and tag")
	flags.BoolVar(&cfg.emdashes, "emdashes", false, "also report emdashes and endashes, and rewrite them, rather than leaving them alone")
	flags.Var(&cfg.exclude, "exclude", "skip refs matching this `glob` (repeatable)")
	flags.BoolVar(&cfg.exitCode, "exit-code", false, "exit 1 when anything is found, as git diff does")
	flags.StringVar(&cfg.identity, "identity", "", "`identity` to put on agent-authored commits, or none to leave them alone (default: the repository's user.name and user.email)")
	flags.BoolVar(&cfg.push, "push", false, "force push the rewritten refs (apply only)")
	flags.BoolVar(&cfg.quiet, "quiet", false, "print nothing unless a repository found something, for a scheduled run")
	flags.BoolVar(&cfg.verbose, "verbose", false, "report every commit rather than a summary")
	flags.Usage = func() {
		_, _ = fmt.Fprint(flags.Output(), usage)
		flags.PrintDefaults()
	}
	if err := flags.Parse(argv); err != nil {
		return cfg, nil, err
	}

	switch {
	case cfg.push && !cfg.applying():
		return cfg, nil, errors.New("--push belongs to apply; there is nothing to push until the history is rewritten")
	case cfg.base != "" && !cfg.scanning():
		return cfg, nil, errors.New("--base belongs to scan and apply, which are the commands that walk the history")
	case cfg.exitCode && !cfg.scanning():
		return cfg, nil, errors.New("--exit-code belongs to scan and apply, which are the commands that look for attributions")
	case cfg.quiet && !cfg.scanning():
		return cfg, nil, errors.New("--quiet belongs to scan and apply; what backups and restore print is the whole point of running them")
	}
	return cfg, flags.Args(), nil
}

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "devel"
}

// matches tests each pattern against the full ref and its short forms, so
// --exclude dev covers refs/tags/dev and --exclude agent-work covers
// refs/remotes/origin/agent-work.
func (p *refPatterns) matches(ref string) (bool, error) {
	candidates := []string{ref}
	for _, prefix := range []string{"refs/heads/", "refs/tags/", "refs/remotes/"} {
		if short, ok := strings.CutPrefix(ref, prefix); ok {
			candidates = append(candidates, short)
			// A remote-tracking ref is still qualified by its remote.
			if prefix == "refs/remotes/" {
				if _, branch, found := strings.Cut(short, "/"); found {
					candidates = append(candidates, branch)
				}
			}
		}
	}

	for _, pattern := range *p {
		for _, candidate := range candidates {
			matched, err := path.Match(pattern, candidate)
			if err != nil {
				return false, fmt.Errorf("bad --exclude pattern %q: %w", pattern, err)
			}
			if matched {
				return true, nil
			}
		}
	}
	return false, nil
}
