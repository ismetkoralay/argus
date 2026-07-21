package review

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/ismetkoralay/argus/internal/githubapp"
	"github.com/ismetkoralay/argus/internal/logging"
	"github.com/ismetkoralay/argus/internal/repoconfig"
)

// concurrency bounds how many diff units are sent to the provider at once,
// to keep a local Ollama instance responsive. Everything else that used to
// be hardcoded here (severity floor, comment cap, ...) now comes from
// .argus.yml via repoconfig; see loadConfig.
const concurrency = 4

var severityRank = map[string]int{"info": 0, "warning": 1, "error": 2}

const summaryCommentMarker = "<!-- argus-summary -->"

// GithubClient is the subset of githubapp.Client the orchestrator needs to
// fetch a PR's diff and config, and post its findings.
type GithubClient interface {
	GetFileContent(ctx context.Context, installationID int64, owner, repo, ref, path string) ([]byte, bool, error)
	GetPRHeadSHA(ctx context.Context, installationID int64, owner, repo string, prNumber int) (string, error)
	ListPRFiles(ctx context.Context, installationID int64, owner, repo string, prNumber int) ([]githubapp.PRFile, error)
	ListReviewComments(ctx context.Context, installationID int64, owner, repo string, prNumber int) ([]githubapp.ReviewComment, error)
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
	// Enrich the context logger with this review's correlation fields. This
	// is the one place both call paths (direct PR event, /argus review)
	// converge with all three values guaranteed present, so every log line
	// from here down carries them without threading a logger through every
	// interface in the call chain.
	logger := logging.FromContext(ctx, o.logger).With("repo", owner+"/"+repo, "pr", prNumber, "head_sha", headSHA)
	ctx = logging.WithLogger(ctx, logger)

	cfg := o.loadConfig(ctx, installationID, owner, repo, headSHA)

	files, err := o.github.ListPRFiles(ctx, installationID, owner, repo, prNumber)
	if err != nil {
		return fmt.Errorf("list PR files: %w", err)
	}
	totalFiles := len(files)

	files = FilterIgnored(files, cfg.Ignore)
	sort.Slice(files, func(i, j int) bool { return files[i].Filename < files[j].Filename })
	if len(files) > cfg.MaxFiles {
		files = files[:cfg.MaxFiles]
	}
	reviewedFiles := len(files)

	units := BuildDiffUnits(files)

	findings := o.reviewUnits(ctx, units, cfg.Persona)
	findings = filterByCategory(findings, cfg.Categories)
	findings = filterBySeverityFloor(findings, cfg.MinSeverity)
	findings = o.dedupAgainstExisting(ctx, installationID, owner, repo, prNumber, findings)
	findings = capFindings(findings, cfg.MaxComments)

	if len(findings) > 0 {
		comments := make([]githubapp.InlineComment, 0, len(findings))
		for _, f := range findings {
			comments = append(comments, githubapp.InlineComment{Path: f.File, Line: f.Line, Body: withFindingMarker(formatFindingBody(f), f)})
		}
		if err := o.github.CreateReview(ctx, installationID, owner, repo, prNumber, headSHA, comments, "COMMENT", ""); err != nil {
			return fmt.Errorf("create review: %w", err)
		}
	}

	summary := buildSummary(findings, reviewedFiles, totalFiles)
	if err := o.github.UpsertSummaryComment(ctx, installationID, owner, repo, prNumber, summary); err != nil {
		return fmt.Errorf("upsert summary comment: %w", err)
	}
	return nil
}

// ReviewPRByNumber resolves the PR's current head SHA and runs the same
// review as ReviewPR. It exists for the /argus review command, where the
// issue_comment webhook payload doesn't include a head SHA the way a
// pull_request payload does.
func (o *Orchestrator) ReviewPRByNumber(ctx context.Context, installationID int64, owner, repo string, prNumber int) error {
	headSHA, err := o.github.GetPRHeadSHA(ctx, installationID, owner, repo, prNumber)
	if err != nil {
		return fmt.Errorf("get PR head SHA: %w", err)
	}
	return o.ReviewPR(ctx, installationID, owner, repo, prNumber, headSHA)
}

