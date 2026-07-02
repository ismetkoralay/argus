// Package webhook handles incoming GitHub webhook deliveries: signature
// verification, event routing, and dispatching PR reviews.
package webhook

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/go-github/v88/github"
)

// maxPayloadBytes caps the request body GitHub webhook deliveries may send.
const maxPayloadBytes = 1 << 20 // 1MB

// Reviewer reviews a pull request's diff and posts the findings, acting as
// a GitHub App installation. Implemented by internal/review.Orchestrator.
type Reviewer interface {
	ReviewPR(ctx context.Context, installationID int64, owner, repo string, prNumber int, headSHA string) error
}

// handler verifies and routes GitHub webhook deliveries.
type handler struct {
	secret   []byte
	reviewer Reviewer
	logger   *slog.Logger
}

// NewHandler builds the GitHub webhook HTTP handler. secret is the GitHub
// App webhook secret used to verify X-Hub-Signature-256. reviewer runs a
// full review on pull_request:opened and pull_request:synchronize events.
func NewHandler(secret []byte, reviewer Reviewer, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &handler{secret: secret, reviewer: reviewer, logger: logger}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPayloadBytes)

	payload, err := github.ValidatePayload(r, h.secret)
	if err != nil {
		h.logger.Warn("webhook signature validation failed", "err", err)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	event, err := github.ParseWebHook(github.WebHookType(r), payload)
	if err != nil {
		// Unrecognized event type: ack so GitHub doesn't retry, nothing to do.
		w.WriteHeader(http.StatusOK)
		return
	}

	prEvent, ok := event.(*github.PullRequestEvent)
	action := prEvent.GetAction()
	if !ok || (action != "opened" && action != "synchronize") {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Respond immediately so GitHub doesn't time out; review async.
	w.WriteHeader(http.StatusOK)

	installationID := prEvent.GetInstallation().GetID()
	owner := prEvent.GetRepo().GetOwner().GetLogin()
	repo := prEvent.GetRepo().GetName()
	prNumber := prEvent.GetPullRequest().GetNumber()
	headSHA := prEvent.GetPullRequest().GetHead().GetSHA()

	go func() {
		if err := h.reviewer.ReviewPR(context.Background(), installationID, owner, repo, prNumber, headSHA); err != nil {
			h.logger.Error("failed to review PR", "err", err, "owner", owner, "repo", repo, "pr", prNumber)
		}
	}()
}
