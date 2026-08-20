// What the command package needs to report a wrong invocation and an exit
// status, kept beside the run it describes.

package cli

import (
	"errors"
	"fmt"
)

// Exit codes. A finding and a fork are statuses a caller reads rather than
// failures, so they are carried the same way and separated here.
const (
	ExitClean   = 0
	ExitFound   = 1
	ExitFailed  = 2
	ExitSkipped = 3
)

// UsageError marks a wrong invocation: an unknown command, an unknown flag, or
// a flag the command does not take. ai-attributions exits 2 for these and for a
// run that could not complete, so a caller can tell either from a finding.
type UsageError struct{ err error }

func (e UsageError) Error() string { return e.err.Error() }

func (e UsageError) Unwrap() error { return e.err }

// Usage marks an existing error as a wrong invocation.
func Usage(err error) error { return UsageError{err} }

// Usagef reports a wrong invocation, as an argument validator does.
func Usagef(format string, a ...any) error { return UsageError{fmt.Errorf(format, a...)} }

// statusError carries a status that is not a failure, so that a finding under
// --exit-code reaches the caller without a message describing it as an error.
// The command silences the error before returning one.
type statusError struct{ code int }

func (e *statusError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

// Status turns an exit status into the error a cobra RunE returns, and nil for
// a status of zero, which is a run with nothing to report.
func Status(code int) error {
	if code == ExitClean {
		return nil
	}
	return &statusError{code}
}

// ExitCode returns the status to exit with for the given error. A wrong
// invocation and a failed run both exit 2: the message says which, and no
// caller has a reason to act differently.
func ExitCode(err error) int {
	if err == nil {
		return ExitClean
	}
	if s, ok := errors.AsType[*statusError](err); ok {
		return s.code
	}
	return ExitFailed
}
