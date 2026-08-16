//go:build unix

package cli

import (
	"os/signal"
	"syscall"
)

// Writing to a closed stdout, which is what piping into head or quitting a
// pager does, otherwise kills the process on the spot. A rewrite in progress
// has to finish, so the write is left to fail instead.
func init() {
	signal.Ignore(syscall.SIGPIPE)
}
