package cli

import (
	"fmt"
	"os"
)

// The report is a wall of lowercase prose, where the line saying a run stopped
// early reads like the lines saying what it found. These mark how a run ended.
const (
	red     = "\x1b[31m"
	green   = "\x1b[32m"
	yellow  = "\x1b[33m"
	blue    = "\x1b[34m"
	magenta = "\x1b[35m"
	cyan    = "\x1b[36m"
	reset   = "\x1b[0m"
)

// colorOut and colorErr are decided once per stream, so that a report that is
// piped, redirected or read by a CI job stays plain text. A sweep writes each
// repository's report to a buffer, but the buffer is written to this same
// stdout, so it is still the stream the decision answers for.
// https://no-color.org
var (
	colorOut = isTerminal(os.Stdout)
	colorErr = isTerminal(os.Stderr)
)

func isTerminal(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// interleaved reports whether the report and the failures land in the same
// place, one terminal or one redirect, which is what makes a line written to
// one of them read as following the other.
var interleaved = sameFile(os.Stdout, os.Stderr)

func sameFile(a, b *os.File) bool {
	first, err := a.Stat()
	if err != nil {
		return false
	}
	second, err := b.Stat()
	if err != nil {
		return false
	}
	return os.SameFile(first, second)
}

func paint(enabled bool, color, text string) string {
	if !enabled {
		return text
	}
	return color + text + reset
}

// outcomeWidth is the width of a sweep's first column, taken from the longest
// word that can appear in it rather than written down, so that adding a word
// cannot knock the paths out of line.
var outcomeWidth = widestOutcome()

func widestOutcome() int {
	// failed is not an outcome: it is a run that could not finish one. It
	// prints in the same column all the same.
	widest := len("failed")
	for _, o := range outcomes() {
		if width := len(o.String()); width > widest {
			widest = width
		}
	}
	return widest
}

// column pads a sweep's outcome to the column width, then colors it. An escape
// code counts toward a printf width, so coloring first would knock the paths
// that follow out of line.
func column(enabled bool, color, text string) string {
	return paint(enabled, color, fmt.Sprintf("%-*s", outcomeWidth, text))
}
