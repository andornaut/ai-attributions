// Package cli is the command: argument parsing, the report it prints, and the
// scan and rewrite it drives. Everything but the entry point lives here, so
// what main does is call Main and exit on what it returns.
package cli

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/andornaut/ai-attributions/internal/clean"
	"github.com/andornaut/ai-attributions/internal/gitexec"
	"github.com/andornaut/ai-attributions/internal/rewrite"
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

const (
	backupPrefix = "refs/ai-attributions-backup/"
	identityNone = "none"
)

// out is where the reports are written. A sweep swaps in a buffer for each
// repository, so that a clean one can be summarized in a line rather than
// printed in full.
var out io.Writer = os.Stdout

// said records whether any report has been written, so that a failure with
// nothing above it does not open the output with a blank line.
var said bool

// noteworthy records whether the report holds something a scheduled run has to
// see, which the outcome cannot answer on its own: a remote branch carrying
// attributions is worth naming and moves no status, the refs in scope being
// what the status answers for. Without this, --quiet weighs a report by its
// outcome and drops the one finding that has none.
var noteworthy bool

// say writes one piece of the report. The write error is dropped: a report that
// cannot reach a closed stdout is not a reason to fail a rewrite that worked,
// and every caller would otherwise carry a check it has no answer for.
func sayf(format string, args ...any) {
	said = true
	_, _ = fmt.Fprintf(out, format, args...)
}

// reportDone closes an apply that completed, so that a report ending without
// this line is a run that stopped part way rather than one that finished with
// little to say.
func reportDonef(format string, args ...any) {
	sayf("\n%s %s\n", paint(colorOut, green, "done:"), fmt.Sprintf(format, args...))
}

// reportNothingToRewrite closes an apply that had nothing to do, so that every
// apply that ran to the end says so, whether or not it changed anything.
func reportNothingToRewrite(cfg config) {
	if cfg.applying() {
		reportDonef("nothing to rewrite")
	}
}

// reportFailure writes the line that says a run could not complete. It goes to
// stderr, so a caller can redirect the report and still see what went wrong.
func reportFailuref(format string, args ...any) {
	// The blank line separates the failure from the report above it, which it
	// can only do when both land in the same place. A report redirected
	// elsewhere leaves the failure alone on the stream, with nothing above it
	// to be separated from.
	if said && interleaved {
		fmt.Fprintln(os.Stderr)
	}
	fmt.Fprintf(os.Stderr, "%s %s\n",
		paint(colorErr, red, "ai-attributions: error:"), fmt.Sprintf(format, args...))
}

// outcome is how one repository's run ended. It is what a sweep lists per
// repository, and what the exit status is reduced from. A repository the run
// declined to examine is deliberately not clean: reporting "I did not look" as
// "nothing to find" is the one answer a sweep must not give.
type outcome int

const (
	outcomeClean outcome = iota
	outcomeFound
	outcomeSkipped
)

func (o outcome) String() string {
	switch o {
	case outcomeFound:
		return "found"
	case outcomeSkipped:
		return "skipped"
	default:
		return "clean"
	}
}

// color marks what a sweep's line means without reading it: nothing to do,
// something found, or a repository that was not examined.
func (o outcome) color() string {
	switch o {
	case outcomeFound:
		return yellow
	case outcomeSkipped:
		return blue
	default:
		return green
	}
}

// status is the exit status an outcome reduces to. Like git diff --exit-code, a
// finding is reported by the status only when it was asked for; a failure is
// not routed through here, and always exits 2.
func (o outcome) status(cfg config) int {
	if !cfg.exitCode {
		return 0
	}
	switch o {
	case outcomeFound:
		return 1
	case outcomeSkipped:
		return 3
	default:
		return 0
	}
}

// stampRe matches the timestamp a backup is saved under.
var stampRe = regexp.MustCompile(`^\d{8}T\d{6}Z$`)

var commands = map[string]bool{
	"scan": true, "apply": true,
	"backups": true, "restore": true, "version": true,
}

