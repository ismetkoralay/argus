package githubapp

// TEMPORARY: this file exists only to de-risk inline-comment anchoring
// against a real PR (M1 step 1). Delete this file (and spike_anchor_test.go)
// once the manual checkpoint confirms anchoring lands on the right line;
// CreateReview in client.go is the permanent helper this proved out.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const spikeCommentBody = "🔬 Argus anchoring spike — should land on this line."

// parseFirstAddedLine returns the file-side line number of the first added
// ("+") line in a unified diff patch, and whether one was found.
func parseFirstAddedLine(patch string) (int, bool) {
	fileLine := 0
	for raw := range strings.SplitSeq(patch, "\n") {
		switch {
		case strings.HasPrefix(raw, "@@ "):
			parts := strings.Fields(raw)
			if len(parts) < 3 {
				continue
			}
			newRange := strings.TrimPrefix(parts[2], "+")
			start, _, _ := strings.Cut(newRange, ",")
			n, err := strconv.Atoi(start)
			if err != nil {
				continue
			}
			fileLine = n - 1
		case strings.HasPrefix(raw, "+++"):
			// Not a content line; ignore.
		case strings.HasPrefix(raw, "+"):
			fileLine++
			return fileLine, true
		case strings.HasPrefix(raw, "-"):
			// Removed line: doesn't exist on the new side, don't advance.
		default:
			fileLine++
		}
	}
	return 0, false
}

// SpikeAnchorFirstChangedLine posts one hardcoded inline comment on the
// first added line of the PR's first changed (non-binary) file, to prove
// out CreateReview's line anchoring against a real PR.
func (c *Client) SpikeAnchorFirstChangedLine(ctx context.Context, installationID int64, owner, repo string, prNumber int, headSHA string) error {
	ghClient, err := c.installationClient(installationID)
	if err != nil {
		return err
	}

	files, _, err := ghClient.PullRequests.ListFiles(ctx, owner, repo, prNumber, nil)
	if err != nil {
		return fmt.Errorf("failed to list PR files: %w", err)
	}

	for _, f := range files {
		patch := f.GetPatch()
		if patch == "" {
			continue
		}
		line, ok := parseFirstAddedLine(patch)
		if !ok {
			continue
		}
		return c.CreateReview(ctx, installationID, owner, repo, prNumber, headSHA,
			[]InlineComment{{Path: f.GetFilename(), Line: line, Body: spikeCommentBody}},
			"COMMENT", "Argus anchoring spike")
	}
	return fmt.Errorf("no changed file with an added line found")
}
