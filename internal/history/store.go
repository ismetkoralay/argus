// Package history implements review.HistoryStore against Postgres,
// persisting each completed review and its findings per TECH_DESIGN.md §4.
// It is only ever constructed when an operator has explicitly configured
// DATABASE_URL — see internal/config and cmd/service/main.go; every other
// code path runs against internal/review's own no-op HistoryStore.
package history

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver

	"github.com/ismetkoralay/argus/internal/review"
)

// Store persists review history to Postgres. It implements
// review.HistoryStore.
type Store struct {
	db *sql.DB
}

var _ review.HistoryStore = (*Store)(nil)

// New opens a connection pool to databaseURL, verifies connectivity, and
// applies any pending migrations. Both are treated as fatal: New is only
// called when an operator has explicitly opted into persistence via
// DATABASE_URL, so a broken connection or a bad migration is a real
// configuration error the caller should fail fast on at startup, not
// silently degrade from mid-review.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return &Store{db: db}, nil
}

// Close releases the underlying connection pool.
func (s *Store) Close() error {
	return s.db.Close()
}

// SaveReview implements review.HistoryStore: it inserts rec and findings in
// a single transaction so a review is never recorded without the findings
// posted alongside it (or vice versa).
func (s *Store) SaveReview(ctx context.Context, rec review.Record, findings []review.Finding) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var reviewID int64
	err = tx.QueryRowContext(ctx,
		`INSERT INTO reviews (repo, pr_number, head_sha, findings_count, latency_ms)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		rec.Repo, rec.PRNumber, rec.HeadSHA, rec.FindingsCount, rec.LatencyMS,
	).Scan(&reviewID)
	if err != nil {
		return fmt.Errorf("insert review: %w", err)
	}

	for _, f := range findings {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO findings (review_id, file, line, severity, category, message)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			reviewID, f.File, f.Line, f.Severity, f.Category, f.Message,
		); err != nil {
			return fmt.Errorf("insert finding: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
