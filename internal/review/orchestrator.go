package review

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/ismetkoralay/argus/internal/githubapp"
)

// Hardcoded for M1; become configurable via .argus.yml in M2.
const (
	concurrency   = 4
	severityFloor = "info"
	commentCap    = 15
)

var severityRank = map[string]int{"info": 0, "warning": 1, "error": 2}

const summaryCommentMarker = "<!-- argus-summary -->"

// GithubClient is the subset of githubapp.Client the orchestrator needs to
// fetch a PR's diff and post its findings.
type GithubClient interface {
	ListPRFiles(ctx context.Context, installationID int64, owner, repo string, prNumber int) ([]githubapp.PRFile, error)
	CreateReview(ctx context.Context, installationID int64, owner, repo string, prNumber int, commitSHA string, comments []githubapp.InlineComment, event, body string) error
	UpsertSummaryComment(ctx context.Context, installationID int64, owner, repo string, prNumber int, body string) error
}

// Orchestrator fans a PR's diff out to a Provider, aggregates the
// findings, and posts them as a GitHub review plus a summary comment.
type Orchestrator struct {
	provider Provider
	github   GithubClient
	logger   *slog.Logger
}

// NewOrchestrator builds an Orchestrator. logger defaults to slog.Default()
// when nil.
func NewOrchestrator(provider Provider, github GithubClient, logger *slog.Logger) *Orchestrator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Orchestrator{provider: provider, github: github, logger: logger}
}

// ReviewPR fetches the PR's diff, reviews it, and posts inline comments
// plus a summary comment. Provider errors on individual units are logged
// and skipped rather than failing the whole review.
func (o *Orchestrator) ReviewPR(ctx context.Context, installationID int64, owner, repo string, prNumber int, headSHA string) error {
	files, err := o.github.ListPRFiles(ctx, installationID, owner, repo, prNumber)
	if err != nil {
		return fmt.Errorf("list PR files: %w", err)
	}
	units := BuildDiffUnits(files)

	findings := o.reviewUnits(ctx, units)
	findings = filterBySeverityFloor(findings)
	findings = capFindings(findings)

	if len(findings) > 0 {
		comments := make([]githubapp.InlineComment, 0, len(findings))
		for _, f := range findings {
			comments = append(comments, githubapp.InlineComment{Path: f.File, Line: f.Line, Body: formatFindingBody(f)})
		}
		if err := o.github.CreateReview(ctx, installationID, owner, repo, prNumber, headSHA, comments, "COMMENT", ""); err != nil {
			return fmt.Errorf("create review: %w", err)
		}
	}

	summary := buildSummary(findings, len(files))
	if err := o.github.UpsertSummaryComment(ctx, installationID, owner, repo, prNumber, summary); err != nil {
		return fmt.Errorf("upsert summary comment: %w", err)
	}
	return nil
}

// reviewUnits fans units out to the provider with bounded concurrency,
// logging and skipping any unit whose review fails.
func (o *Orchestrator) reviewUnits(ctx context.Context, units []DiffUnit) []Finding {
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var findings []Finding

	for _, unit := range units {
		wg.Add(1)
		sem <- struct{}{}
		go func(unit DiffUnit) {
			defer wg.Done()
			defer func() { <-sem }()

			unitFindings, err := o.provider.Review(ctx, unit, Config{})
			if err != nil {
				o.logger.Error("skipping diff unit after provider error", "err", err, "file", unit.File)
				return
			}
			mu.Lock()
			findings = append(findings, unitFindings...)
			mu.Unlock()
		}(unit)
	}
	wg.Wait()
	return findings
}

func filterBySeverityFloor(findings []Finding) []Finding {
	floor := severityRank[severityFloor]
	kept := make([]Finding, 0, len(findings))
	for _, f := range findings {
		if severityRank[f.Severity] >= floor {
			kept = append(kept, f)
		}
	}
	return kept
}

// capFindings deterministically orders findings (highest severity first,
// then file, then line) and truncates to commentCap.
func capFindings(findings []Finding) []Finding {
	sort.SliceStable(findings, func(i, j int) bool {
		if severityRank[findings[i].Severity] != severityRank[findings[j].Severity] {
			return severityRank[findings[i].Severity] > severityRank[findings[j].Severity]
		}
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})
	if len(findings) > commentCap {
		findings = findings[:commentCap]
	}
	return findings
}

var severityEmoji = map[string]string{"error": "🚨", "warning": "⚠️", "info": "ℹ️"}

func formatFindingBody(f Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s **[%s]** %s", severityEmoji[f.Severity], f.Category, f.Message)
	if f.Suggestion != "" {
		fmt.Fprintf(&b, "\n\n*Suggestion:* %s", f.Suggestion)
	}
	return b.String()
}

func buildSummary(findings []Finding, filesReviewed int) string {
	bySeverity := map[string]int{}
	byCategory := map[string]int{}
	for _, f := range findings {
		bySeverity[f.Severity]++
		byCategory[f.Category]++
	}

	verdict := "looks good"
	if bySeverity["error"] > 0 {
		verdict = "needs attention"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", summaryCommentMarker)
	fmt.Fprintf(&b, "🛡️ **Argus review** — %d findings", len(findings))
	if len(byCategory) > 0 {
		parts := make([]string, 0, len(byCategory))
		for _, cat := range []string{"bug", "security", "performance", "style", "maintainability"} {
			if n := byCategory[cat]; n > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", n, cat))
			}
		}
		fmt.Fprintf(&b, " (%s)", strings.Join(parts, ", "))
	}
	fmt.Fprintf(&b, " across %d files. Overall: %s.\n", filesReviewed, verdict)
	fmt.Fprintf(&b, "Reviewed %d/%d changed files.", filesReviewed, filesReviewed)
	return b.String()
}
