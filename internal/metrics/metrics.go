// Package metrics implements Argus's Prometheus instrumentation. Recorder
// satisfies review.Metrics (defined at that consumer, per this repo's
// convention of putting interfaces next to the code that needs them) without
// this package importing internal/review at all.
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Recorder records the review-path metrics described in TECH_DESIGN.md §7,
// registered against a caller-supplied prometheus.Registerer rather than the
// global default — so each test (and, if ever needed, each server instance)
// gets its own isolated set of series instead of relying on package-level
// state.
type Recorder struct {
	reviewsTotal   prometheus.Counter
	findingsTotal  *prometheus.CounterVec
	llmErrorsTotal prometheus.Counter
	reviewDuration prometheus.Histogram
}

// NewRecorder builds a Recorder whose metrics are registered against reg.
func NewRecorder(reg prometheus.Registerer) *Recorder {
	factory := promauto.With(reg)
	return &Recorder{
		reviewsTotal: factory.NewCounter(prometheus.CounterOpts{
			Name: "argus_reviews_total",
			Help: "Total number of pull request reviews processed.",
		}),
		findingsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "argus_findings_total",
			Help: "Total number of findings posted, by category.",
		}, []string{"category"}),
		llmErrorsTotal: factory.NewCounter(prometheus.CounterOpts{
			Name: "argus_llm_errors_total",
			Help: "Total number of LLM provider errors encountered while reviewing diff units.",
		}),
		reviewDuration: factory.NewHistogram(prometheus.HistogramOpts{
			Name:    "argus_review_duration_seconds",
			Help:    "Duration of a full pull request review, in seconds.",
			Buckets: []float64{0.5, 1, 2, 5, 10, 20, 30, 60, 120, 300},
		}),
	}
}

// ReviewCompleted records that a review finished (successfully or not) and
// how long it took.
func (r *Recorder) ReviewCompleted(duration time.Duration) {
	r.reviewsTotal.Inc()
	r.reviewDuration.Observe(duration.Seconds())
}

// FindingPosted records one finding actually posted to a pull request, by
// its category.
func (r *Recorder) FindingPosted(category string) {
	r.findingsTotal.WithLabelValues(category).Inc()
}

// LLMError records a provider error encountered while reviewing a diff unit.
func (r *Recorder) LLMError() {
	r.llmErrorsTotal.Inc()
}