type config struct {
	command string
	// agentsFiles asks for the instruction files the refs in scope carry. Off
	// by default, as --emdashes is: an agent instruction file is a contributor's
	// business until a repository says otherwise, and a scan that reported one
	// unasked would fail a build over a file no rewrite here takes back out.
	agentsFiles bool
	base        string
	// currentBranch narrows the run to the branch that is checked out. The
	// default is every local branch and tag, "is this repository clean" being
	// the question the tool answers.
	currentBranch bool
	emdashes      bool
	exclude       refPatterns
	exitCode      bool
	identity      string
	push          bool
	// quiet holds the report back unless the run has something to answer for,
	// which is what lets a scheduled run mail only the days that need one.
	quiet   bool
	verbose bool

	// remote is resolved from the branch's upstream, rather than assumed to be
	// origin.
	remote string
}

func (c config) applying() bool { return c.command == "apply" }

// scanning reports whether the command walks the history looking for
// attributions, which is what has a finding to report and a scope to report it
// for. backups and restore only move refs this tool saved.
func (c config) scanning() bool { return c.command == "scan" || c.applying() }

// target is a ref to rewrite, the commit it pointed at beforehand, where it
// ended up, and the value to expect on the remote when pushing. The lease is
// empty for a ref with no remote-tracking counterpart, such as a tag or a
// branch never pushed.
type target struct {
	ref   string
	hash  string
	after string
	lease string

	// publish is false for a ref the rewrite has to repoint but the run does
	// not own: a tag --exclude left out of scope still has to come off the
	// commits it names, but publishing it is not this run's call.
	publish bool
}

// moved reports whether the rewrite changed where a ref points. A ref whose
// commits carried no change keeps its hash, and pushing it would force a value
// this run did not produce over whatever the remote holds.
func (t target) moved() bool { return t.after != "" && t.after != t.hash }

// unleased reports whether there is no value to hold the remote to.
func (t target) unleased() bool { return t.lease == "" }

// identity is the name and address to put on a commit an agent authored. Both
// parts are set together or not at all, so a rewrite cannot assign half of one.
type identity struct {
	name    string
	address string
	enabled bool
}

func (i identity) String() string { return i.name + " <" + i.address + ">" }

// resolved reports whether there is an identity to re-attribute to. Scanning
// works without one.
func (i identity) resolved() bool { return i.name != "" && i.address != "" }

// refPatterns collects the repeatable --exclude flag.
type refPatterns []string

func (p *refPatterns) String() string     { return strings.Join(*p, ",") }
func (p *refPatterns) Set(v string) error { *p = append(*p, v); return nil }

// Main runs the command and returns the status to exit with. It returns rather
// than exiting so that the status is a value a test can assert on, which is
// what the suite beside this file does.
func Main(argv []string) int {
	status, err := run(argv)
	if err != nil {
		// Every failure exits 2, the status the flag package already uses, so a
		// caller can tell a finding from a run that could not look.
		reportFailuref("%v", err)
		return 2
	}
	return status
}

func run(argv []string) (int, error) {
	cfg, args, err := parseArgs(argv)
	if err != nil {
		return 0, err
	}
	if cfg.command == "version" {
		sayf("%s\n", version())
		return 0, nil
	}

	stamp := ""
	if cfg.command == "restore" {
		if len(args) == 0 {
			return 0, errors.New("restore needs a backup timestamp; ai-attributions backups lists them")
		}
		stamp, args = args[0], args[1:]
		// The timestamp comes first, so a lone path would otherwise be read as
		// one.
		if !stampRe.MatchString(strings.Trim(stamp, "/")) {
			return 0, fmt.Errorf("restore expects a timestamp like 20260811T121757Z, got %q; ai-attributions backups lists them", stamp)
		}
	}
	if len(args) == 0 {
		args = []string{"."}
	}
	if len(args) > 1 {
		return sweep(cfg, stamp, args), nil
	}

	if cfg.quiet {
		return quietRepo(cfg, stamp, args[0])
	}
	found, err := runRepo(cfg, stamp, args[0])
	if err != nil {
		return 0, err
	}
	return found.status(cfg), nil
}

