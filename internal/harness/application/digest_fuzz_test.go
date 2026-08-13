package application

import (
	"testing"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/domain"
)

func FuzzDigestAppendRequest(f *testing.F) {
	f.Add("seed")
	f.Fuzz(func(t *testing.T, input string) {
		if !utf8.ValidString(input) {
			return
		}
		request := validAppendRequest()
		request.Events[0].Event = domain.AssistantMessageCompleted{TurnID: "turn-1", ItemID: "item-1", Text: input}
		first, firstErr := DigestAppendRequest(request)
		second, secondErr := DigestAppendRequest(request)
		if (firstErr == nil) != (secondErr == nil) || (firstErr == nil && first != second) {
			t.Fatalf("non-deterministic digest: (%x, %v) then (%x, %v)", first, firstErr, second, secondErr)
		}
	})
}
