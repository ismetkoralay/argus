package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

func TestRecorder_ReviewCompleted(t *testing.T) {
	reg := prometheus.NewRegistry()
	r := NewRecorder(reg)

	r.ReviewCompleted(2 * time.Second)
	r.ReviewCompleted(3 * time.Second)

	if got := testutil.ToFloat64(r.reviewsTotal); got != 2 {
		t.Fatalf("argus_reviews_total = %v, want 2", got)
	}

	// Histogram.Collect() reports one *aggregated* metric (cumulative
	// buckets + sample count), not one metric per Observe() call, so we
	// read the sample count directly rather than via CollectAndCount.
	var m dto.Metric
	if err := r.reviewDuration.Write(&m); err != nil {
		t.Fatalf("write histogram: %v", err)
	}
	if got := m.GetHistogram().GetSampleCount(); got != 2 {
		t.Fatalf("argus_review_duration_seconds sample count = %d, want 2", got)
	}
}

func TestRecorder_FindingPosted(t *testing.T) {
	reg := prometheus.NewRegistry()
	r := NewRecorder(reg)

	r.FindingPosted("bug")
	r.FindingPosted("bug")
	r.FindingPosted("style")

	if got := testutil.ToFloat64(r.findingsTotal.WithLabelValues("bug")); got != 2 {
		t.Fatalf(`argus_findings_total{category="bug"} = %v, want 2`, got)
	}
	if got := testutil.ToFloat64(r.findingsTotal.WithLabelValues("style")); got != 1 {
		t.Fatalf(`argus_findings_total{category="style"} = %v, want 1`, got)
	}
	if got := testutil.ToFloat64(r.findingsTotal.WithLabelValues("security")); got != 0 {
		t.Fatalf(`argus_findings_total{category="security"} = %v, want 0 (never posted)`, got)
	}
}

func TestRecorder_LLMError(t *testing.T) {
	reg := prometheus.NewRegistry()
	r := NewRecorder(reg)

	r.LLMError()
	r.LLMError()
	r.LLMError()

	if got := testutil.ToFloat64(r.llmErrorsTotal); got != 3 {
		t.Fatalf("argus_llm_errors_total = %v, want 3", got)
	}
}

func TestNewRecorder_RegistersAgainstGivenRegistererOnly(t *testing.T) {
	reg := prometheus.NewRegistry()
	r := NewRecorder(reg)
	// Touch every series once — a CounterVec with no observed label
	// combinations yet contributes nothing to Gather(), so findings_total
	// wouldn't otherwise appear below.
	r.ReviewCompleted(time.Second)
	r.FindingPosted("bug")
	r.LLMError()

	metricFamilies, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	var names []string
	for _, mf := range metricFamilies {
		names = append(names, mf.GetName())
	}
	want := []string{"argus_findings_total", "argus_llm_errors_total", "argus_review_duration_seconds", "argus_reviews_total"}
	if len(names) != len(want) {
		t.Fatalf("got metric families %v, want %v", names, want)
	}
}
