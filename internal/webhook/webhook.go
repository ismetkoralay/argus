// Package webhook handles incoming GitHub webhook deliveries: signature
// verification, event routing, and dispatching PR reviews.
package webhook

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/google/go-github/v88/github"

	"github.com/ismetkoralay/argus/internal/logging"
)

// maxPayloadBytes caps the request body GitHub webhook deliveries may send.
const maxPayloadBytes = 1 << 20 // 1MB

// argusReviewCommandRe matches the /argus review command anywhere in a
// comment body (not a bare substring match, so it won't fire on unrelated
// prose that happens to mention "argus review" without the leading slash).
var argusReviewCommandRe = regexp.MustCompile(`(?i)/argus\s+review\b`)

// Reviewer reviews a pull request's diff and posts the findings, acting as
// a GitHub App installation. Implemented by internal/review.Orchestrator.
type Reviewer interface {
	ReviewPR(ctx context.Context, installationID int64, owner, repo string, prNumber int, headSHA string) error
	ReviewPRByNumber(ctx context.Context, installationID int64, owner, repo string, prNumber int) error
}

// handler verifies and routes GitHub webhook deliveries.
type handler struct {
	secret   []byte
	reviewer Reviewer
	logger   *slog.Logger
}

// NewHandler builds the GitHub webhook HTTP handler. secret is the GitHub
// App webhook secret used to verify X-Hub-Signature-256. reviewer runs a
// full review on pull_request:opened, pull_request:synchronize, and an
// issue_comment containing "/argus review" on an open pull request.
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

	reqLogger := h.logger.With("delivery_id", github.DeliveryID(r))

	var task func()
	switch e := event.(type) {
	case *github.PullRequestEvent:
		task = h.pullRequestTask(e, reqLogger)
	case *github.IssueCommentEvent:
		task = h.issueCommentTask(e, reqLogger)
	}

	// Respond immediately so GitHub doesn't time out; review async.
	w.WriteHeader(http.StatusOK)
	if task != nil {
		go task()
	}
}

// pullRequestTask returns the review to run for a pull_request event, or
// nil if the action isn't one that warrants a review. reqLogger already
// carries the delivery ID; the returned closure stores it on the context
// passed to the reviewer so every log line for this review is correlated.
func (h *handler) pullRequestTask(e *github.PullRequestEvent, reqLogger *slog.Logger) func() {
	action := e.GetAction()
	if action != "opened" && action != "synchronize" {
		return nil
	}

	installationID := e.GetInstallation().GetID()
	owner := e.GetRepo().GetOwner().GetLogin()
	repo := e.GetRepo().GetName()
	prNumber := e.GetPullRequest().GetNumber()
	headSHA := e.GetPullRequest().GetHead().GetSHA()

	return func() {
		ctx := logging.WithLogger(context.Background(), reqLogger)
		if err := h.reviewer.ReviewPR(ctx, installationID, owner, repo, prNumber, headSHA); err != nil {
			reqLogger.Error("failed to review PR", "err", err, "owner", owner, "repo", repo, "pr", prNumber, "head_sha", headSHA)
		}
	}
}

// issueCommentTask returns the review to run for an issue_comment event, or
// nil unless it's a newly created "/argus review" comment from a non-bot
// user on an open pull request (not a plain issue). reqLogger already
// carries the delivery ID; the returned closure stores it on the context
// passed to the reviewer so every log line for this review is correlated.
func (h *handler) issueCommentTask(e *github.IssueCommentEvent, reqLogger *slog.Logger) func() {
	if e.GetAction() != "created" {
		return nil
	}
	if e.GetIssue().GetPullRequestLinks() == nil {
		return nil
	}
	if !argusReviewCommandRe.MatchString(e.GetComment().GetBody()) {
		return nil
	}
	if e.GetComment().GetUser().GetType() == "Bot" {
		return nil
	}

	installationID := e.GetInstallation().GetID()
	owner := e.GetRepo().GetOwner().GetLogin()
	repo := e.GetRepo().GetName()
	prNumber := e.GetIssue().GetNumber()

	return func() {
		ctx := logging.WithLogger(context.Background(), reqLogger)
		if err := h.reviewer.ReviewPRByNumber(ctx, installationID, owner, repo, prNumber); err != nil {
			reqLogger.Error("failed to review PR via /argus review", "err", err, "owner", owner, "repo", repo, "pr", prNumber)
		}
	}
}