// worthSaying reports whether a --quiet run has to print what it buffered. A
// finding and a failure are the obvious two. The third is a status the caller
// will read: a non-zero exit with nothing above it names no repository and no
// reason, which is the one answer a scheduled run cannot act on.
func worthSaying(cfg config, ended outcome, err error) bool {
	return err != nil || ended == outcomeFound || noteworthy || ended.status(cfg) != 0
}

// quietRepo runs one repository with the report held in a buffer, and writes it
// out only when there is something to answer for. A failure prints what the run
// got as far as: a run that could not look is not a run that found nothing.
func quietRepo(cfg config, stamp, repoPath string) (int, error) {
	previous, saidBefore := out, said
	buf := &bytes.Buffer{}
	out, noteworthy = buf, false
	found, err := runRepo(cfg, stamp, repoPath)
	out = previous

	if worthSaying(cfg, found, err) {
		_, _ = io.Copy(out, buf)
	} else {
		// Nothing reached the stream after all, so a failure printed later must
		// not be spaced away from a report nobody saw.
		said = saidBefore
	}
	if err != nil {
		return 0, err
	}
	return found.status(cfg), nil
}

// runRepo does one repository's work and reports how it ended, leaving the exit
// status to the caller, which is the only part that differs between a single
// run and a sweep.
func runRepo(cfg config, stamp, repoPath string) (outcome, error) {
	repo, err := gitexec.Open(repoPath)
	if err != nil {
		return outcomeClean, err
	}
	cfg.remote = targetRemote(repo)

	// backups and restore only put back refs this tool saved, so they stay
	// available whatever the repository is.
	switch cfg.command {
	case "backups":
		return outcomeClean, listBackups(repo)
	case "restore":
		return outcomeClean, restoreBackup(repo, stamp)
	}

	upstream, isFork, err := forkUpstream(repo, cfg.remote)
	if err != nil {
		return outcomeClean, err
	}
	if isFork {
		reportFork(repo, upstream)
		if cfg.applying() {
			reportDonef("nothing examined, a fork is not this repository's to rewrite")
		}
		return outcomeSkipped, nil
	}
	return scan(repo, cfg)
}

// sweep runs every path given and prints one line per repository, followed by
// the full report for each one that found something. A repository that fails
// does not end the sweep: the rest are still worth looking at, and a failure
// nobody sees is the reason to keep going rather than stop.
func sweep(cfg config, stamp string, paths []string) int {
	type result struct {
		path   string
		report string
	}

	previous := out
	defer func() { out = previous }()

	var reports []result
	var failed, found, skipped bool
	for _, repoPath := range paths {
		// backups and restore report rather than scan. There is no finding to
		// summarize, and what they print is the whole point of running them, so
		// it goes straight out under a heading instead of being weighed against
		// an outcome none of them produce.
		if !cfg.scanning() {
			sayf("=== %s\n", repoPath)
			if _, err := runRepo(cfg, stamp, repoPath); err != nil {
				failed = true
				reportFailuref("%s: %v", repoPath, err)
			}
			continue
		}

		// Buffered so that a clean repository costs one line rather than a
		// screen, which is what makes a sweep of many readable at all.
		buf := &bytes.Buffer{}
		out, noteworthy = buf, false
		ended, err := runRepo(cfg, stamp, repoPath)
		out = previous

		// The line goes out as each repository finishes, rather than at the
		// end, so that a long sweep says where it has got to. The failure
		// itself goes to stderr, where a single run puts it.
		if err != nil {
			failed = true
			sayf("%s %s\n", column(colorOut, red, "failed"), repoPath)
			reportFailuref("%s: %v", repoPath, err)
			// Whatever the run reported before it failed is still worth having:
			// an apply that failed at the push has already named the backup it
			// saved and the refs it moved.
			reports = append(reports, result{path: repoPath, report: buf.String()})
			continue
		}
		// --quiet keeps the line and the report for a repository worth
		// answering for, and drops both for one that is not, so a sweep of
		// thirty clean checkouts says nothing at all. A fork is nothing to
		// answer for, being the same fork every day, until --exit-code makes it
		// a status the caller reads.
		speak := !cfg.quiet || worthSaying(cfg, ended, nil)
		if speak {
			sayf("%s %s\n", column(colorOut, ended.color(), ended.String()), repoPath)
		}
		switch ended {
		case outcomeFound:
			found = true
		case outcomeSkipped:
			skipped = true
		case outcomeClean:
			// Neither flag: a clean repository is what the exit status reports
			// when nothing else happened.
		}
		if ended != outcomeClean && speak {
			reports = append(reports, result{path: repoPath, report: buf.String()})
		}
	}

	for _, r := range reports {
		if strings.TrimSpace(r.report) == "" {
			continue
		}
		sayf("\n=== %s\n%s", r.path, r.report)
	}

	// Worst first, and decided by what the sweep saw rather than by which
	// repository it saw it in, so the status does not depend on the order the
	// paths were given in.
	switch {
	case failed:
		return 2
	case !cfg.exitCode:
		return 0
	case found:
		return 1
	case skipped:
		return 3
	}
	return 0
}

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

