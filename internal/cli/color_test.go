package cli

import (
	"strings"
	"testing"
)

// A stream that is not a terminal gets the report as plain text, so a redirect
// or a CI log carries no escape codes.
func TestPaint(t *testing.T) {
	if got := paint(false, red, "failed"); got != "failed" {
		t.Errorf("paint(false) = %q, want the text unchanged", got)
	}
	if got := paint(true, red, "failed"); got != red+"failed"+reset {
		t.Errorf("paint(true) = %q, want the text colored and reset", got)
	}
}

// A sweep's outcome column is padded before it is colored: an escape code
// counts toward a printf width and would knock the paths that follow out of
// line.
func TestColumnPadsBeforeColoring(t *testing.T) {
	padded := "clean" + strings.Repeat(" ", outcomeWidth-len("clean"))
	if got := column(true, green, "clean"); got != green+padded+reset {
		t.Errorf("column() = %q, want %q", got, green+padded+reset)
	}
	if got := column(false, green, "clean"); got != padded {
		t.Errorf("column() = %q, want the padding without color", got)
	}
}

// The column is as wide as the longest word that can appear in it, so a word
// added later lines up with the rest rather than pushing its path along.
func TestColumnFitsEveryOutcome(t *testing.T) {
	for _, o := range outcomes() {
		if got := column(false, green, o.String()); len(got) != outcomeWidth {
			t.Errorf("column(%s) is %d wide, want %d", o, len(got), outcomeWidth)
		}
	}
	if got := column(false, green, "failed"); len(got) != outcomeWidth {
		t.Errorf("column(failed) is %d wide, want %d", len(got), outcomeWidth)
	}
}

// Every outcome a sweep prints has a color, and they differ: the line says
// which repositories need attention without being read.
func TestOutcomeColors(t *testing.T) {
	seen := map[string]outcome{}
	for _, o := range outcomes() {
		color := o.color()
		if color == "" {
			t.Errorf("outcome(%s).color() is empty", o)
		}
		if other, taken := seen[color]; taken {
			t.Errorf("outcome(%s) and outcome(%s) share a color", o, other)
		}
		seen[color] = o
	}
}
