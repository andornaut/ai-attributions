// Package cmd is the command line: what each command takes, and what it hands
// to the run underneath. The work itself is internal/cli's.
package cmd

import (
	"github.com/spf13/cobra"

	"github.com/andornaut/ai-attributions/internal/cli"
)

// cfg is filled in by the flags below and handed to the run. The command is set
// by whichever RunE was reached, so no string is compared to find out which one
// that was.
var cfg cli.Config

// run is what every command that walks a repository does: say which work it
// asks for, take the paths, and turn the status into the error cobra carries
// back to main.
func run(c *cobra.Command, op cli.Op, stamp string, paths []string) error {
	status, err := cli.Execute(op, cfg, stamp, paths)
	if err != nil {
		return err
	}
	// A finding is a status the caller reads, not a failure with a message, so
	// the error carrying it is silenced before it goes back.
	c.SilenceErrors = true
	return cli.Status(status)
}

var scan = &cobra.Command{
	Use:   "scan [repo-path...]",
	Short: "Report what would change, without changing it",
	Long: "Report the AI attributions in a repository's history, and, where the\n" +
		"flags for them ask, its emdashes and endashes and the agent instruction\n" +
		"files its refs carry. Nothing is rewritten: apply is the command that\n" +
		"does that.\n\n" +
		"repo-path defaults to the current directory; more than one path runs\n" +
		"each in turn and summarizes them.",
	RunE: func(c *cobra.Command, args []string) error { return run(c, cli.OpScan, "", args) },
}

var apply = &cobra.Command{
	Use:   "apply [repo-path...]",
	Short: "Rewrite the history, and push it where --push asks",
	Long: "Rewrite what scan reports: the attributions, and the dashes and\n" +
		"instruction files the flags for them put in scope. Every ref it moves is\n" +
		"saved beforehand, and the backups command lists what was saved.",
	RunE: func(c *cobra.Command, args []string) error { return run(c, cli.OpApply, "", args) },
}

var backups = &cobra.Command{
	Use:   "backups [repo-path...]",
	Short: "List the pre-rewrite refs saved by earlier runs",
	RunE:  func(c *cobra.Command, args []string) error { return run(c, cli.OpBackups, "", args) },
}

var clean = &cobra.Command{
	Use:   "clean [timestamp] [repo-path...]",
	Short: "Remove the pre-rewrite refs saved by earlier runs",
	Long: "Remove backups: the refs one run saved where a timestamp names it,\n" +
		"every run but the newest n where --keep-last bounds them, and every\n" +
		"backup the repository holds where neither says so.\n\n" +
		"apply prunes the runs before it to the last 3 as it saves its own, so a\n" +
		"repository holds four at most and this is for bounding the namespace to\n" +
		"another number or emptying it. A backup keeps the pre-rewrite commits\n" +
		"reachable; removing one takes the refs away and not the objects, which\n" +
		"git gc expires on its own schedule.",
	// The timestamp comes first where there is one, as restore's does, and a
	// command line that named both it and --keep-last has asked for two
	// different things.
	Args: func(c *cobra.Command, args []string) error {
		if !c.Flags().Changed("keep-last") {
			return nil
		}
		if cfg.KeepLast < 0 {
			return cli.Usagef("clean --keep-last takes a count, not %d", cfg.KeepLast)
		}
		if len(args) > 0 && cli.ValidStamp(args[0]) {
			return cli.Usagef("clean takes a timestamp or --keep-last, not both")
		}
		return nil
	},
	RunE: func(c *cobra.Command, args []string) error {
		if len(args) > 0 && cli.ValidStamp(args[0]) {
			return run(c, cli.OpClean, args[0], args[1:])
		}
		return run(c, cli.OpClean, "", args)
	},
}

var restore = &cobra.Command{
	Use:   "restore <timestamp> [repo-path...]",
	Short: "Put the refs saved by one run back",
	// The timestamp comes first, so a lone path would otherwise be read as one.
	Args: func(c *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cli.Usagef("restore needs a backup timestamp; ai-attributions backups lists them")
		}
		if !cli.ValidStamp(args[0]) {
			return cli.Usagef("restore expects a timestamp like 20260811T121757Z, got %q; ai-attributions backups lists them", args[0])
		}
		return nil
	},
	RunE: func(c *cobra.Command, args []string) error { return run(c, cli.OpRestore, args[0], args[1:]) },
}

var version = &cobra.Command{
	Use:                   "version",
	Short:                 "Print the version",
	Args:                  cobra.NoArgs,
	DisableFlagsInUseLine: true,
	RunE: func(c *cobra.Command, args []string) error {
		c.Println(cli.Version())
		return nil
	},
}

// Cmd implements the root ai-attributions command.
var Cmd = &cobra.Command{
	Use:   "ai-attributions",
	Short: "AI attributions in commits are ads, remove them!",
	Long: "AI attributions in commits are ads, remove them!\n\n" +
		"Reports the AI attributions in a repository's history, and, where the\n" +
		"flags for them ask, its emdashes and endashes and the agent instruction\n" +
		"files its refs carry. Nothing is rewritten unless the apply command asks\n" +
		"for it. repo-path defaults to the current directory; more than one path\n" +
		"runs each in turn and summarizes them.\n\n" +
		"A command is optional: with none, scan runs, so `ai-attributions .` and\n" +
		"`ai-attributions scan .` are the same run.\n\n" +
		"Exit status:\n" +
		"  0  nothing found\n" +
		"  1  attributions, or the dashes and instruction files the flags for them\n" +
		"     put in scope, found with --exit-code\n" +
		"  2  the run could not complete, or was invoked wrongly\n" +
		"  3  nothing was examined, a fork for instance, with --exit-code",
	// Runs once the arguments have been accepted, so that a failure from here on
	// stops being a wrong invocation worth printing usage for.
	PersistentPreRun: func(c *cobra.Command, args []string) { c.SilenceUsage = true },
	Args:             cobra.NoArgs,
	RunE:             func(c *cobra.Command, args []string) error { return nil },
}

