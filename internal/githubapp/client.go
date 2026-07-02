// Package githubapp wraps GitHub App authentication (JWT + per-installation
// tokens) and the small set of GitHub API calls Argus needs.
package githubapp

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v88/github"
)

// summaryCommentMarker prefixes Argus's summary comment so re-reviews can
// find and edit it instead of posting a new one each time.
const summaryCommentMarker = "<!-- argus-summary -->"

// Client authenticates as a GitHub App and acts on its installations.
type Client struct {
	appsTransport *ghinstallation.AppsTransport
	// baseURL overrides the GitHub API base URL; only set in tests.
	baseURL string
}

// InlineComment is a single review comment anchored to a file/line on the
// diff's RIGHT side (the PR's head commit).
type InlineComment struct {
	Path string
	Line int
	Body string
}

// PRFile is one file changed in a pull request, with its unified diff
// patch. Patch is empty for binary or very large files (GitHub omits it).
type PRFile struct {
	Filename string
	Patch    string
	Status   string
}

// New builds a Client that signs requests as the GitHub App identified by
// appID, using privateKeyPEM to mint per-installation tokens on demand.
func New(appID int64, privateKeyPEM []byte) (*Client, error) {
	atr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, appID, privateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to build github app transport: %w", err)
	}
	return &Client{appsTransport: atr}, nil
}

// installationClient builds a go-github client authenticated as the given
// installation.
func (c *Client) installationClient(installationID int64) (*github.Client, error) {
	itr := ghinstallation.NewFromAppsTransport(c.appsTransport, installationID)
	if c.baseURL != "" {
		itr.BaseURL = c.baseURL
	}

	opts := []github.ClientOptionsFunc{github.WithTransport(itr)}
	if c.baseURL != "" {
		opts = append(opts, github.WithURLs(&c.baseURL, &c.baseURL))
	}
	ghClient, err := github.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to build github client: %w", err)
	}
	return ghClient, nil
}

// CommentOnPR posts a top-level comment on the given pull request, acting as
// the installation identified by installationID.
func (c *Client) CommentOnPR(ctx context.Context, installationID int64, owner, repo string, prNumber int, body string) error {
	ghClient, err := c.installationClient(installationID)
	if err != nil {
		return err
	}

	if _, _, err := ghClient.Issues.CreateComment(ctx, owner, repo, prNumber, &github.IssueComment{Body: &body}); err != nil {
		return fmt.Errorf("failed to create PR comment: %w", err)
	}
	return nil
}

// UpsertSummaryComment creates or updates Argus's single summary comment on
// the given pull request. body must start with summaryCommentMarker so a
// later call can find and edit it instead of posting a duplicate.
func (c *Client) UpsertSummaryComment(ctx context.Context, installationID int64, owner, repo string, prNumber int, body string) error {
	ghClient, err := c.installationClient(installationID)
	if err != nil {
		return err
	}

	comments, _, err := ghClient.Issues.ListComments(ctx, owner, repo, prNumber, nil)
	if err != nil {
		return fmt.Errorf("failed to list PR comments: %w", err)
	}

	for _, comment := range comments {
		if strings.HasPrefix(comment.GetBody(), summaryCommentMarker) {
			if _, _, err := ghClient.Issues.EditComment(ctx, owner, repo, comment.GetID(), &github.IssueComment{Body: &body}); err != nil {
				return fmt.Errorf("failed to edit summary comment: %w", err)
			}
			return nil
		}
	}

	if _, _, err := ghClient.Issues.CreateComment(ctx, owner, repo, prNumber, &github.IssueComment{Body: &body}); err != nil {
		return fmt.Errorf("failed to create summary comment: %w", err)
	}
	return nil
}

// ListPRFiles fetches every changed file (across all pages) for the given
// pull request.
func (c *Client) ListPRFiles(ctx context.Context, installationID int64, owner, repo string, prNumber int) ([]PRFile, error) {
	ghClient, err := c.installationClient(installationID)
	if err != nil {
		return nil, err
	}

	var files []PRFile
	opts := &github.ListOptions{PerPage: 100}
	for {
		page, resp, err := ghClient.PullRequests.ListFiles(ctx, owner, repo, prNumber, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list PR files: %w", err)
		}
		for _, f := range page {
			files = append(files, PRFile{
				Filename: f.GetFilename(),
				Patch:    f.GetPatch(),
				Status:   f.GetStatus(),
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return files, nil
}

// CreateReview posts a single GitHub review on the given pull request,
// anchoring each inline comment to its file/line on the head commit
// (commitSHA). event is one of "COMMENT", "APPROVE", or "REQUEST_CHANGES".
func (c *Client) CreateReview(ctx context.Context, installationID int64, owner, repo string, prNumber int, commitSHA string, comments []InlineComment, event, body string) error {
	ghClient, err := c.installationClient(installationID)
	if err != nil {
		return err
	}

	draftComments := make([]*github.DraftReviewComment, 0, len(comments))
	for _, comment := range comments {
		draftComments = append(draftComments, &github.DraftReviewComment{
			Path: github.Ptr(comment.Path),
			Line: github.Ptr(comment.Line),
			Body: github.Ptr(comment.Body),
		})
	}

	review := &github.PullRequestReviewRequest{
		CommitID: github.Ptr(commitSHA),
		Body:     github.Ptr(body),
		Event:    github.Ptr(event),
		Comments: draftComments,
	}

	if _, _, err := ghClient.PullRequests.CreateReview(ctx, owner, repo, prNumber, review); err != nil {
		return fmt.Errorf("failed to create PR review: %w", err)
	}
	return nil
}
