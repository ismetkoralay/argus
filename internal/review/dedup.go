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

// findingID returns a stable identity for a Finding, derived from the file,
// line, and message. Two reviews that produce the "same" finding for
// unchanged code hash to the same ID.
func findingID(f Finding) string {
	h := sha256.Sum256([]byte(f.File + "\x00" + strconv.Itoa(f.Line) + "\x00" + f.Message))
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
