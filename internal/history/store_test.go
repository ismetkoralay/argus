package history

import (
	"context"
	"os"
	"testing"

	"github.com/ismetkoralay/argus/internal/review"
)

// newTestStore skips the calling test unless TEST_DATABASE_URL is set,
// which keeps `go test ./...` (and `make test`) green with no Postgres
// running — the same property the no-DB path relies on in production. CI's
// test job sets TEST_DATABASE_URL against a real Postgres service container
// so these tests actually run there.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping internal/history integration tests")
	}

	ctx := context.Background()
	store, err := New(ctx, dsn)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if _, err := store.db.ExecContext(ctx, "TRUNCATE reviews, findings RESTART IDENTITY CASCADE"); err != nil {
		t.Fatalf("truncate tables: %v", err)
	}
	return store
}

func TestNew_MigratesAndIsIdempotent(t *testing.T) {
	store := newTestStore(t)

	// newTestStore already ran New once; running it again against the same
	// database must be a no-op, not an error, since main.go calls New on
	// every process startup.
	second, err := New(context.Background(), os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("second New call: %v", err)
	}
	defer func() { _ = second.Close() }()
	_ = store
}

func TestStore_SaveReview_PersistsReviewAndFindings(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	rec := review.Record{
		Repo: "octo-org/octo-repo", PRNumber: 7, HeadSHA: "deadbeef",
		FindingsCount: 2, LatencyMS: 1234,
	}
	findings := []review.Finding{
		{File: "a.go", Line: 1, Severity: "error", Category: "bug", Message: "bug in a.go"},
		{File: "b.go", Line: 5, Severity: "warning", Category: "style", Message: "style nit"},
	}

	if err := store.SaveReview(ctx, rec, findings); err != nil {
		t.Fatalf("SaveReview: %v", err)
	}

	var reviewID int64
	var gotFindingsCount int
	var gotLatencyMS int64
	row := store.db.QueryRowContext(ctx, `SELECT id, findings_count, latency_ms FROM reviews WHERE repo = $1 AND pr_number = $2`, rec.Repo, rec.PRNumber)
	if err := row.Scan(&reviewID, &gotFindingsCount, &gotLatencyMS); err != nil {
		t.Fatalf("query review: %v", err)
	}
	if gotFindingsCount != 2 || gotLatencyMS != 1234 {
		t.Fatalf("got findings_count=%d latency_ms=%d, want 2/1234", gotFindingsCount, gotLatencyMS)
	}

	var findingCount int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM findings WHERE review_id = $1`, reviewID).Scan(&findingCount); err != nil {
		t.Fatalf("query findings: %v", err)
	}
	if findingCount != 2 {
		t.Fatalf("got %d findings rows, want 2", findingCount)
	}
}

func TestStore_SaveReview_ZeroFindingsPersistsReviewOnly(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	rec := review.Record{Repo: "octo-org/octo-repo", PRNumber: 9, HeadSHA: "cafebabe", FindingsCount: 0}
	if err := store.SaveReview(ctx, rec, nil); err != nil {
		t.Fatalf("SaveReview: %v", err)
	}

	var reviewID int64
	if err := store.db.QueryRowContext(ctx, `SELECT id FROM reviews WHERE repo = $1 AND pr_number = $2`, rec.Repo, rec.PRNumber).Scan(&reviewID); err != nil {
		t.Fatalf("query review: %v", err)
	}

	var findingCount int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM findings WHERE review_id = $1`, reviewID).Scan(&findingCount); err != nil {
		t.Fatalf("query findings: %v", err)
	}
	if findingCount != 0 {
		t.Fatalf("got %d findings rows, want 0", findingCount)
	}
}
