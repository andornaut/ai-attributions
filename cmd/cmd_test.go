package cmd

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/andornaut/ai-attributions/internal/cli"
)

// execute runs the root command as main does, and returns what it printed and
// the error it ended with.
func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()
	// The flags write into one package-level Config that outlives an Execute,
	// pflag holding pointers into it, so a value one test sets reaches the next
	// unless it is cleared here. Zeroing the Config resets every value; the
	// changed marks are cobra's own and are cleared beside them.
	cfg = cli.Config{}
	for _, c := range Cmd.Commands() {
		c.Flags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
	}
	buf := &bytes.Buffer{}
	Cmd.SetOut(buf)
	Cmd.SetErr(buf)
	Cmd.SetArgs(Args(args))
	t.Cleanup(func() {
		Cmd.SetOut(nil)
		Cmd.SetErr(nil)
		Cmd.SetArgs(nil)
	})
	return buf.String(), Cmd.Execute()
}

// A command takes the flags that describe the work it does and no others. The
// parser this replaced registered every flag for every command and rejected the
// wrong ones afterwards by hand, so the flags nobody had written a rule for
// were accepted and ignored.
func TestFlagsBelongToTheCommandsThatUseThem(t *testing.T) {
	for _, flag := range []string{
		"--agents-files", "--base=x", "--current-branch", "--emdashes",
		"--exclude=x", "--exit-code", "--identity=x", "--keep-last=1", "--push",
		"--quiet", "--verbose",
	} {
		t.Run("backups "+flag, func(t *testing.T) {
			_, err := execute(t, "backups", flag, ".")
			if err == nil {
				t.Fatalf("backups accepted %s, which does nothing there", flag)
			}
			if !strings.Contains(err.Error(), strings.SplitN(flag, "=", 2)[0]) {
				t.Errorf("the error does not name the flag: %v", err)
			}
		})
	}
}

// --push rewrites the remote, so it belongs to apply and not to the scan that
// changes nothing.
func TestPushBelongsToApplyAlone(t *testing.T) {
	if _, err := execute(t, "scan", "--push", "."); err == nil {
		t.Fatal("scan accepted --push, which has nothing to push")
	}
	if apply.Flags().Lookup("push") == nil {
		t.Error("apply does not take --push")
	}
}

// The command is optional: what came before cobra read a first argument that
// named no command as a repo-path, and scanned.
func TestArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "nothing at all scans", args: nil, want: []string{"scan"}},
		{name: "a path scans it", args: []string{"."}, want: []string{"scan", "."}},
		{name: "a flag first still scans", args: []string{"--emdashes", "."}, want: []string{"scan", "--emdashes", "."}},
		{name: "a command is left alone", args: []string{"apply", "."}, want: []string{"apply", "."}},
		{name: "backups is left alone", args: []string{"backups"}, want: []string{"backups"}},
		{name: "help is the root's", args: []string{"--help"}, want: []string{"--help"}},
		{name: "-h is the root's", args: []string{"-h"}, want: []string{"-h"}},
		// The flag package this replaced took one dash or two, and the
		// documentation says so; pflag reads one dash as shorthands.
		{name: "a single dash is widened", args: []string{"-emdashes", "."}, want: []string{"scan", "--emdashes", "."}},
		{name: "a single dash with a value", args: []string{"-base=x", "."}, want: []string{"scan", "--base=x", "."}},
		{name: "operands after -- are left", args: []string{"scan", "--", "-weird-path"}, want: []string{"scan", "--", "-weird-path"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Args(tt.args); !slices.Equal(got, tt.want) {
				t.Errorf("Args(%q) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

// Every command cobra knows about is left alone, the ones it registers for
// itself included. cobra adds help, completion and the shell-completion
// requests inside Execute, which runs after Args, so a command missing from the
// list Args reads is taken for a repo-path: shell completion would scan the
// working directory on every press of TAB instead of answering.
func TestArgsLeavesEveryCommandAlone(t *testing.T) {
	registered := Cmd.Commands()
	names := make([]string, 0, len(registered)+2)
	names = append(names, cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd)
	for _, c := range registered {
		names = append(names, c.Name())
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			got := Args([]string{name})
			if len(got) == 0 || got[0] != name {
				t.Errorf("Args([%q]) = %q, want it left alone", name, got)
			}
		})
	}
	// The two cobra registers late are the ones worth naming: a list that has
	// them only because this test ran Execute first would prove nothing.
	for _, want := range []string{"help", "completion"} {
		if !slices.Contains(names, want) {
			t.Errorf("%s is not registered before Args reads the command list", want)
		}
	}
}

// clean names one run or bounds how many are kept, and a command line asking
// for both has asked for two different things. A count below zero keeps no run
// this side of the bare command, which already removes every backup.
func TestCleanTakesATimestampOrABound(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "both",
			args: []string{"clean", "--keep-last=1", "20260811T121757Z"},
			want: "not both",
		},
		{
			name: "a count below zero",
			args: []string{"clean", "--keep-last=-2"},
			want: "takes a count",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := execute(t, append(tt.args, t.TempDir())...)
			if err == nil {
				t.Fatalf("%q was accepted", tt.args)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("the error is %q, want it to name %q", err, tt.want)
			}
		})
	}
}

// restore reads a timestamp before a path, so a lone path is refused rather
// than taken for the backup to put back.
func TestRestoreNeedsATimestamp(t *testing.T) {
	for _, args := range [][]string{{"restore"}, {"restore", "notatimestamp"}} {
		if _, err := execute(t, args...); err == nil {
			t.Errorf("%q was accepted", args)
		}
	}
	if _, err := execute(t, "restore", "20260811T121757Z", t.TempDir()); err == nil {
		t.Error("a well formed timestamp was refused")
	}
}
