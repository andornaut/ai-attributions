// The values a run is described by, shared by every command.

package cli

import "regexp"

const (
	backupPrefix = "refs/ai-attributions-backup/"
	identityNone = "none"
)

// defaultKeepRuns bounds the backup namespace: the number of earlier runs whose
// pre-rewrite refs a rewrite leaves in place, its own snapshot sitting above
// the bound until the next one prunes to it again. The oldest goes first, and
// across a sequence of rewrites that is the one naming the history the first
// started from, so this is how far back restore can reach. A run that moves
// nothing leaves no snapshot, so these are three rewrites rather than three
// invocations, and clean --keep-last asks for another number.
const defaultKeepRuns = 3

// stampLayout is how a backup's timestamp is spelled, and stampRe matches what
// it spells. UTC and fixed width, so ordering the stamps as strings orders the
// runs by time.
const stampLayout = "20060102T150405Z"

var stampRe = regexp.MustCompile(`^\d{8}T\d{6}Z$`)

type Config struct {
	// AgentsFiles asks for the instruction files the refs in scope carry. Off
	// by default, as --emdashes is: an agent instruction file is a contributor's
	// business until a repository says otherwise, and a scan that reported one
	// unasked would fail a build over a file no rewrite here takes back out.
	AgentsFiles bool
	Base        string
	// CurrentBranch narrows the run to the branch that is checked out. The
	// default is every local branch and tag, "is this repository clean" being
	// the question the tool answers.
	CurrentBranch bool
	Emdashes      bool
	Exclude       refPatterns
	ExitCode      bool
	Identity      string
	// KeepLast is the number of runs clean keeps, and zero for the bare
	// command, which takes every backup away. A rewrite prunes to
	// defaultKeepRuns with no flag at all, so a bounded namespace is what a run
	// leaves behind rather than something to remember to ask for.
	KeepLast int
	Push     bool
	// Quiet holds the report back unless the run has something to answer for,
	// which is what lets a scheduled run mail only the days that need one.
	Quiet   bool
	Verbose bool

	// Remote is resolved from the branch's upstream, rather than assumed to be
	// origin.
	Remote string
}

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

// An Op is the work a command asks of each repository. The command passes one
// rather than naming itself, so a command that does not exist does not compile
// and the run never compares a string to find out what it was asked for.
type Op int

const (
	OpScan Op = iota
	OpApply
	OpBackups
	OpClean
	OpRestore
)

// rewrites reports whether the op changes the history rather than reporting on
// it, which is what decides whether a run closes by saying what it did.
func (o Op) rewrites() bool { return o == OpApply }

// walksHistory reports whether the op looks for attributions at all. backups,
// clean and restore only read or move refs this tool saved, so they have no
// finding to summarize and no scope to report one for.
func (o Op) walksHistory() bool { return o == OpScan || o == OpApply }
