package contextengine

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

// MaxProjectedToolResultTokens computes design §10's cap:
// min(2048, max(256, protectedTail/2)).
func MaxProjectedToolResultTokens(protectedTail uint64) uint64 {
	return minUint64(2048, maxUint64(256, protectedTail/2))
}

// ErrContextUnitTooLarge reports that a single protected message or
// projected Tool Result still exceeds hardInput even after projection
// (design §10's own escape valve) — the Application layer maps this to
// context_unit_too_large (design §16).
var ErrContextUnitTooLarge = errors.New("contextengine: a single unit exceeds hard input even after projection")

// toolResultOpenMarker and toolResultCloseMarker are design §10's fixed
// framing lines. A literal occurrence of either inside excerpted content
// is escaped (see escapeMarkerDelimiters) so the projected output can
// never be mistaken for real marker syntax.
const (
	toolResultOpenMarker  = "[tool result projected by Open Code Harness]"
	toolResultCloseMarker = "[end projected tool result]"
)

// ProjectedToolResult is Tool Result projection's output (design §10).
type ProjectedToolResult struct {
	// Text is what a caller sends to the model in place of the original
	// content — either the original, unchanged, or the marker-framed
	// excerpt.
	Text string
	// Projected is true when Text is the marker-framed excerpt, not the
	// original content verbatim.
	Projected bool
}

func estimateText(meter Meter, text string) uint64 {
	return meter.EstimateMessages([]domain.ModelPromptMessage{{Role: "tool", Text: text}})
}

// frameToolResult builds the complete marker-framed text for one
// (head, tail) rune-count split of content, escaping marker delimiters in
// each excerpt independently.
func frameToolResult(eventID, content string, digest [32]byte, headRunes, tailRunes []rune) string {
	head := escapeMarkerDelimiters(string(headRunes))
	tail := escapeMarkerDelimiters(string(tailRunes))
	return fmt.Sprintf("%s\nevent_id: %s\noriginal_bytes: %d\nsha256: %x\ncontent_head:\n%s\ncontent_tail:\n%s\n%s",
		toolResultOpenMarker, eventID, len(content), digest, head, tail, toolResultCloseMarker)
}

// ProjectToolResult applies design §10's Tool Result projection over one
// Tool Result's content: content whose meter estimate is at or below
// maxTokens stays byte-identical (Projected=false). Larger content
// becomes the fixed marker format — event_id, original_bytes, sha256,
// then content_head/content_tail excerpts, 75% of the content budget to
// the head and 25% to the tail (rounded toward the head), cut only at
// UTF-8 rune boundaries, with marker delimiters inside the excerpts
// escaped. If the marker, metadata, and excerpts together would still
// exceed maxTokens, the excerpts shrink further to fit — implemented as a
// binary search over the total rune budget split at a fixed 3:1 ratio,
// since the meter's own cost function is not simply invertible. If even a
// zero-content marker (just the header/metadata lines) exceeds hardInput,
// ProjectToolResult returns ErrContextUnitTooLarge rather than silently
// truncating further or dropping the result — the caller (Task 3/9)
// decides what happens to a Tool Result unit this large; this function
// only reports that no projection fits.
func ProjectToolResult(eventID, content string, maxTokens uint64, meter Meter, hardInput uint64) (ProjectedToolResult, error) {
	if estimateText(meter, content) <= maxTokens {
		return ProjectedToolResult{Text: content}, nil
	}

	digest := sha256.Sum256([]byte(content))
	runes := []rune(content)

	split := func(totalRunes int) (head, tail []rune) {
		headCount := (totalRunes*3 + 3) / 4 // round toward head
		if headCount > len(runes) {
			headCount = len(runes)
		}
		tailCount := totalRunes - headCount
		if tailCount > len(runes)-headCount {
			tailCount = len(runes) - headCount
		}
		return runes[:headCount], runes[len(runes)-tailCount:]
	}

	low, high := 0, len(runes)
	var best string
	fits := false
	for low <= high {
		mid := (low + high) / 2
		head, tail := split(mid)
		candidate := frameToolResult(eventID, content, digest, head, tail)
		if estimateText(meter, candidate) <= maxTokens {
			best = candidate
			fits = true
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	if !fits {
		empty := frameToolResult(eventID, content, digest, nil, nil)
		if estimateText(meter, empty) > hardInput {
			return ProjectedToolResult{}, ErrContextUnitTooLarge
		}
		best = empty
	}
	return ProjectedToolResult{Text: best, Projected: true}, nil
}

// escapeMarkerDelimiters prevents excerpted content from being mistaken
// for real marker syntax: a literal occurrence of either fixed framing
// line is prefixed with a backslash, matching how the rest of the marker
// itself is plain, unescaped text — a reader distinguishes "this looks
// like a marker line because it starts with `\`" from a real one.
func escapeMarkerDelimiters(text string) string {
	text = strings.ReplaceAll(text, toolResultOpenMarker, `\`+toolResultOpenMarker)
	text = strings.ReplaceAll(text, toolResultCloseMarker, `\`+toolResultCloseMarker)
	return text
}
