package review

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ismetkoralay/argus/internal/githubapp"
)

type fakeGithubClient struct {
	files []githubapp.PRFile

	mu               sync.Mutex
	reviewCalled     bool
	reviewComments   []githubapp.InlineComment
	reviewCommitSHA  string
	reviewEvent      string
	summaryCalled    bool
	summaryBody      string
	createReviewErr  error
	upsertSummaryErr error
}

func (f *fakeGithubClient) ListPRFiles(_ context.Context, _ int64, _, _ string, _ int) ([]githubapp.PRFile, error) {
	return f.files, nil
}

func (f *fakeGithubClient) CreateReview(_ context.Context, _ int64, _, _ string, _ int, commitSHA string, comments []githubapp.InlineComment, event, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reviewCalled = true
	f.reviewComments = comments
	f.reviewCommitSHA = commitSHA
	f.reviewEvent = event
	return f.createReviewErr
}

func (f *fakeGithubClient) UpsertSummaryComment(_ context.Context, _ int64, _, _ string, _ int, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.summaryCalled = true
	f.summaryBody = body
	return f.upsertSummaryErr
}

func patchFile(name string, n int) githubapp.PRFile {
	return githubapp.PRFile{Filename: name, Patch: fmt.Sprintf("@@ -1,1 +1,%d @@\n+line%d", n, n), Status: "modified"}
}

