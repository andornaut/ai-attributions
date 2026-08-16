// Command ai-attributions removes AI attributions from a repository's git
// history: co-author and session trailers, "generated with" footers, and the
// agent identities on the commits themselves. --emdashes adds the emdashes and
// endashes in the messages in scope, wherever they appear, and --agents-files
// adds the agent instruction files the refs in scope carry, which are found
// rather than removed: they are files in a tree, and the rewrite replaces
// messages and identities. Both are off unless asked for, so what a run reports
// is what it was pointed at.
package main

import (
	"os"

	"github.com/andornaut/ai-attributions/cmd"
	"github.com/andornaut/ai-attributions/internal/cli"
)

func main() {
	cmd.Cmd.SetArgs(cmd.Args(os.Args[1:]))
	if err := cmd.Cmd.Execute(); err != nil {
		os.Exit(cli.ExitCode(err))
	}
}
