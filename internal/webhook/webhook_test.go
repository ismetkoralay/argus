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

const closedPRPayload = `{
  "action": "closed",
  "number": 7,
  "pull_request": {"number": 7},
  "repository": {"name": "octo-repo", "owner": {"login": "octo-org"}},
  "installation": {"id": 42}
}`

type fakeCommenter struct {
	called          chan struct{}
	err             error
	gotInstallation int64
	gotOwner        string
	gotRepo         string
	gotPRNumber     int
}

func newFakeCommenter() *fakeCommenter {
	return &fakeCommenter{called: make(chan struct{}, 1)}
}

func (f *fakeCommenter) CommentOnPR(_ context.Context, installationID int64, owner, repo string, prNumber int, _ string) error {
	f.gotInstallation = installationID
	f.gotOwner = owner
	f.gotRepo = repo
	f.gotPRNumber = prNumber
	f.called <- struct{}{}
	return f.err
}

// TEMPORARY: fakeSpikeAnchorer supports webhook_test.go's coverage of the
// anchoring spike trigger. Delete alongside the spike wiring in webhook.go.
type fakeSpikeAnchorer struct {
	called          chan struct{}
	err             error
	gotInstallation int64
	gotOwner        string
	gotRepo         string
	gotPRNumber     int
	gotHeadSHA      string
}

func newFakeSpikeAnchorer() *fakeSpikeAnchorer {
	return &fakeSpikeAnchorer{called: make(chan struct{}, 1)}
}

func (f *fakeSpikeAnchorer) SpikeAnchorFirstChangedLine(_ context.Context, installationID int64, owner, repo string, prNumber int, headSHA string) error {
	f.gotInstallation = installationID
	f.gotOwner = owner
	f.gotRepo = repo
	f.gotPRNumber = prNumber
	f.gotHeadSHA = headSHA
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

	commenter := newFakeCommenter()
	h := NewHandler([]byte(secret), commenter, nil, nil)
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
	}{
		{
			name:       "pull_request opened triggers comment",
			eventType:  "pull_request",
			payload:    openedPRPayload,
			wantCalled: true,
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

			commenter := newFakeCommenter()
			h := NewHandler([]byte(testSecret), commenter, nil, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("got status %d, want 200", rec.Code)
			}

			select {
			case <-commenter.called:
				if !tt.wantCalled {
					t.Fatal("commenter was called but should not have been")
				}
				if commenter.gotInstallation != 42 || commenter.gotOwner != "octo-org" ||
					commenter.gotRepo != "octo-repo" || commenter.gotPRNumber != 7 {
					t.Fatalf("commenter got unexpected args: %+v", commenter)
				}
			case <-time.After(200 * time.Millisecond):
				if tt.wantCalled {
					t.Fatal("expected commenter to be called, timed out waiting")
				}
			}
		})
	}
}

// TEMPORARY: covers the anchoring spike trigger. Delete alongside the spike
// wiring in webhook.go once the manual checkpoint confirms anchoring works.
func TestHandler_SpikeAnchorTriggeredOnOpened(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(openedPRPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(github.SHA256SignatureHeader, sign(testSecret, openedPRPayload))
	req.Header.Set(github.EventTypeHeader, "pull_request")

	commenter := newFakeCommenter()
	spike := newFakeSpikeAnchorer()
	h := NewHandler([]byte(testSecret), commenter, spike, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}

	select {
	case <-spike.called:
		if spike.gotInstallation != 42 || spike.gotOwner != "octo-org" ||
			spike.gotRepo != "octo-repo" || spike.gotPRNumber != 7 || spike.gotHeadSHA != "deadbeef" {
			t.Fatalf("spike anchorer got unexpected args: %+v", spike)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected spike anchorer to be called, timed out waiting")
	}
}