func TestOrchestrator_ReviewPR_Aggregation(t *testing.T) {
	gh := &fakeGithubClient{files: []githubapp.PRFile{patchFile("a.go", 1), patchFile("b.go", 2)}}
	provider := &FakeProvider{FindingsFunc: func(unit DiffUnit) ([]Finding, error) {
		return []Finding{{File: unit.File, Line: 1, Severity: "error", Category: "bug", Message: "bug in " + unit.File}}, nil
	}}

	o := NewOrchestrator(provider, gh, nil)
	if err := o.ReviewPR(context.Background(), 42, "octo-org", "octo-repo", 7, "deadbeef"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gh.mu.Lock()
	defer gh.mu.Unlock()
	if !gh.reviewCalled || len(gh.reviewComments) != 2 {
		t.Fatalf("got %d review comments (called=%v), want 2", len(gh.reviewComments), gh.reviewCalled)
	}
	if gh.reviewCommitSHA != "deadbeef" || gh.reviewEvent != "COMMENT" {
		t.Fatalf("got commitSHA=%q event=%q, want deadbeef/COMMENT", gh.reviewCommitSHA, gh.reviewEvent)
	}
	if !gh.summaryCalled || !strings.Contains(gh.summaryBody, "2") {
		t.Fatalf("got summary called=%v body=%q, want it called and mentioning 2 findings", gh.summaryCalled, gh.summaryBody)
	}
}

func TestOrchestrator_ReviewPR_SeverityFloorKeepsEverythingAtOrAboveFloor(t *testing.T) {
	gh := &fakeGithubClient{files: []githubapp.PRFile{patchFile("a.go", 1)}}
	provider := &FakeProvider{Findings: []Finding{
		{File: "a.go", Line: 1, Severity: "info", Category: "style", Message: "at floor"},
		{File: "a.go", Line: 2, Severity: "warning", Category: "style", Message: "above floor"},
	}}
	if severityRank[severityFloor] > severityRank["info"] {
		t.Fatalf("severityFloor is %q, but this test assumes info is at or above the floor", severityFloor)
	}

	o := NewOrchestrator(provider, gh, nil)
	if err := o.ReviewPR(context.Background(), 42, "octo-org", "octo-repo", 7, "deadbeef"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gh.mu.Lock()
	defer gh.mu.Unlock()
	if len(gh.reviewComments) != 2 {
		t.Fatalf("got %d comments, want 2 (both at or above the %q floor): %+v", len(gh.reviewComments), severityFloor, gh.reviewComments)
	}
}

func TestOrchestrator_ReviewPR_CapsCommentCount(t *testing.T) {
	files := make([]githubapp.PRFile, 0, 20)
	for i := range 20 {
		files = append(files, patchFile(fmt.Sprintf("f%d.go", i), i))
	}
	gh := &fakeGithubClient{files: files}
	provider := &FakeProvider{FindingsFunc: func(unit DiffUnit) ([]Finding, error) {
		return []Finding{{File: unit.File, Line: 1, Severity: "warning", Category: "style", Message: "finding"}}, nil
	}}

	o := NewOrchestrator(provider, gh, nil)
	if err := o.ReviewPR(context.Background(), 42, "octo-org", "octo-repo", 7, "deadbeef"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gh.mu.Lock()
	defer gh.mu.Unlock()
	if len(gh.reviewComments) != commentCap {
		t.Fatalf("got %d comments, want capped at %d", len(gh.reviewComments), commentCap)
	}
}

func TestOrchestrator_ReviewPR_BoundsConcurrency(t *testing.T) {
	files := make([]githubapp.PRFile, 0, 10)
	for i := range 10 {
		files = append(files, patchFile(fmt.Sprintf("f%d.go", i), i))
	}
	gh := &fakeGithubClient{files: files}

	var inFlight int32
	var maxInFlight int32
	provider := &FakeProvider{FindingsFunc: func(_ DiffUnit) ([]Finding, error) {
		cur := atomic.AddInt32(&inFlight, 1)
		for {
			observedMax := atomic.LoadInt32(&maxInFlight)
			if cur <= observedMax || atomic.CompareAndSwapInt32(&maxInFlight, observedMax, cur) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		return nil, nil
	}}

	o := NewOrchestrator(provider, gh, nil)
	if err := o.ReviewPR(context.Background(), 42, "octo-org", "octo-repo", 7, "deadbeef"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if maxInFlight > concurrency {
		t.Fatalf("got max in-flight %d, want <= %d", maxInFlight, concurrency)
	}
	if maxInFlight < 2 {
		t.Fatalf("got max in-flight %d, want fan-out to actually overlap (>= 2)", maxInFlight)
	}
}

func TestOrchestrator_ReviewPR_SkipsUnitOnProviderError(t *testing.T) {
	gh := &fakeGithubClient{files: []githubapp.PRFile{patchFile("a.go", 1), patchFile("b.go", 2)}}
	provider := &FakeProvider{FindingsFunc: func(unit DiffUnit) ([]Finding, error) {
		if unit.File == "a.go" {
			return nil, errors.New("boom")
		}
		return []Finding{{File: unit.File, Line: 1, Severity: "error", Category: "bug", Message: "bug in " + unit.File}}, nil
	}}

	o := NewOrchestrator(provider, gh, nil)
	if err := o.ReviewPR(context.Background(), 42, "octo-org", "octo-repo", 7, "deadbeef"); err != nil {
		t.Fatalf("unexpected error (unit errors should be logged and skipped, not fatal): %v", err)
	}

	gh.mu.Lock()
	defer gh.mu.Unlock()
	if len(gh.reviewComments) != 1 || !strings.Contains(gh.reviewComments[0].Body, "b.go") {
		t.Fatalf("got %+v, want only the b.go finding", gh.reviewComments)
	}
}

func TestOrchestrator_ReviewPR_NoFindingsSkipsReviewButPostsSummary(t *testing.T) {
	gh := &fakeGithubClient{files: []githubapp.PRFile{patchFile("a.go", 1)}}
	provider := &FakeProvider{Findings: nil}

	o := NewOrchestrator(provider, gh, nil)
	if err := o.ReviewPR(context.Background(), 42, "octo-org", "octo-repo", 7, "deadbeef"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gh.mu.Lock()
	defer gh.mu.Unlock()
	if gh.reviewCalled {
		t.Fatal("expected CreateReview not to be called with zero findings")
	}
	if !gh.summaryCalled || !strings.Contains(gh.summaryBody, "looks good") {
		t.Fatalf("got summary called=%v body=%q, want it called with a 'looks good' verdict", gh.summaryCalled, gh.summaryBody)
	}
}
