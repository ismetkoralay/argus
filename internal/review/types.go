// Package review holds the types and orchestration logic for turning a PR
// diff into posted findings. It defines the Provider interface at the
// consumer (this package), implemented elsewhere (e.g. internal/llm).
package review

import "context"

// Finding is a single structured review comment the LLM produced for a
// DiffUnit.
type Finding struct {
	File       string `json:"file"`
	Line       int    `json:"line"`
	Severity   string `json:"severity"` // info | warning | error
	Category   string `json:"category"` // bug | security | performance | style | maintainability
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
}

// DiffUnit is a reviewable slice of a PR's diff: either a whole file's
// patch, or one hunk of it, along with the file path it belongs to.
type DiffUnit struct {
	File string
	Hunk string
}

// Config carries per-review settings that shape how a Provider
// reasons about a DiffUnit (e.g. persona/tone). Severity floor and comment
// cap are orchestrator-level concerns, not passed here.
type Config struct {
	Persona string
}

// Provider reviews a single DiffUnit and returns the findings it produced.
type Provider interface {
	Review(ctx context.Context, unit DiffUnit, cfg Config) ([]Finding, error)
}
