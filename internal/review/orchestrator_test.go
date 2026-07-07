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
	"github.com/ismetkoralay/argus/internal/repoconfig"
)

type fakeGithubClient struct {
	files            []githubapp.PRFile
	configContent    []byte
	configFound      bool
	configErr        error
	existingComments []githubapp.ReviewComment
	listCommentsErr  error

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

func (f *fakeGithubClient) GetFileContent(_ context.Context, _ int64, _, _, _, _ string) ([]byte, bool, error) {
	return f.configContent, f.configFound, f.configErr
}

func (f *fakeGithubClient) ListPRFiles(_ context.Context, _ int64, _, _ string, _ int) ([]githubapp.PRFile, error) {
	return f.files, nil
}

func (f *fakeGithubClient) ListReviewComments(_ context.Context, _ int64, _, _ string, _ int) ([]githubapp.ReviewComment, error) {
	return f.existingComments, f.listCommentsErr
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
	if severityRank[repoconfig.Default.MinSeverity] > severityRank["info"] {
		t.Fatalf("default min_severity is %q, but this test assumes info is at or above the floor", repoconfig.Default.MinSeverity)
	}

	o := NewOrchestrator(provider, gh, nil)
	if err := o.ReviewPR(context.Background(), 42, "octo-org", "octo-repo", 7, "deadbeef"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gh.mu.Lock()
	defer gh.mu.Unlock()
	if len(gh.reviewComments) != 2 {
		t.Fatalf("got %d comments, want 2 (both at or above the %q floor): %+v", len(gh.reviewComments), repoconfig.Default.MinSeverity, gh.reviewComments)
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
	if len(gh.reviewComments) != repoconfig.Default.MaxComments {
		t.Fatalf("got %d comments, want capped at %d", len(gh.reviewComments), repoconfig.Default.MaxComments)
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

func TestOrchestrator_ReviewPR_ConfigMinSeverityRaisesFloor(t *testing.T) {
	gh := &fakeGithubClient{
		files:         []githubapp.PRFile{patchFile("a.go", 1)},
		configFound:   true,
		configContent: []byte("min_severity: error\n"),
	}
	provider := &FakeProvider{Findings: []Finding{
		{File: "a.go", Line: 1, Severity: "warning", Category: "style", Message: "below new floor"},
		{File: "a.go", Line: 2, Severity: "error", Category: "bug", Message: "above new floor"},
	}}

	o := NewOrchestrator(provider, gh, nil)
	if err := o.ReviewPR(context.Background(), 42, "octo-org", "octo-repo", 7, "deadbeef"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gh.mu.Lock()
	defer gh.mu.Unlock()
	if len(gh.reviewComments) != 1 || !strings.Contains(gh.reviewComments[0].Body, "above new floor") {
		t.Fatalf("got %+v, want only the error-severity finding", gh.reviewComments)
	}
}

func TestOrchestrator_ReviewPR_ConfigCategoriesFiltersFindings(t *testing.T) {
	gh := &fakeGithubClient{
		files:         []githubapp.PRFile{patchFile("a.go", 1)},
		configFound:   true,
		configContent: []byte("categories: [bug]\n"),
	}
	provider := &FakeProvider{Findings: []Finding{
		{File: "a.go", Line: 1, Severity: "error", Category: "style", Message: "style finding"},
		{File: "a.go", Line: 2, Severity: "error", Category: "bug", Message: "bug finding"},
	}}

	o := NewOrchestrator(provider, gh, nil)
	if err := o.ReviewPR(context.Background(), 42, "octo-org", "octo-repo", 7, "deadbeef"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gh.mu.Lock()
	defer gh.mu.Unlock()
	if len(gh.reviewComments) != 1 || !strings.Contains(gh.reviewComments[0].Body, "bug finding") {
		t.Fatalf("got %+v, want only the bug-category finding", gh.reviewComments)
	}
}

func TestOrchestrator_ReviewPR_ConfigIgnoreDropsFiles(t *testing.T) {
	gh := &fakeGithubClient{
		files:         []githubapp.PRFile{patchFile("a.go", 1), patchFile("vendor/dep.go", 2)},
		configFound:   true,
		configContent: []byte("ignore:\n  - \"vendor/**\"\n"),
	}
	provider := &FakeProvider{FindingsFunc: func(unit DiffUnit) ([]Finding, error) {
		return []Finding{{File: unit.File, Line: 1, Severity: "error", Category: "bug", Message: "bug in " + unit.File}}, nil
	}}

	o := NewOrchestrator(provider, gh, nil)
	if err := o.ReviewPR(context.Background(), 42, "octo-org", "octo-repo", 7, "deadbeef"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(provider.Calls) != 1 || provider.Calls[0].File != "a.go" {
		t.Fatalf("got provider calls %+v, want only a.go (vendor/** ignored)", provider.Calls)
	}
	gh.mu.Lock()
	defer gh.mu.Unlock()
	if len(gh.reviewComments) != 1 || !strings.Contains(gh.reviewComments[0].Body, "a.go") {
		t.Fatalf("got %+v, want only the a.go finding", gh.reviewComments)
	}
}

func TestOrchestrator_ReviewPR_ConfigMaxFilesCapsFilesReviewed(t *testing.T) {
	files := []githubapp.PRFile{patchFile("a.go", 1), patchFile("b.go", 2), patchFile("c.go", 3)}
	gh := &fakeGithubClient{
		files:         files,
		configFound:   true,
		configContent: []byte("max_files: 2\n"),
	}
	provider := &FakeProvider{FindingsFunc: func(unit DiffUnit) ([]Finding, error) {
		return []Finding{{File: unit.File, Line: 1, Severity: "error", Category: "bug", Message: "bug"}}, nil
	}}

	o := NewOrchestrator(provider, gh, nil)
	if err := o.ReviewPR(context.Background(), 42, "octo-org", "octo-repo", 7, "deadbeef"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(provider.Calls) != 2 {
		t.Fatalf("got %d provider calls, want 2 (max_files cap)", len(provider.Calls))
	}
	gh.mu.Lock()
	defer gh.mu.Unlock()
	if !strings.Contains(gh.summaryBody, "Reviewed 2/3 changed files") {
		t.Fatalf("got summary %q, want it to mention 2/3 changed files reviewed", gh.summaryBody)
	}
}

func TestOrchestrator_ReviewPR_ConfigMaxCommentsOverridesCap(t *testing.T) {
	files := make([]githubapp.PRFile, 0, 5)
	for i := range 5 {
		files = append(files, patchFile(fmt.Sprintf("f%d.go", i), i))
	}
	gh := &fakeGithubClient{
		files:         files,
		configFound:   true,
		configContent: []byte("max_comments: 2\n"),
	}
	provider := &FakeProvider{FindingsFunc: func(unit DiffUnit) ([]Finding, error) {
		return []Finding{{File: unit.File, Line: 1, Severity: "warning", Category: "style", Message: "finding"}}, nil
	}}

	o := NewOrchestrator(provider, gh, nil)
	if err := o.ReviewPR(context.Background(), 42, "octo-org", "octo-repo", 7, "deadbeef"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gh.mu.Lock()
	defer gh.mu.Unlock()
	if len(gh.reviewComments) != 2 {
		t.Fatalf("got %d comments, want capped at configured max_comments=2", len(gh.reviewComments))
	}
}

func TestOrchestrator_ReviewPR_ConfigPersonaThreadedToProvider(t *testing.T) {
	gh := &fakeGithubClient{
		files:         []githubapp.PRFile{patchFile("a.go", 1)},
		configFound:   true,
		configContent: []byte("persona: \"concise senior engineer\"\n"),
	}
	provider := &FakeProvider{}

	o := NewOrchestrator(provider, gh, nil)
	if err := o.ReviewPR(context.Background(), 42, "octo-org", "octo-repo", 7, "deadbeef"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(provider.Configs) != 1 || provider.Configs[0].Persona != "concise senior engineer" {
		t.Fatalf("got provider configs %+v, want persona threaded through", provider.Configs)
	}
}

func TestOrchestrator_ReviewPR_MalformedConfigFallsBackToDefaults(t *testing.T) {
	gh := &fakeGithubClient{
		files:         []githubapp.PRFile{patchFile("a.go", 1)},
		configFound:   true,
		configContent: []byte("min_severity: not-a-real-severity\n"),
	}
	provider := &FakeProvider{Findings: []Finding{
		{File: "a.go", Line: 1, Severity: "info", Category: "style", Message: "info finding"},
	}}

	o := NewOrchestrator(provider, gh, nil)
	if err := o.ReviewPR(context.Background(), 42, "octo-org", "octo-repo", 7, "deadbeef"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gh.mu.Lock()
	defer gh.mu.Unlock()
	if len(gh.reviewComments) != 1 {
		t.Fatalf("got %d comments, want 1 (malformed config should fall back to default info floor)", len(gh.reviewComments))
	}
}

func TestOrchestrator_ReviewPR_Dedup_FirstReviewPostsEverything(t *testing.T) {
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
	if len(gh.reviewComments) != 2 {
		t.Fatalf("got %d comments on a first review with no existing comments, want 2", len(gh.reviewComments))
	}
}

func TestOrchestrator_ReviewPR_Dedup_IdenticalReReviewPostsNothing(t *testing.T) {
	findingA := Finding{File: "a.go", Line: 1, Severity: "error", Category: "bug", Message: "bug in a.go"}
	findingB := Finding{File: "b.go", Line: 1, Severity: "error", Category: "bug", Message: "bug in b.go"}
	gh := &fakeGithubClient{
		files: []githubapp.PRFile{patchFile("a.go", 1), patchFile("b.go", 2)},
		existingComments: []githubapp.ReviewComment{
			{Path: "a.go", Line: 1, Body: withFindingMarker("previously posted", findingA)},
			{Path: "b.go", Line: 1, Body: withFindingMarker("previously posted", findingB)},
		},
	}
	provider := &FakeProvider{FindingsFunc: func(unit DiffUnit) ([]Finding, error) {
		if unit.File == "a.go" {
			return []Finding{findingA}, nil
		}
		return []Finding{findingB}, nil
	}}

	o := NewOrchestrator(provider, gh, nil)
	if err := o.ReviewPR(context.Background(), 42, "octo-org", "octo-repo", 7, "deadbeef"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gh.mu.Lock()
	defer gh.mu.Unlock()
	if gh.reviewCalled {
		t.Fatalf("expected CreateReview not to be called when every finding was already posted, got comments %+v", gh.reviewComments)
	}
}

func TestOrchestrator_ReviewPR_Dedup_OnlyNewFindingIsPosted(t *testing.T) {
	findingA := Finding{File: "a.go", Line: 1, Severity: "error", Category: "bug", Message: "bug in a.go"}
	newFindingB := Finding{File: "b.go", Line: 5, Severity: "error", Category: "bug", Message: "a new bug in b.go"}
	gh := &fakeGithubClient{
		files: []githubapp.PRFile{patchFile("a.go", 1), patchFile("b.go", 2)},
		existingComments: []githubapp.ReviewComment{
			{Path: "a.go", Line: 1, Body: withFindingMarker("previously posted", findingA)},
		},
	}
	provider := &FakeProvider{FindingsFunc: func(unit DiffUnit) ([]Finding, error) {
		if unit.File == "a.go" {
			return []Finding{findingA}, nil
		}
		return []Finding{newFindingB}, nil
	}}

	o := NewOrchestrator(provider, gh, nil)
	if err := o.ReviewPR(context.Background(), 42, "octo-org", "octo-repo", 7, "deadbeef"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gh.mu.Lock()
	defer gh.mu.Unlock()
	if len(gh.reviewComments) != 1 || !strings.Contains(gh.reviewComments[0].Body, "a new bug in b.go") {
		t.Fatalf("got %+v, want only the new b.go finding", gh.reviewComments)
	}
}
