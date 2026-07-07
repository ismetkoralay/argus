// Package repoconfig loads and validates a repo's .argus.yml, the
// per-repo configuration described in TECH_DESIGN.md §3.6. It is
// intentionally free of any GitHub I/O: callers fetch the raw file bytes
// (or none, if absent) and pass them to Parse.
package repoconfig

import (
	"fmt"
	"slices"

	"gopkg.in/yaml.v3"
)

// Path is the well-known config file name Argus looks for on a PR's head
// ref.
const Path = ".argus.yml"

// Config carries the per-repo settings that shape a review: which findings
// get posted (severity/category/ignore filters), how much of the PR gets
// reviewed (max_files), how noisy the result can be (max_comments), and the
// review's tone (persona).
type Config struct {
	MinSeverity string   `yaml:"min_severity"`
	Categories  []string `yaml:"categories"`
	Ignore      []string `yaml:"ignore"`
	MaxFiles    int      `yaml:"max_files"`
	MaxComments int      `yaml:"max_comments"`
	Persona     string   `yaml:"persona"`
}

// allowedSeverities and allowedCategories mirror the enums llm.OllamaProvider
// validates findings against.
var (
	allowedSeverities = []string{"info", "warning", "error"}
	allowedCategories = []string{"bug", "security", "performance", "style", "maintainability"}
)

// Default is used for any field absent from .argus.yml, and returned in
// full when the file is missing or fails to parse/validate.
var Default = Config{
	MinSeverity: "info",
	Categories:  []string{"bug", "security", "performance", "style", "maintainability"},
	Ignore:      nil,
	MaxFiles:    25,
	MaxComments: 15,
	Persona:     "",
}

// Parse parses raw .argus.yml bytes, merging any fields present in raw over
// Default — yaml.Unmarshal only overwrites fields present in the document,
// so unmarshalling into a Default-initialized struct gives merge-for-free
// for a partial file. Empty raw returns Default, nil (no file present).
//
// A YAML syntax error or an invalid field value (unknown severity/category,
// non-positive max_files/max_comments) returns Default alongside a wrapped
// error: callers should log the error and fall back to the returned Default
// rather than failing the review.
func Parse(raw []byte) (Config, error) {
	cfg := Default
	if len(raw) == 0 {
		return cfg, nil
	}

	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Default, fmt.Errorf("parse %s: %w", Path, err)
	}

	if err := validate(cfg); err != nil {
		return Default, fmt.Errorf("validate %s: %w", Path, err)
	}
	return cfg, nil
}

func validate(cfg Config) error {
	if !slices.Contains(allowedSeverities, cfg.MinSeverity) {
		return fmt.Errorf("min_severity %q must be one of %v", cfg.MinSeverity, allowedSeverities)
	}
	if len(cfg.Categories) == 0 {
		return fmt.Errorf("categories must not be empty")
	}
	for _, cat := range cfg.Categories {
		if !slices.Contains(allowedCategories, cat) {
			return fmt.Errorf("categories: %q must be one of %v", cat, allowedCategories)
		}
	}
	if cfg.MaxFiles <= 0 {
		return fmt.Errorf("max_files must be > 0, got %d", cfg.MaxFiles)
	}
	if cfg.MaxComments <= 0 {
		return fmt.Errorf("max_comments must be > 0, got %d", cfg.MaxComments)
	}
	return nil
}
