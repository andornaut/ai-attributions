package main

import "testing"

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
	want := green + "clean   " + reset
	if got := column(true, green, "clean"); got != want {
		t.Errorf("column() = %q, want %q", got, want)
	}
	if got := column(false, green, "clean"); got != "clean   " {
		t.Errorf("column() = %q, want the padding without color", got)
	}
}

// Every outcome a sweep prints has a color, and they differ: the line says
// which repositories need attention without being read.
func TestOutcomeColors(t *testing.T) {
	seen := map[string]outcome{}
	for _, o := range []outcome{outcomeClean, outcomeFound, outcomeSkipped} {
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
