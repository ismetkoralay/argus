package review

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
)

// findingMarkerFmt wraps a Finding's stable identity (see findingID) in an
// HTML comment appended to its posted comment body. A later review reads
// this back (via extractFindingID) to recognize findings it already posted
// without needing a database — GitHub's comment listing is the only state
// Argus keeps.
const findingMarkerFmt = "<!-- argus-finding:%s -->"

var findingMarkerRe = regexp.MustCompile(`<!-- argus-finding:([0-9a-f]+) -->`)

// findingID returns a stable identity for a Finding, derived from the file
// and line only. The LLM's message wording (and even its category/severity
// pick) isn't stable across review runs for the same unchanged code, so
// including Message here would defeat dedup — two calls describing the same
// line differently would hash to different IDs and both get posted. File+line
// trades off catching multiple distinct findings on one line, but that's the
// only signal that stays stable across reviews without a database.
func findingID(f Finding) string {
	h := sha256.Sum256([]byte(f.File + "\x00" + strconv.Itoa(f.Line)))
	return hex.EncodeToString(h[:8])
}

// withFindingMarker appends f's identity marker to body.
func withFindingMarker(body string, f Finding) string {
	return fmt.Sprintf("%s\n%s", body, fmt.Sprintf(findingMarkerFmt, findingID(f)))
}

// extractFindingID reads a finding identity marker out of a posted comment
// body, if present.
func extractFindingID(body string) (string, bool) {
	m := findingMarkerRe.FindStringSubmatch(body)
	if m == nil {
		return "", false
	}
	return m[1], true
}
