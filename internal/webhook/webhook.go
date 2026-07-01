// Package webhook handles incoming GitHub webhook deliveries: signature
// verification, event routing, and dispatching the M0 "hello PR" comment.
package webhook

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/go-github/v88/github"
)

// maxPayloadBytes caps the request body GitHub webhook deliveries may send.
const maxPayloadBytes = 1 << 20 // 1MB

// helloMessage is the static M0 acknowledgement comment.
const helloMessage = "👋 Argus is online and will review this PR."

// GithubApp posts a comment on a pull request, acting as a GitHub App
// installation. Implemented by internal/githubapp.Client.
type GithubApp interface {
	CommentOnPR(ctx context.Context, installationID int64, owner, repo string, prNumber int, body string) error
}

// SpikeAnchorer posts one hardcoded inline comment on the first changed
// line of a PR, to prove out inline-comment anchoring against a real PR.
//
// TEMPORARY: implemented by internal/githubapp.Client.SpikeAnchorFirstChangedLine.
// Delete this interface, the spike field/trigger below, and the env-gated
// wiring in cmd/service/main.go once the manual checkpoint confirms
// anchoring lands on the right line.
type SpikeAnchorer interface {
	SpikeAnchorFirstChangedLine(ctx context.Context, installationID int64, owner, repo string, prNumber int, headSHA string) error
}

// handler verifies and routes GitHub webhook deliveries.
type handler struct {
	secret    []byte
	githubApp GithubApp
	spike     SpikeAnchorer
	logger    *slog.Logger
}

// NewHandler builds the GitHub webhook HTTP handler. secret is the GitHub
// App webhook secret used to verify X-Hub-Signature-256. commenter posts the
// "hello PR" comment on pull_request:opened events. spike, if non-nil, is
// called on the same events to run the anchoring spike (nil disables it).
func NewHandler(secret []byte, githubApp GithubApp, spike SpikeAnchorer, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &handler{secret: secret, githubApp: githubApp, spike: spike, logger: logger}
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
	if !ok || prEvent.GetAction() != "opened" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Respond immediately so GitHub doesn't time out; post the comment async.
	w.WriteHeader(http.StatusOK)

	installationID := prEvent.GetInstallation().GetID()
	owner := prEvent.GetRepo().GetOwner().GetLogin()
	repo := prEvent.GetRepo().GetName()
	prNumber := prEvent.GetPullRequest().GetNumber()
	headSHA := prEvent.GetPullRequest().GetHead().GetSHA()

	go func() {
		if err := h.githubApp.CommentOnPR(context.Background(), installationID, owner, repo, prNumber, helloMessage); err != nil {
			h.logger.Error("failed to post PR comment", "err", err, "owner", owner, "repo", repo, "pr", prNumber)
		}
	}()

	// TEMPORARY: anchoring spike, see SpikeAnchorer doc comment.
	if h.spike != nil {
		go func() {
			if err := h.spike.SpikeAnchorFirstChangedLine(context.Background(), installationID, owner, repo, prNumber, headSHA); err != nil {
				h.logger.Error("anchoring spike failed", "err", err, "owner", owner, "repo", repo, "pr", prNumber)
			}
		}()
	}
}
