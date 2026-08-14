// Package clean rewrites commit messages to remove AI attribution trailers and
// emdashes, and names the identities and instruction-file paths an agent leaves
// behind.
package clean

import (
	"regexp"
	"strings"
)

// Options selects which transformations to apply.
type Options struct {
	Trailers bool
	Emdashes bool
}

type LineChange struct {
	Old string
	New string
}

// Findings describes what Message changes in a commit message.
type Findings struct {
	RemovedLines []string
	ChangedLines []LineChange
}

func (f Findings) Empty() bool {
	return len(f.RemovedLines) == 0 && len(f.ChangedLines) == 0
}

var (
	// unambiguousAgent matches names that only ever refer to an AI agent or its
	// vendor, and so are evidence on their own.
	unambiguousAgent = regexp.MustCompile(`(?i)\b(claude|anthropic|chatgpt|openai|copilot|codeium|windsurf|aider|tabnine|codewhisperer)\b`)

	// ambiguousAgent matches agent names that are also human given names. These
	// count only alongside a bot identity, a vendor domain, or a product word.
	ambiguousAgent = regexp.MustCompile(`(?i)\b(devin|jules|cursor|codex|gemini|amp)\b`)

	// productContext matches the words that turn an ambiguous name into a
	// product name.
	productContext = regexp.MustCompile(`(?i)\b(agent|assist|assistant|bot|cli|code|connector|integration|labs)\b`)

	// vendorDomain matches a host belonging to an AI agent vendor.
	vendorDomain = regexp.MustCompile(`(?i)(^|[@./])(anthropic\.com|claude\.ai|openai\.com|chatgpt\.com|cursor\.(com|sh)|cognition\.ai|codeium\.com|windsurf\.com|githubcopilot\.com)$`)

	// botIdentity matches the account shape that AI integrations commit under.
	botIdentity = regexp.MustCompile(`(?i)\[bot\]|\bbot@|-bot\b`)

	// trailerRe splits a "Key: value" trailer line.
	trailerRe = regexp.MustCompile(`^[ \t]*([A-Za-z][A-Za-z0-9-]*)[ \t]*:[ \t]*(.*)$`)

	// identityRe splits a "Display Name <local@host>" trailer value.
	identityRe = regexp.MustCompile(`^(.*?)[ \t]*<([^>]*)>[ \t]*$`)

	// sessionKeyRe matches trailer keys that link to an agent transcript.
	sessionKeyRe = regexp.MustCompile(`(?i)^(claude|codex|cursor|devin|agent|ai)-session$`)

	// generatedFooterRe matches a line whose whole point is to say that an
	// agent produced the commit. It is anchored so that body prose mentioning
	// an agent in passing is left alone.
	generatedFooterRe = regexp.MustCompile(`(?i)^[ \t*_-]*[\x{1F916}\x{1F9E0}\x{2728}]?[ \t*_-]*(co-)?(generated|created|written|authored|produced)[ \t]+(with|by|using)\b`)

	// markerRe matches a line holding only agent decoration.
	markerRe = regexp.MustCompile(`^[ \t]*[\x{1F916}\x{1F9E0}\x{2728}]+[ \t]*$`)
)

// attributionKeys are trailer keys that attribute authorship. A line is dropped
// only when its value also names an AI agent.
var attributionKeys = map[string]bool{
	"co-authored-by": true,
	"coauthored-by":  true,
	"assisted-by":    true,
	"ai-assisted-by": true,
	"generated-by":   true,
	"generated-with": true,
	"signed-off-by":  true,
}

// dashes are the characters a rewrite replaces with a hyphen: the emdash and
// the endash, both of which a hyphen says as well. The figure dash and the
// horizontal bar are left alone, being typography for a numeric span and for
// quoted speech rather than punctuation a hyphen stands in for.
const dashes = "—–"