func scan(repo *gitexec.Repo, cfg config) (outcome, error) {
	who, err := targetIdentity(repo, cfg)
	if err != nil {
		return outcomeClean, err
	}
	refs, err := targetRefs(repo, cfg)
	if err != nil {
		return outcomeClean, err
	}
	// A question about the trees at the tips, so it is asked of the refs
	// themselves rather than of the commits in range: a base that narrows the
	// history to one pull request narrows nothing about what the branch ships.
	// Asked only where --agents-files asks for it, and a run that never looked
	// reports nothing and finds nothing, which an empty set already says.
	var agents agentFiles
	if cfg.agentsFiles {
		agents, err = inspectAgentFiles(repo, refs)
		if err != nil {
			return outcomeClean, err
		}
	}

	commits, err := commitsInScope(repo, cfg, refs)
	if err != nil {
		return outcomeClean, err
	}
	if len(commits) == 0 {
		if cfg.base == "" {
			sayf("no commits to inspect\n")
		} else {
			sayf("no commits to inspect: %s adds nothing over %s\n",
				strings.Join(refs, ", "), cfg.base)
		}
		agents.report(cfg.verbose)
		reportNothingToRewrite(cfg)
		return agents.outcome(), nil
	}
	// Trailers are the point of the tool. Emdashes and endashes are asked for,
	// and asking is what makes one a finding: a run that reports a dash rewrites
	// it too, so what fails a build is what apply takes back out.
	opts := clean.Options{Trailers: true, Emdashes: cfg.emdashes}

	found := inspect(opts, who, commits)
	found.report(cfg.verbose, scopeLabel(cfg, refs))
	agents.report(cfg.verbose)
	moved, err := found.reportRadius(repo, cfg, refs)
	if err != nil {
		return outcomeClean, err
	}
	carried, err := carriedTags(repo, cfg, refs, moved)
	if err != nil {
		return outcomeClean, err
	}
	reportCarried(carried)
	// A remote branch sits outside a range rather than beside it, so a run
	// given a base does not measure one against it.
	if cfg.base == "" {
		if err := reportRemoteOnly(repo, cfg, opts, who, refs); err != nil {
			return outcomeClean, err
		}
	}

	if found.flagged == 0 {
		reportNothingToRewrite(cfg)
		return agents.outcome(), nil
	}
	if !cfg.applying() {
		if len(found.changes) > 0 {
			sayf("\nnothing was rewritten. Run apply to rewrite the history\n")
		}
		return outcomeFound, nil
	}
	// A rewrite that succeeded still reports what it found, so that a job can
	// tell a run that had to change something from one that had nothing to do.
	if err := apply(repo, cfg, refs, carried, found.changes); err != nil {
		return outcomeClean, err
	}
	return outcomeFound, nil
}

// carriedTags returns the tags that name a commit the rewrite moves and are not
// in scope already. A rewritten commit gets a new hash, so a tag left out would
// go on naming history nothing else references. Their commits are in the
// rewrite either way, so carrying the tags repoints them without widening what
// is rewritten.
func carriedTags(repo *gitexec.Repo, cfg config, refs []string, moved map[string]bool) ([]target, error) {
	if len(moved) == 0 {
		return nil, nil
	}
	tags, err := repo.Tags()
	if err != nil {
		return nil, err
	}

	var carried []target
	for _, tag := range tags {
		if !moved[tag.Commit] || slices.Contains(refs, tag.Ref) {
			continue
		}
		excluded, err := cfg.exclude.matches(tag.Ref)
		if err != nil {
			return nil, err
		}
		carried = append(carried, target{ref: tag.Ref, publish: !excluded})
	}
	return carried, nil
}

