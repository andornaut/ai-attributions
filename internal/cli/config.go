// The values a run is described by, shared by every command.

package cli

import "regexp"

const (
	backupPrefix = "refs/ai-attributions-backup/"
	identityNone = "none"
)

// stampRe matches the timestamp a backup is saved under.
var stampRe = regexp.MustCompile(`^\d{8}T\d{6}Z$`)

type Config struct {
	Command string
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
	Push          bool
	// Quiet holds the report back unless the run has something to answer for,
	// which is what lets a scheduled run mail only the days that need one.
	Quiet   bool
	Verbose bool

	// Remote is resolved from the branch's upstream, rather than assumed to be
	// origin.
	Remote string
}

func (c Config) applying() bool { return c.Command == "apply" }

// scanning reports whether the command walks the history looking for
// attributions, which is what has a finding to report and a scope to report it
// for. backups and restore only move refs this tool saved.
func (c Config) scanning() bool { return c.Command == "scan" || c.applying() }

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