// urlOrDashRe matches a URL or a run of those dashes. The URL alternative comes
// first and is matched only to step over it, since a dash inside one is part of
// the address rather than punctuation.
var urlOrDashRe = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s<>]+|[` + dashes + `]+`)

// Apply returns msg with the selected transformations applied, along with what
// they changed. One walk answers both, since a caller decides from the findings
// whether the rewritten message is one it wants.
func Apply(opts Options, msg string) (string, Findings) {
	var findings Findings

	body := strings.TrimRight(msg, "\n")
	hadNewline := len(body) < len(msg)
	lines := strings.Split(body, "\n")

	if opts.Trailers {
		kept := make([]string, 0, len(lines))
		for i, line := range lines {
			// The subject line never carries an attribution, and dropping it
			// would leave the commit without one.
			if i > 0 && isAttribution(line) {
				findings.RemovedLines = append(findings.RemovedLines, line)
				continue
			}
			kept = append(kept, line)
		}
		lines = kept
	}

	if opts.Emdashes {
		for i, line := range lines {
			replaced := replaceDashes(line)
			if replaced != line {
				findings.ChangedLines = append(findings.ChangedLines, LineChange{Old: line, New: replaced})
				lines[i] = replaced
			}
		}
	}

	// A dash pass replaces rather than removes, so only a dropped trailer can
	// leave a gap. A message nothing touched keeps its original spacing.
	if len(findings.RemovedLines) > 0 {
		lines = collapseBlanks(lines)
	}

	out := strings.Join(lines, "\n")
	if hadNewline {
		out += "\n"
	}
	return out, findings
}

func isAttribution(line string) bool {
	if markerRe.MatchString(line) {
		return true
	}
	if m := trailerRe.FindStringSubmatch(line); m != nil {
		key := strings.ToLower(m[1])
		if sessionKeyRe.MatchString(key) {
			return true
		}
		if attributionKeys[key] && namesAgent(m[2]) {
			return true
		}
	}
	return generatedFooterRe.MatchString(line) && mentionsAgent(line)
}

// Identity reports whether a commit's name and address identify an AI agent
// rather than a person. It is the same test applied to attribution trailers.
func Identity(name, address string) bool {
	return namesAgent(name + " <" + address + ">")
}

// namesAgent reports whether an identity is an AI agent rather than a person.
// The display name and the address are weighed separately so that a vendor name
// appearing in an unrelated hostname is not taken as evidence.
//
// A bot account is not evidence on its own: dependabot, renovate and
// github-actions are bots that no agent wrote, so an account has to name an
// agent as well.
func namesAgent(value string) bool {
	name, address := splitIdentity(value)
	local, host, _ := strings.Cut(address, "@")

	if vendorDomain.MatchString(host) {
		return true
	}
	if unambiguousAgent.MatchString(name) || unambiguousAgent.MatchString(local) {
		return true
	}
	if ambiguousAgent.MatchString(name) || ambiguousAgent.MatchString(local) {
		return productContext.MatchString(value) || botIdentity.MatchString(value)
	}
	return false
}

// mentionsAgent reports whether free-form text names an AI agent. It is looser
// than namesAgent, so only anchored footer lines should be tested with it.
func mentionsAgent(text string) bool {
	if unambiguousAgent.MatchString(text) || botIdentity.MatchString(text) {
		return true
	}
	for field := range strings.FieldsSeq(strings.Map(hostRunes, text)) {
		if vendorDomain.MatchString(field) {
			return true
		}
	}
	return ambiguousAgent.MatchString(text) && productContext.MatchString(text)
}

// hostRunes keeps the characters a hostname can hold and blanks the rest, so a
// host embedded in a URL or in markup can be matched on its own.
func hostRunes(r rune) rune {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return r
	case r == '.', r == '-':
		return r
	}
	return ' '
}

// splitIdentity separates "Display Name <local@host>". A value without an
// address is all display name.
func splitIdentity(value string) (name, address string) {
	if m := identityRe.FindStringSubmatch(value); m != nil {
		return strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
	}
	return strings.TrimSpace(value), ""
}

// collapseBlanks removes trailing blank lines and reduces runs of blank lines
// to one.
func collapseBlanks(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		blank := strings.TrimSpace(line) == ""
		if blank && len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			continue
		}
		out = append(out, line)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

// replaceDashes rewrites an emdash or an endash as a hyphen, whatever the dash
// is doing: a run of them is one hyphen, and the spacing around it is left as it
// was.
func replaceDashes(line string) string {
	// Most lines hold no such dash at all, and the scan reads every line of
	// every commit message.
	if !strings.ContainsAny(line, dashes) {
		return line
	}
	return urlOrDashRe.ReplaceAllStringFunc(line, func(match string) string {
		if strings.Contains(match, "://") {
			return match
		}
		return "-"
	})
}
