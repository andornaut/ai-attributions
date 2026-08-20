// The repeatable --exclude flag: the pflag.Value that collects ref patterns,
// and the match it tests a ref with.

package cli

import (
	"fmt"
	"path"
	"strings"
)

// refPatterns collects the repeatable --exclude flag.
type refPatterns []string

func (p *refPatterns) String() string { return strings.Join(*p, ",") }

func (p *refPatterns) Set(v string) error { *p = append(*p, v); return nil }

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

// Type names the value in a usage line. pflag asks for it; the flag package
// this replaced derived the same word from the backquotes in the description.
func (p *refPatterns) Type() string { return "glob" }
