package review

import "context"

// Record captures one completed review for persistence, independent
// of the Findings slice saved alongside it.
type Record struct {
	Repo          string
	PRNumber      int
	HeadSHA       string
	FindingsCount int
	LatencyMS     int64
}

// HistoryStore persists a completed review and the findings posted for it,
// per TECH_DESIGN.md §4. It is optional: Argus runs with no history at all
// (see noopHistoryStore) when no database is configured, and a failure to
// save is logged rather than treated as a review failure — see
// Orchestrator.ReviewPR.
type HistoryStore interface {
	SaveReview(ctx context.Context, rec Record, findings []Finding) error
}

// noopHistoryStore is the default HistoryStore used when NewOrchestrator is
// given nil, so callers that don't configure persistence don't need a fake.
type noopHistoryStore struct{}

func (noopHistoryStore) SaveReview(context.Context, Record, []Finding) error { return nil }