// reportCarried names the tags that move without being in scope, which the refs
// the run was pointed at do not say by themselves.
func reportCarried(carried []target) {
	if len(carried) == 0 {
		return
	}
	sayf("\ntags naming a commit that changes hash, repointed along with it\n")
	for _, t := range carried {
		if t.publish {
			sayf("  %s\n", t.ref)
			continue
		}
		sayf("  %s (excluded, so it is repointed here and not pushed)\n", t.ref)
	}
}

// forkUpstream returns the remote through which a fork tracks the project it
// was forked from. A remote named upstream is the convention that git and gh
// set up; a remote that has been fetched from and points at another project is
// the general case. Both are measured against own, the remote the current
// branch tracks.
func forkUpstream(repo *gitexec.Repo, own string) (gitexec.Remote, bool, error) {
	remotes, err := repo.Remotes()
	if err != nil {
		return gitexec.Remote{}, false, err
	}
	if len(remotes) < 2 {
		return gitexec.Remote{}, false, nil
	}
	mine := ownProject(remotes, own)

	for _, remote := range remotes {
		if remote.Name == "upstream" && remote.Project != mine {
			return remote, true, nil
		}
	}
	for _, remote := range remotes {
		if remote.Project == mine {
			continue
		}
		// A remote that has never been fetched from, a deploy target for
		// instance, has brought no history here.
		refs, err := repo.RemoteRefs(remote.Name)
		if err != nil {
			return gitexec.Remote{}, false, err
		}
		if len(refs) > 0 {
			return remote, true, nil
		}
	}
	return gitexec.Remote{}, false, nil
}

// ownProject returns the project the named remote points at, which the other
// remotes are compared against. A repository whose branch tracks nothing falls
// back to the first remote.
func ownProject(remotes []gitexec.Remote, own string) string {
	for _, remote := range remotes {
		if remote.Name == own {
			return remote.Project
		}
	}
	return remotes[0].Project
}

func reportFork(repo *gitexec.Repo, upstream gitexec.Remote) {
	sayf("%s %s: a fork, tracking %s through the %s remote\n",
		paint(colorOut, blue, "skipping"), repo.Dir(), upstream.Project, upstream.Name)
	sayf("history that arrives from another project is not this repository's to rewrite\n")
}

// targetRemote is the remote the current branch tracks. A branch pushed
// somewhere other than origin would otherwise be leased and published against
// the wrong remote.
func targetRemote(repo *gitexec.Repo) string {
	if ref, err := repo.CurrentBranch(); err == nil {
		if branch, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
			if remote := repo.Config("branch." + branch + ".remote"); remote != "" {
				return remote
			}
		}
	}
	return "origin"
}

// targetIdentity resolves who agent-authored commits are re-attributed to. A
// bad --identity is always an error, but an unset git identity is only one when
// there is a rewrite to do.
func targetIdentity(repo *gitexec.Repo, cfg config) (identity, error) {
	if cfg.identity == identityNone {
		return identity{}, nil
	}

	if cfg.identity != "" {
		name, address, found := strings.Cut(strings.TrimSuffix(strings.TrimSpace(cfg.identity), ">"), "<")
		who := identity{name: strings.TrimSpace(name), address: strings.TrimSpace(address), enabled: true}
		if !found || !who.resolved() {
			return identity{}, fmt.Errorf("--identity should look like \"Name <email>\" with both parts set, or none, got %q", cfg.identity)
		}
		return who, nil
	}

	who := identity{name: repo.Config("user.name"), address: repo.Config("user.email"), enabled: true}
	if !who.resolved() {
		if cfg.applying() {
			return identity{}, errors.New("the repository has no user.name and user.email to re-attribute to; pass --identity or --identity=none")
		}
		sayf("note: no user.name and user.email are set, so agent identities are reported but cannot be rewritten\n")
		return identity{enabled: true}, nil
	}
	return who, nil
}

