package review

import (
	"path/filepath"
	"strings"

	"github.com/ismetkoralay/argus/internal/githubapp"
)

// maxUnitChars is a rough token-budget heuristic (characters, not tokens):
// patches larger than this are split per hunk instead of sent as one unit.
const maxUnitChars = 4000

// lockfileNames are skipped regardless of directory: they're generated,
// huge, and not worth an LLM's attention. Full .argus.yml ignore globs are
// M2.
var lockfileNames = map[string]bool{
	"go.sum":            true,
	"package-lock.json": true,
	"yarn.lock":         true,
	"pnpm-lock.yaml":    true,
}

// BuildDiffUnits turns a PR's changed files into reviewable DiffUnits: one
// per file normally, or one per hunk when a file's patch exceeds the token
// budget.
func BuildDiffUnits(files []githubapp.PRFile) []DiffUnit {
	var units []DiffUnit
	for _, f := range files {
		if f.Patch == "" {
			continue // binary or too large; GitHub omits the patch
		}
		if lockfileNames[filepath.Base(f.Filename)] {
			continue
		}

		if len(f.Patch) <= maxUnitChars {
			units = append(units, DiffUnit{File: f.Filename, Hunk: f.Patch})
			continue
		}

		for _, hunk := range splitHunks(f.Patch) {
			units = append(units, DiffUnit{File: f.Filename, Hunk: hunk})
		}
	}
	return units
}

// splitHunks splits a unified diff patch into its individual "@@ ... @@"
// hunks, each hunk text including its header through to (but not
// including) the next hunk header.
func splitHunks(patch string) []string {
	lines := strings.Split(patch, "\n")

	var hunks []string
	var current []string
	for _, line := range lines {
		if strings.HasPrefix(line, "@@ ") && len(current) > 0 {
			hunks = append(hunks, strings.Join(current, "\n"))
			current = nil
		}
		current = append(current, line)
	}
	if len(current) > 0 {
		hunks = append(hunks, strings.Join(current, "\n"))
	}
	return hunks
}
