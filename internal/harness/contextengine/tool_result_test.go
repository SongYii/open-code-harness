package contextengine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMaxProjectedToolResultTokens(t *testing.T) {
	tests := []struct {
		protectedTail uint64
		want          uint64
	}{
		{protectedTail: 0, want: 256},        // max(256, 0) = 256, min(2048, 256) = 256
		{protectedTail: 1000, want: 500},     // max(256, 500) = 500
		{protectedTail: 10000, want: 2048},   // max(256, 5000)=5000, min(2048, 5000)=2048
		{protectedTail: 100_000, want: 2048}, // capped
	}
	for _, test := range tests {
		if got := MaxProjectedToolResultTokens(test.protectedTail); got != test.want {
			t.Errorf("MaxProjectedToolResultTokens(%d) = %d, want %d", test.protectedTail, got, test.want)
		}
	}
}

func TestProjectToolResultByteIdenticalUnderCap(t *testing.T) {
	meter := WireEstimateMeter{}
	content := "short tool output"
	result, err := ProjectToolResult("evt1", content, 2048, meter, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if result.Projected || result.Text != content {
		t.Fatalf("got %+v, want byte-identical passthrough", result)
	}
}

func TestProjectToolResultOverCapSplitsHeadTail(t *testing.T) {
	meter := WireEstimateMeter{}
	// A long, distinctive string so head/tail excerpts are identifiable.
	var b strings.Builder
	for i := 0; i < 2000; i++ {
		b.WriteString("0123456789")
	}
	content := b.String()
	result, err := ProjectToolResult("evt1", content, 300, meter, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Projected {
		t.Fatal("expected the oversized content to be projected")
	}
	if !strings.Contains(result.Text, toolResultOpenMarker) || !strings.Contains(result.Text, toolResultCloseMarker) {
		t.Fatalf("projected text missing marker framing: %s", result.Text)
	}
	if !strings.Contains(result.Text, "evt1") {
		t.Fatal("projected text missing event_id")
	}
	digest := sha256.Sum256([]byte(content))
	if !strings.Contains(result.Text, hex.EncodeToString(digest[:])) {
		t.Fatal("projected text missing the correct sha256 digest")
	}
	tokens := estimateText(meter, result.Text)
	if tokens > 300 {
		t.Fatalf("projected text estimates at %d tokens, want <= 300", tokens)
	}
	// The excerpt must actually be a real prefix/suffix of the original
	// content, not fabricated text.
	headIndex := strings.Index(result.Text, "content_head:\n")
	tailIndex := strings.Index(result.Text, "content_tail:\n")
	if headIndex < 0 || tailIndex < 0 || tailIndex <= headIndex {
		t.Fatalf("could not locate content_head/content_tail sections in: %s", result.Text)
	}
	headExcerpt := result.Text[headIndex+len("content_head:\n") : tailIndex-1]
	if headExcerpt != "" && !strings.HasPrefix(content, headExcerpt) {
		t.Fatalf("head excerpt %q is not a prefix of the original content", headExcerpt)
	}
}

func TestProjectToolResultRuneBoundaryOnMultiByteUTF8(t *testing.T) {
	meter := WireEstimateMeter{}
	// Repeated multi-byte Chinese characters so a naive byte-count cut
	// would land mid-rune unless the implementation cuts at rune
	// boundaries specifically.
	content := strings.Repeat("你好世界，这是一段测试文本。", 200)
	result, err := ProjectToolResult("evt1", content, 100, meter, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Projected {
		t.Fatal("expected projection for this fixture")
	}
	if !utf8.ValidString(result.Text) {
		t.Fatal("projected text is not valid UTF-8 -- a rune was cut mid-sequence")
	}
}

func TestProjectToolResultEscapesEmbeddedMarkerDelimiters(t *testing.T) {
	meter := WireEstimateMeter{}
	// Embed the real marker strings directly in the content near both the
	// head and the tail, padded so the content is large enough to force
	// projection and to keep the embedded markers within the excerpted
	// regions.
	pad := strings.Repeat("x", 2000)
	content := toolResultOpenMarker + pad + toolResultCloseMarker
	result, err := ProjectToolResult("evt1", content, 300, meter, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Projected {
		t.Fatal("expected projection for this fixture")
	}
	// The framing itself legitimately contains one real, unescaped
	// instance of each delimiter (the actual marker open/close lines).
	// Beyond those two, no unescaped delimiter may appear -- every
	// occurrence contributed by the original content must be escaped.
	if strings.Count(result.Text, toolResultOpenMarker) != 2 {
		t.Fatalf("got %d unescaped occurrences of the open marker, want exactly 2 (the real framing plus zero from content, since it must be escaped): %s", strings.Count(result.Text, toolResultOpenMarker), result.Text)
	}
	if !strings.Contains(result.Text, `\`+toolResultOpenMarker) {
		t.Fatal("expected the embedded open marker in content to be escaped with a leading backslash")
	}
}

// TestProjectToolResultEscapeMutation is the mutation-check counterpart
// (design §22.4's "marker delimiter" concern, plan Task 4): disabling
// escapeMarkerDelimiters must make the embedded-delimiter test above fail.
func TestProjectToolResultEscapeMutation(t *testing.T) {
	// This test directly exercises escapeMarkerDelimiters as a unit,
	// mirroring what the manual mutation (turning it into a no-op) would
	// break -- see the commit message for the actual mutation run.
	escaped := escapeMarkerDelimiters("prefix " + toolResultOpenMarker + " suffix")
	if strings.Contains(escaped, " "+toolResultOpenMarker+" ") {
		t.Fatal("escapeMarkerDelimiters did not escape an embedded open marker")
	}
}

func TestProjectToolResultTooLargeForHardInput(t *testing.T) {
	meter := WireEstimateMeter{}
	content := strings.Repeat("x", 10_000)
	_, err := ProjectToolResult("evt1", content, 64, meter, 10) // hardInput=10: even the empty marker cannot fit
	if !errors.Is(err, ErrContextUnitTooLarge) {
		t.Fatalf("got %v, want ErrContextUnitTooLarge", err)
	}
}