// loadConfig fetches and parses .argus.yml from ref, logging and falling
// back to repoconfig.Default if the file is absent, unreadable, or invalid.
func (o *Orchestrator) loadConfig(ctx context.Context, installationID int64, owner, repo, ref string) repoconfig.Config {
	raw, found, err := o.github.GetFileContent(ctx, installationID, owner, repo, ref, repoconfig.Path)
	if err != nil {
		logging.FromContext(ctx, o.logger).Warn("failed to fetch .argus.yml, using defaults", "err", err)
		return repoconfig.Default
	}
	if !found {
		return repoconfig.Default
	}

	cfg, err := repoconfig.Parse(raw)
	if err != nil {
		logging.FromContext(ctx, o.logger).Warn("invalid .argus.yml, using defaults", "err", err)
	}
	return cfg
}

// dedupAgainstExisting drops any finding that was already posted on a prior
// review of this PR, identified by the finding-ID marker embedded in each
// comment's body (see dedup.go). This is what keeps re-reviews stateless:
// prior state is derived from GitHub's own comment listing, not a database.
// A failure to list existing comments is logged and treated as "no existing
// comments" — dedup is a noise-reduction nicety, not worth failing the
// review over.
func (o *Orchestrator) dedupAgainstExisting(ctx context.Context, installationID int64, owner, repo string, prNumber int, findings []Finding) []Finding {
	existing, err := o.github.ListReviewComments(ctx, installationID, owner, repo, prNumber)
	if err != nil {
		logging.FromContext(ctx, o.logger).Warn("failed to list existing review comments, skipping dedup", "err", err)
		return findings
	}

	posted := make(map[string]bool, len(existing))
	for _, c := range existing {
		if id, ok := extractFindingID(c.Body); ok {
			posted[id] = true
		}
	}

	kept := make([]Finding, 0, len(findings))
	for _, f := range findings {
		if !posted[findingID(f)] {
			kept = append(kept, f)
		}
	}
	return kept
}

// reviewUnits fans units out to the provider with bounded concurrency,
// logging and skipping any unit whose review fails.
func (o *Orchestrator) reviewUnits(ctx context.Context, units []DiffUnit, persona string) []Finding {
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

			unitFindings, err := o.provider.Review(ctx, unit, Config{Persona: persona})
			if err != nil {
				logging.FromContext(ctx, o.logger).Error("skipping diff unit after provider error", "err", err, "file", unit.File)
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

func filterByCategory(findings []Finding, categories []string) []Finding {
	kept := make([]Finding, 0, len(findings))
	for _, f := range findings {
		if slices.Contains(categories, f.Category) {
			kept = append(kept, f)
		}
	}
	return kept
}

func filterBySeverityFloor(findings []Finding, minSeverity string) []Finding {
	floor := severityRank[minSeverity]
	kept := make([]Finding, 0, len(findings))
	for _, f := range findings {
		if severityRank[f.Severity] >= floor {
			kept = append(kept, f)
		}
	}
	return kept
}

// capFindings deterministically orders findings (highest severity first,
// then file, then line) and truncates to maxComments.
func capFindings(findings []Finding, maxComments int) []Finding {
	sort.SliceStable(findings, func(i, j int) bool {
		if severityRank[findings[i].Severity] != severityRank[findings[j].Severity] {
			return severityRank[findings[i].Severity] > severityRank[findings[j].Severity]
		}
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})
	if len(findings) > maxComments {
		findings = findings[:maxComments]
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

func buildSummary(findings []Finding, filesReviewed, totalFiles int) string {
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
	fmt.Fprintf(&b, "Reviewed %d/%d changed files.", filesReviewed, totalFiles)
	return b.String()
}
