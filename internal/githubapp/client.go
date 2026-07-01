// Package githubapp wraps GitHub App authentication (JWT + per-installation
// tokens) and the small set of GitHub API calls Argus needs.
package githubapp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v88/github"
)

// Client authenticates as a GitHub App and acts on its installations.
type Client struct {
	appsTransport *ghinstallation.AppsTransport
	// baseURL overrides the GitHub API base URL; only set in tests.
	baseURL string
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

// CommentOnPR posts a top-level comment on the given pull request, acting as
// the installation identified by installationID.
func (c *Client) CommentOnPR(ctx context.Context, installationID int64, owner, repo string, prNumber int, body string) error {
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
		return fmt.Errorf("failed to build github client: %w", err)
	}

	if _, _, err := ghClient.Issues.CreateComment(ctx, owner, repo, prNumber, &github.IssueComment{Body: &body}); err != nil {
		return fmt.Errorf("failed to create PR comment: %w", err)
	}
	return nil
}
