package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v88/github"
)

const testSecret = "test-secret"

const openedPRPayload = `{
  "action": "opened",
  "number": 7,
  "pull_request": {"number": 7, "head": {"sha": "deadbeef"}},
  "repository": {"name": "octo-repo", "owner": {"login": "octo-org"}},
  "installation": {"id": 42}
}`

const synchronizePRPayload = `{
  "action": "synchronize",
  "number": 7,
  "pull_request": {"number": 7, "head": {"sha": "cafef00d"}},
  "repository": {"name": "octo-repo", "owner": {"login": "octo-org"}},
  "installation": {"id": 42}
}`

const closedPRPayload = `{
  "action": "closed",
  "number": 7,
  "pull_request": {"number": 7, "head": {"sha": "deadbeef"}},
  "repository": {"name": "octo-repo", "owner": {"login": "octo-org"}},
  "installation": {"id": 42}
}`

const argusReviewCommandOnPRPayload = `{
  "action": "created",
  "issue": {"number": 7, "pull_request": {"url": "https://api.example.com/repos/octo-org/octo-repo/pulls/7"}},
  "comment": {"body": "/argus review", "user": {"login": "octocat", "type": "User"}},
  "repository": {"name": "octo-repo", "owner": {"login": "octo-org"}},
  "installation": {"id": 42}
}`

const argusReviewCommandOnPlainIssuePayload = `{
  "action": "created",
  "issue": {"number": 9},
  "comment": {"body": "/argus review", "user": {"login": "octocat", "type": "User"}},
  "repository": {"name": "octo-repo", "owner": {"login": "octo-org"}},
  "installation": {"id": 42}
}`

const argusReviewCommandFromBotPayload = `{
  "action": "created",
  "issue": {"number": 7, "pull_request": {"url": "https://api.example.com/repos/octo-org/octo-repo/pulls/7"}},
  "comment": {"body": "/argus review", "user": {"login": "argus[bot]", "type": "Bot"}},
  "repository": {"name": "octo-repo", "owner": {"login": "octo-org"}},
  "installation": {"id": 42}
}`

const unrelatedCommentOnPRPayload = `{
  "action": "created",
  "issue": {"number": 7, "pull_request": {"url": "https://api.example.com/repos/octo-org/octo-repo/pulls/7"}},
  "comment": {"body": "nice work on this PR", "user": {"login": "octocat", "type": "User"}},
  "repository": {"name": "octo-repo", "owner": {"login": "octo-org"}},
  "installation": {"id": 42}
}`

const argusReviewCommandEditedPayload = `{
  "action": "edited",
  "issue": {"number": 7, "pull_request": {"url": "https://api.example.com/repos/octo-org/octo-repo/pulls/7"}},
  "comment": {"body": "/argus review", "user": {"login": "octocat", "type": "User"}},
  "repository": {"name": "octo-repo", "owner": {"login": "octo-org"}},
  "installation": {"id": 42}
}`

type fakeReviewer struct {
	called          chan struct{}
	err             error
	gotInstallation int64
	gotOwner        string
	gotRepo         string
	gotPRNumber     int
	gotHeadSHA      string
	gotByNumber     bool
}

func newFakeReviewer() *fakeReviewer {
	return &fakeReviewer{called: make(chan struct{}, 1)}
}

func (f *fakeReviewer) ReviewPR(_ context.Context, installationID int64, owner, repo string, prNumber int, headSHA string) error {
	f.gotInstallation = installationID
	f.gotOwner = owner
	f.gotRepo = repo
	f.gotPRNumber = prNumber
	f.gotHeadSHA = headSHA
	f.called <- struct{}{}
	return f.err
}

func (f *fakeReviewer) ReviewPRByNumber(_ context.Context, installationID int64, owner, repo string, prNumber int) error {
	f.gotInstallation = installationID
	f.gotOwner = owner
	f.gotRepo = repo
	f.gotPRNumber = prNumber
	f.gotByNumber = true
	f.called <- struct{}{}
	return f.err
}

