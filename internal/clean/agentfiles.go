package clean

import "slices"

// agentFiles are the paths an agent reads its instructions from. They configure
// the tools a contributor happens to run rather than describe the project, so a
// repository that ships one hands its prompts to everyone who clones it.
//
// Each entry is a path to look up at a ref, not a pattern to match every blob
// against, which is what keeps the check the cost of a tree descent per ref
// rather than a walk of every tree in scope. That is also its limit: an
// instruction file nested somewhere else, a monorepo's per-package AGENTS.md
// for instance, is not at one of these paths and is not found.
//
// Sorted, so that a report listing them reads the same way twice.
var agentFiles = []string{
	".aider.conf.yml",
	".clinerules",
	".cursor/rules",
	".cursorrules",
	".github/copilot-instructions.md",
	".junie/guidelines.md",
	".roorules",
	".windsurfrules",
	"AGENT.md",
	"AGENTS.md",
	"CLAUDE.md",
	"GEMINI.md",
	"QWEN.md",
}

// AgentFiles returns the paths an agent's instructions live at.
func AgentFiles() []string { return slices.Clone(agentFiles) }
