package review

import "testing"

func TestFindingID_StableAndDistinct(t *testing.T) {
	a := Finding{File: "main.go", Line: 12, Severity: "error", Category: "bug", Message: "nil check missing"}
	sameA := Finding{File: "main.go", Line: 12, Severity: "warning", Category: "style", Message: "nil check missing"}
	differentLine := Finding{File: "main.go", Line: 13, Severity: "error", Category: "bug", Message: "nil check missing"}
	differentMessage := Finding{File: "main.go", Line: 12, Severity: "error", Category: "bug", Message: "different message"}
	differentFile := Finding{File: "other.go", Line: 12, Severity: "error", Category: "bug", Message: "nil check missing"}

	if findingID(a) != findingID(sameA) {
		t.Fatal("expected identity to depend only on file+line, not severity/category")
	}
	if findingID(a) == findingID(differentLine) {
		t.Fatal("expected different line to produce a different identity")
	}
	if findingID(a) != findingID(differentMessage) {
		t.Fatal("expected identity to depend only on file+line, not message wording (LLM phrasing isn't stable across runs)")
	}
	if findingID(a) == findingID(differentFile) {
		t.Fatal("expected different file to produce a different identity")
	}
}

func TestWithFindingMarkerAndExtractFindingID_RoundTrip(t *testing.T) {
	f := Finding{File: "main.go", Line: 12, Severity: "error", Category: "bug", Message: "nil check missing"}
	body := withFindingMarker("some formatted body", f)

	got, ok := extractFindingID(body)
	if !ok {
		t.Fatal("expected to extract a finding ID, found none")
	}
	if got != findingID(f) {
		t.Fatalf("got ID %q, want %q", got, findingID(f))
	}
}

func TestExtractFindingID_NoMarker(t *testing.T) {
	if _, ok := extractFindingID("a plain comment with no marker"); ok {
		t.Fatal("expected no finding ID to be extracted")
	}
}