func sign(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func doRequest(t *testing.T, secret, body, signature, eventType string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if signature != "" {
		req.Header.Set(github.SHA256SignatureHeader, signature)
	}
	if eventType != "" {
		req.Header.Set(github.EventTypeHeader, eventType)
	}

	reviewer := newFakeReviewer()
	h := NewHandler([]byte(secret), reviewer, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandler_SignatureVerification(t *testing.T) {
	tests := []struct {
		name       string
		signature  string
		wantStatus int
	}{
		{
			name:       "valid signature",
			signature:  sign(testSecret, openedPRPayload),
			wantStatus: http.StatusOK,
		},
		{
			name:       "invalid signature",
			signature:  sign("wrong-secret", openedPRPayload),
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "missing signature",
			signature:  "",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doRequest(t, testSecret, openedPRPayload, tt.signature, "pull_request")
			if rec.Code != tt.wantStatus {
				t.Fatalf("got status %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestHandler_EventRouting(t *testing.T) {
	tests := []struct {
		name       string
		eventType  string
		payload    string
		wantCalled bool
		wantSHA    string
	}{
		{
			name:       "pull_request opened triggers a review",
			eventType:  "pull_request",
			payload:    openedPRPayload,
			wantCalled: true,
			wantSHA:    "deadbeef",
		},
		{
			name:       "pull_request synchronize triggers a review",
			eventType:  "pull_request",
			payload:    synchronizePRPayload,
			wantCalled: true,
			wantSHA:    "cafef00d",
		},
		{
			name:       "pull_request closed is a no-op",
			eventType:  "pull_request",
			payload:    closedPRPayload,
			wantCalled: false,
		},
		{
			name:       "unrelated event type is acked and ignored",
			eventType:  "ping",
			payload:    `{"zen": "hello"}`,
			wantCalled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(tt.payload))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(github.SHA256SignatureHeader, sign(testSecret, tt.payload))
			req.Header.Set(github.EventTypeHeader, tt.eventType)

			reviewer := newFakeReviewer()
			h := NewHandler([]byte(testSecret), reviewer, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("got status %d, want 200", rec.Code)
			}

			select {
			case <-reviewer.called:
				if !tt.wantCalled {
					t.Fatal("reviewer was called but should not have been")
				}
				if reviewer.gotInstallation != 42 || reviewer.gotOwner != "octo-org" ||
					reviewer.gotRepo != "octo-repo" || reviewer.gotPRNumber != 7 || reviewer.gotHeadSHA != tt.wantSHA {
					t.Fatalf("reviewer got unexpected args: %+v", reviewer)
				}
			case <-time.After(200 * time.Millisecond):
				if tt.wantCalled {
					t.Fatal("expected reviewer to be called, timed out waiting")
				}
			}
		})
	}
}

func TestHandler_IssueCommentRouting(t *testing.T) {
	tests := []struct {
		name       string
		payload    string
		wantCalled bool
	}{
		{
			name:       "/argus review on a PR triggers a review",
			payload:    argusReviewCommandOnPRPayload,
			wantCalled: true,
		},
		{
			name:       "/argus review on a plain issue is a no-op",
			payload:    argusReviewCommandOnPlainIssuePayload,
			wantCalled: false,
		},
		{
			name:       "/argus review from a bot is a no-op",
			payload:    argusReviewCommandFromBotPayload,
			wantCalled: false,
		},
		{
			name:       "a comment without the command is a no-op",
			payload:    unrelatedCommentOnPRPayload,
			wantCalled: false,
		},
		{
			name:       "an edited comment does not trigger (only created)",
			payload:    argusReviewCommandEditedPayload,
			wantCalled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(tt.payload))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(github.SHA256SignatureHeader, sign(testSecret, tt.payload))
			req.Header.Set(github.EventTypeHeader, "issue_comment")

			reviewer := newFakeReviewer()
			h := NewHandler([]byte(testSecret), reviewer, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("got status %d, want 200", rec.Code)
			}

			select {
			case <-reviewer.called:
				if !tt.wantCalled {
					t.Fatal("reviewer was called but should not have been")
				}
				if !reviewer.gotByNumber || reviewer.gotInstallation != 42 ||
					reviewer.gotOwner != "octo-org" || reviewer.gotRepo != "octo-repo" || reviewer.gotPRNumber != 7 {
					t.Fatalf("reviewer got unexpected args: %+v", reviewer)
				}
			case <-time.After(200 * time.Millisecond):
				if tt.wantCalled {
					t.Fatal("expected reviewer to be called, timed out waiting")
				}
			}
		})
	}
}