// targetRefs returns the refs to scan and rewrite, or nothing when the
// repository has no commits to walk.
func targetRefs(repo *gitexec.Repo, cfg config) ([]string, error) {
	var refs []string
	if cfg.currentBranch {
		branch, err := repo.CurrentBranch()
		if err != nil {
			return nil, err
		}
		// A branch with no commits yet is a valid symbolic ref that nothing
		// resolves to, which git log cannot walk.
		if _, err := repo.Resolve(branch); err != nil {
			return nil, nil //nolint:nilerr // see above: an unborn branch is not an error
		}
		refs = []string{branch}
	} else {
		found, err := repo.ListRefs()
		if err != nil {
			return nil, err
		}
		refs = found
	}

	var kept []string
	for _, ref := range refs {
		if excluded, err := cfg.exclude.matches(ref); err != nil {
			return nil, err
		} else if excluded {
			sayf("excluding %s\n", ref)
			continue
		}
		kept = append(kept, ref)
	}
	return kept, nil
}

// commitsInScope returns the commits to inspect: everything the refs in scope
// reach, or only what they add over --base, which are the commits whoever
// wrote them can still rewrite.
func commitsInScope(repo *gitexec.Repo, cfg config, refs []string) ([]gitexec.Commit, error) {
	// git log with no ref reads HEAD, which is the one thing a repository with
	// no branch does not have.
	if len(refs) == 0 {
		return nil, nil
	}
	if cfg.base == "" {
		return repo.Commits(refs)
	}
	if _, err := repo.Resolve(cfg.base); err != nil {
		return nil, fmt.Errorf("--base %q does not name a commit in this repository; fetch it first", cfg.base)
	}
	return repo.CommitsNotIn([]string{cfg.base}, refs)
}

