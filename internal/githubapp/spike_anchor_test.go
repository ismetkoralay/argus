package githubapp

// TEMPORARY: this file exists only to de-risk inline-comment anchoring
// against a real PR (M1 step 1). Delete alongside spike_anchor.go once the
// manual checkpoint confirms anchoring lands on the right line.

import "testing"

func TestParseFirstAddedLine(t *testing.T) {
	tests := []struct {
		name     string
		patch    string
		wantLine int
		wantOK   bool
	}{
		{
			name:     "single hunk, first line added",
			patch:    "@@ -1,3 +1,4 @@\n+new line\n line1\n line2\n line3",
			wantLine: 1,
			wantOK:   true,
		},
		{
			name:     "added line after context",
			patch:    "@@ -1,3 +1,4 @@\n line1\n+new line\n line2\n line3",
			wantLine: 2,
			wantOK:   true,
		},
		{
			name:     "added line in second hunk",
			patch:    "@@ -1,2 +1,2 @@\n line1\n line2\n@@ -10,2 +11,3 @@\n line10\n+new line\n line11",
			wantLine: 12,
			wantOK:   true,
		},
		{
			name:     "no added lines",
			patch:    "@@ -1,3 +1,3 @@\n line1\n line2\n line3",
			wantLine: 0,
			wantOK:   false,
		},
		{
			name:     "empty patch",
			patch:    "",
			wantLine: 0,
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLine, gotOK := parseFirstAddedLine(tt.patch)
			if gotOK != tt.wantOK || gotLine != tt.wantLine {
				t.Fatalf("parseFirstAddedLine(%q) = (%d, %v), want (%d, %v)", tt.patch, gotLine, gotOK, tt.wantLine, tt.wantOK)
			}
		})
	}
}
