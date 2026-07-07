package review

import (
	"strings"
	"testing"

	"github.com/ismetkoralay/argus/internal/githubapp"
)

func TestBuildDiffUnits_FiltersBinaryAndLockfiles(t *testing.T) {
	files := []githubapp.PRFile{
		{Filename: "main.go", Patch: "@@ -1,1 +1,2 @@\n+bug", Status: "modified"},
		{Filename: "image.png", Patch: "", Status: "modified"},
		{Filename: "go.sum", Patch: "@@ -1,1 +1,1 @@\n-a\n+b", Status: "modified"},
		{Filename: "vendor/package-lock.json", Patch: "@@ -1,1 +1,1 @@\n-a\n+b", Status: "modified"},
	}

	units := BuildDiffUnits(files)

	if len(units) != 1 {
		t.Fatalf("got %d units, want 1: %+v", len(units), units)
	}
	if units[0].File != "main.go" {
		t.Fatalf("got file %q, want main.go", units[0].File)
	}
}

func TestBuildDiffUnits_SmallFileIsOneUnit(t *testing.T) {
	files := []githubapp.PRFile{
		{Filename: "main.go", Patch: "@@ -1,3 +1,4 @@\n line1\n+line2\n line3\n line4", Status: "modified"},
	}

	units := BuildDiffUnits(files)

	if len(units) != 1 {
		t.Fatalf("got %d units, want 1: %+v", len(units), units)
	}
	if units[0].File != "main.go" || units[0].Hunk != files[0].Patch {
		t.Fatalf("got %+v, want file=main.go hunk=%q", units[0], files[0].Patch)
	}
}

func TestBuildDiffUnits_OversizedFileSplitsPerHunk(t *testing.T) {
	hunk1 := "@@ -1,1 +1,1 @@\n" + strings.Repeat("+padding line to grow this hunk past the budget\n", 200)
	hunk2 := "@@ -100,1 +100,1 @@\n" + strings.Repeat("+more padding to keep this well over budget\n", 200)
	patch := hunk1 + hunk2

	files := []githubapp.PRFile{
		{Filename: "big.go", Patch: patch, Status: "modified"},
	}

	units := BuildDiffUnits(files)

	if len(units) != 2 {
		t.Fatalf("got %d units, want 2: file lengths were %d", len(units), len(patch))
	}
	for _, u := range units {
		if u.File != "big.go" {
			t.Fatalf("got file %q, want big.go", u.File)
		}
	}
	if !strings.HasPrefix(units[0].Hunk, "@@ -1,1 +1,1 @@") {
		t.Fatalf("unit 0 doesn't start with first hunk header: %q", units[0].Hunk[:30])
	}
	if !strings.HasPrefix(units[1].Hunk, "@@ -100,1 +100,1 @@") {
		t.Fatalf("unit 1 doesn't start with second hunk header: %q", units[1].Hunk[:30])
	}
}

func TestFilterIgnored(t *testing.T) {
	files := []githubapp.PRFile{
		{Filename: "main.go", Patch: "patch"},
		{Filename: "vendor/pkg/mod.go", Patch: "patch"},
		{Filename: "web/dist/bundle.min.js", Patch: "patch"},
		{Filename: "internal/gen/api.pb.go", Patch: "patch"},
	}

	tests := []struct {
		name   string
		ignore []string
		want   []string
	}{
		{
			name:   "no patterns keeps everything",
			ignore: nil,
			want:   []string{"main.go", "vendor/pkg/mod.go", "web/dist/bundle.min.js", "internal/gen/api.pb.go"},
		},
		{
			name:   "simple glob matches one file",
			ignore: []string{"web/dist/bundle.min.js"},
			want:   []string{"main.go", "vendor/pkg/mod.go", "internal/gen/api.pb.go"},
		},
		{
			name:   "recursive ** glob matches any depth under a directory",
			ignore: []string{"vendor/**"},
			want:   []string{"main.go", "web/dist/bundle.min.js", "internal/gen/api.pb.go"},
		},
		{
			name:   "pattern with no match keeps everything",
			ignore: []string{"**/*.lock"},
			want:   []string{"main.go", "vendor/pkg/mod.go", "web/dist/bundle.min.js", "internal/gen/api.pb.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterIgnored(files, tt.ignore)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d files, want %d: %+v", len(got), len(tt.want), got)
			}
			for i, f := range got {
				if f.Filename != tt.want[i] {
					t.Fatalf("file %d: got %q, want %q", i, f.Filename, tt.want[i])
				}
			}
		})
	}
}