func init() {
	// The flags that describe a walk of the history, on the two commands that
	// walk one. Registering them per command is what makes `backups --base x` a
	// wrong invocation rather than a flag quietly ignored.
	for _, c := range []*cobra.Command{scan, apply} {
		c.Flags().BoolVar(&cfg.AgentsFiles, "agents-files", false, "also report the agent instruction files the refs in scope carry")
		c.Flags().StringVar(&cfg.Base, "base", "", "only the commits the refs in scope add over this `ref`")
		c.Flags().BoolVar(&cfg.CurrentBranch, "current-branch", false, "only the branch that is checked out, not every local branch and tag")
		c.Flags().BoolVar(&cfg.Emdashes, "emdashes", false, "also report emdashes and endashes, and rewrite them, rather than leaving them alone")
		c.Flags().Var(&cfg.Exclude, "exclude", "skip refs matching this `glob` (repeatable)")
		c.Flags().BoolVar(&cfg.ExitCode, "exit-code", false, "exit 1 when anything is found, as git diff does")
		c.Flags().StringVar(&cfg.Identity, "identity", "", "`identity` to put on agent-authored commits, or none to leave them alone (default: the repository's user.name and user.email)")
		c.Flags().BoolVar(&cfg.Quiet, "quiet", false, "print nothing unless a repository found something, for a scheduled run")
		c.Flags().BoolVar(&cfg.Verbose, "verbose", false, "report every commit rather than a summary")
	}
	// --push rewrites the remote, so it belongs to apply alone.
	apply.Flags().BoolVar(&cfg.Push, "push", false, "force push the rewritten refs")
	// Zero by default, which is what the bare command asks for: every backup
	// goes. A rewrite needs no flag at all to bound what it leaves behind.
	clean.Flags().IntVar(&cfg.KeepLast, "keep-last", 0, "keep the newest `n` runs and remove the rest")

	// A flag cobra could not parse is a wrong invocation, and exits 2 like one.
	Cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error { return cli.Usage(err) })
	// cobra prefixes "Error:", and a sweep prefixes each repository's failure
	// itself. One tool reporting failures two ways reads as two tools, so cobra
	// is given the spelling the sweep already uses.
	Cmd.SetErrPrefix("ai-attributions: error:")
	// The generated completion command still works when it is not listed, and
	// this tool has too few commands to spend a line on it.
	Cmd.CompletionOptions.HiddenDefaultCmd = true
	Cmd.AddCommand(scan, apply, backups, clean, restore, version)
	// Registered here rather than left to Execute, which is where cobra adds
	// them: Args reads the command list before Execute runs, and a command
	// missing from it is taken for a repo-path. Both are idempotent, so the
	// call Execute makes later is a no-op. After AddCommand, because the
	// completion command removes itself again when it would be the only one.
	Cmd.InitDefaultHelpCmd()
	Cmd.InitDefaultCompletionCmd()
}

// Args prepares a command line for cobra: it spells single-dash long flags the
// way pflag reads them, and puts "scan" in front where no command was named.
// Both keep a command line that worked before cobra working now.
func Args(args []string) []string { return withDefaultCommand(withDoubleDashes(args)) }

// withDoubleDashes rewrites -flag as --flag. The flag package this replaced
// took either spelling and the documentation says so, where pflag reads a
// single dash as a cluster of shorthands: -emdashes becomes -e -m -d and fails
// on the first. Only -h is a shorthand here, and a single dash followed by one
// character is left alone for it.
//
// Nothing after a bare -- is touched, that being where operands start.
func withDoubleDashes(args []string) []string {
	out := make([]string, 0, len(args))
	for i, arg := range args {
		if arg == "--" {
			return append(out, args[i:]...)
		}
		if len(arg) > 2 && arg[0] == '-' && arg[1] != '-' {
			arg = "-" + arg
		}
		out = append(out, arg)
	}
	return out
}

// withDefaultCommand puts "scan" in front unless the arguments already name a
// command or ask for help, so that `ai-attributions .` and
// `ai-attributions --emdashes .` keep working. cobra has no notion of a default
// subcommand, and the flags belong to the commands that take them rather than
// to the root, so the choice is made here before cobra sees the arguments.
//
// Only the first argument is considered, which is what the parser this replaced
// did: anything that is not a command is a repo-path, and a flag reaches the
// command that declares it.
func withDefaultCommand(args []string) []string {
	if len(args) == 0 {
		return []string{scan.Name()}
	}
	switch args[0] {
	// Flags of the root rather than commands, so they are not in the list below.
	case "-h", "--help":
		return args
	// A shell asking what to complete. cobra registers these inside Execute,
	// which is after this runs, so they are named here rather than found: a
	// scan of the working directory on every press of TAB is what taking them
	// for a path would mean.
	case cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
		return args
	}
	for _, c := range Cmd.Commands() {
		if args[0] == c.Name() {
			return args
		}
	}
	return append([]string{scan.Name()}, args...)
}