// scopeLabel names the refs a report answers for and the base they were
// measured against, so a count cannot be read as covering more than was walked.
func scopeLabel(cfg config, refs []string) string {
	scope := strings.Join(refs, ", ")
	if cfg.base == "" {
		return scope
	}
	return scope + " since " + cfg.base
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

func apply(repo *gitexec.Repo, cfg config, refs []string, carried []target, changes map[string]rewrite.Change) error {
	if err := checkRewritable(repo, cfg); err != nil {
		return err
	}
	targets, err := collectTargets(repo, cfg, refs, carried)
	if err != nil {
		return err
	}
	if err := rewrite.Run(repo, refSpecs(cfg, targets), changes); err != nil {
		return err
	}
	resolveRewritten(repo, targets)
	reportRewritten(targets)

	publish := publishable(targets)
	if len(publish) == 0 {
		reportDonef("no ref moved, so there is nothing to push")
		return nil
	}
	// Read before reporting, so what is named as unleased is what the push
	// really cannot hold the remote to. Only a push uses the network.
	if cfg.push {
		if err := leaseRemote(repo, cfg.remote, publish); err != nil {
			return err
		}
	}
	reportUnleased(publish)

	if cfg.push {
		sayf("\npushing to %s\n", cfg.remote)
		if err := repo.Run(pushArgs(cfg.remote, publish)...); err != nil {
			return err
		}
		reportDonef("the history is rewritten and pushed to %s", cfg.remote)
		return nil
	}
	sayf("\nnot pushed. To publish the rewrite:\n\n    git %s\n",
		strings.Join(pushArgs(cfg.remote, publish), " "))
	reportDonef("the history is rewritten here, and not pushed")
	return nil
}

// refSpecs names the history git-filter-repo is pointed at. filter-repo
// re-exports everything it is given through git fast-export, which drops the
// gpgsig header, so handing it a ref rather than the range would give a signed
// commit below the base a new hash and fork the branch from what it was
// measured against.
func refSpecs(cfg config, targets []target) []string {
	specs := make([]string, 0, len(targets))
	for _, t := range targets {
		specs = append(specs, rangeSpec(cfg, t.ref))
	}
	return specs
}

// rangeSpec names one ref's history, bounded by the base when there is one. The
// rewrite and the count of what it moves ask this question, and answering it
// twice would let them disagree about what the run covers.
func rangeSpec(cfg config, ref string) string {
	if cfg.base == "" {
		return ref
	}
	return cfg.base + ".." + ref
}

func checkRewritable(repo *gitexec.Repo, cfg config) error {
	if err := rewrite.CheckAvailable(); err != nil {
		return err
	}
	// Checked before the rewrite rather than after, so a missing remote does
	// not leave a rewritten history with no way to publish it.
	if cfg.push && !repo.HasRemote(cfg.remote) {
		return fmt.Errorf("the repository has no remote named %q", cfg.remote)
	}
	isClean, err := repo.IsClean()
	if err != nil {
		return err
	}
	if !isClean {
		return errors.New("the working tree has uncommitted changes; commit or stash them first")
	}
	return nil
}

// collectTargets records where each ref pointed before the rewrite, along with
// the remote value to hold the push lease against. Carried tags are backed up
// with the refs in scope, so restore puts a repointed tag back too.
func collectTargets(repo *gitexec.Repo, cfg config, refs []string, carried []target) ([]target, error) {
	stamp := time.Now().UTC().Format("20060102T150405Z")
	targets := make([]target, 0, len(refs)+len(carried))
	for _, ref := range refs {
		targets = append(targets, target{ref: ref, publish: true})
	}
	targets = append(targets, carried...)

	for i, t := range targets {
		hash, err := repo.Resolve(t.ref)
		if err != nil {
			return nil, err
		}
		targets[i].hash = hash
		targets[i].lease = leaseFor(repo, cfg.remote, t.ref)

		if err := repo.UpdateRef(hash, backupPrefix+stamp+"/"+strings.TrimPrefix(t.ref, "refs/")); err != nil {
			return nil, err
		}
	}
	sayf("saved the pre-rewrite refs under %s%s/\n", backupPrefix, stamp)
	return targets, nil
}

// leaseFor returns the commit the remote-tracking ref points at, which is what
// the remote held when it was last fetched. The local ref is the wrong value to
// lease against, since it moves ahead of the remote on every local commit.
func leaseFor(repo *gitexec.Repo, remote, ref string) string {
	branch, ok := strings.CutPrefix(ref, "refs/heads/")
	if !ok {
		return ""
	}
	lease, err := repo.Resolve("refs/remotes/" + remote + "/" + branch)
	if err != nil {
		return ""
	}
	return lease
}

// resolveRewritten records where each ref ended up, so that what moved is read
// from the refs rather than inferred from the changes. A ref that cannot be
// resolved keeps an empty value and counts as unmoved, which leaves it out of
// the push.
func resolveRewritten(repo *gitexec.Repo, targets []target) {
	for i, t := range targets {
		if hash, err := repo.Resolve(t.ref); err == nil {
			targets[i].after = hash
		}
	}
}

func reportRewritten(targets []target) {
	sayf("\n")
	for _, t := range targets {
		if !t.moved() {
			continue
		}
		sayf("%s %s -> %s\n", t.ref, gitexec.Short(t.hash), gitexec.Short(t.after))
	}
}

// publishable returns the refs the push covers: the ones this run moved and
// owns. Pushing a ref that did not move would force a value this run did not
// produce, and a tag is forced without a lease to stop it.
func publishable(targets []target) []target {
	var publish []target
	for _, t := range targets {
		if t.publish && t.moved() {
			publish = append(publish, t)
		}
	}
	return publish
}

// reportUnleased names the refs that will be pushed without a lease, so the
// weaker guarantee is visible rather than implied.
func reportUnleased(targets []target) {
	unleased := unleasedRefs(targets)
	if len(unleased) > 0 {
		sayf("\nno value on the remote to hold %s to, so these are forced\n",
			strings.Join(unleased, ", "))
	}
}

func unleasedRefs(targets []target) []string {
	var unleased []string
	for _, t := range targets {
		if t.unleased() {
			unleased = append(unleased, t.ref)
		}
	}
	return unleased
}

// leaseRemote fills in a lease for every ref with no remote-tracking
// counterpart to read one from, a tag for instance. --force-with-lease takes an
// expected value explicitly, so the push itself refuses a ref standing
// somewhere this run never saw, rather than a check it could move behind.
func leaseRemote(repo *gitexec.Repo, remote string, targets []target) error {
	refs := unleasedRefs(targets)
	if len(refs) == 0 {
		return nil
	}
	values, err := repo.RemoteValues(remote, refs)
	if err != nil {
		return err
	}

	for i, t := range targets {
		if !t.unleased() {
			continue
		}
		// Leased against the value the rewrite started from, not the one just
		// read: a lease naming what is already there agrees to overwrite it. A
		// ref the remote does not carry stays unleased and is created.
		if _, carried := values[t.ref]; carried {
			targets[i].lease = t.hash
		}
	}
	return nil
}

// pushArgs builds the push. A ref with a lease is held to it; one without is
// forced, there being nothing on the remote to overwrite.
func pushArgs(remote string, targets []target) []string {
	// --atomic so that a ref whose lease fails takes the rest of the push down
	// with it. Half a published rewrite is a branch on new history with a tag
	// still naming the old.
	args := []string{"push", "--atomic", remote}
	for _, t := range targets {
		if t.lease != "" {
			args = append(args, fmt.Sprintf("--force-with-lease=%s:%s", t.ref, t.lease))
		}
	}
	for _, t := range targets {
		if t.lease == "" {
			args = append(args, fmt.Sprintf("+%s:%s", t.ref, t.ref))
			continue
		}
		args = append(args, fmt.Sprintf("%s:%s", t.ref, t.ref))
	}
	return args
}

func listBackups(repo *gitexec.Repo) error {
	listing, err := repo.Output("for-each-ref", "--format=%(refname) %(objectname:short)", backupPrefix)
	if err != nil {
		return err
	}
	if strings.TrimSpace(listing) == "" {
		sayf("no backups saved\n")
		return nil
	}
	for line := range strings.SplitSeq(strings.TrimSpace(listing), "\n") {
		ref, hash, _ := strings.Cut(line, " ")
		saved := strings.TrimPrefix(ref, backupPrefix)
		stamp, original, _ := strings.Cut(saved, "/")
		sayf("%s  refs/%s  %s\n", stamp, original, hash)
	}
	sayf("\nput one run back with: ai-attributions restore <timestamp>\n")
	return nil
}

func restoreBackup(repo *gitexec.Repo, stamp string) error {
	// Ref completion offers a trailing slash, which would build a prefix that
	// matches nothing.
	stamp = strings.Trim(stamp, "/")
	if stamp == "" {
		return errors.New("restore needs a backup timestamp; ai-attributions backups lists them")
	}

	prefix := backupPrefix + stamp + "/"
	listing, err := repo.Output("for-each-ref", "--format=%(refname) %(objectname)", prefix)
	if err != nil {
		return err
	}
	if strings.TrimSpace(listing) == "" {
		return fmt.Errorf("no backup saved under %s%s", backupPrefix, stamp)
	}

	restored := 0
	for line := range strings.SplitSeq(strings.TrimSpace(listing), "\n") {
		ref, hash, _ := strings.Cut(line, " ")
		saved, ok := strings.CutPrefix(ref, prefix)
		if !ok {
			return fmt.Errorf("%s is not under %s, so the ref to restore cannot be worked out", ref, prefix)
		}
		original := "refs/" + saved
		if err := repo.UpdateRef(hash, original); err != nil {
			return err
		}
		sayf("%s -> %s\n", original, gitexec.Short(hash))
		restored++
	}
	if restored == 0 {
		return fmt.Errorf("no backup saved under %s%s", backupPrefix, stamp)
	}
	sayf("\nrestored. A published rewrite still needs a force push to undo on the remote\n")
	return nil
}

// sortedByCount orders a tally's keys by descending count.
func sortedByCount(tally map[string]int) []string {
	keys := make([]string, 0, len(tally))
	for key := range tally {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(a, b int) bool {
		if tally[keys[a]] != tally[keys[b]] {
			return tally[keys[a]] > tally[keys[b]]
		}
		return keys[a] < keys[b]
	})
	return keys
}
