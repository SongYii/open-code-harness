package openaicompat

import (
	"strconv"
	"unicode/utf8"

	"github.com/SongYii/open-code-harness/internal/harness/engine"
)

// Accept only the native documented Chat Completions effort controls. In
// particular, "none" is not a hidden switch to disable replay mid-session.
func deepSeekEffort(effort engine.ReasoningEffort) bool {
	switch effort {
	case "", engine.ReasoningEffortLow, engine.ReasoningEffortHigh, engine.ReasoningEffortMax:
		return true
	default:
		return false
	}
}

// encoding/json replaces unpaired UTF-16 surrogates with U+FFFD. That is fine
// for display but cannot be called exact protocol replay. Structural JSON
// validation is still performed by the caller; this guards lossy decoding.
func losslessReplayJSON(payload string) bool {
	if !utf8.ValidString(payload) {
		return false
	}
	for index := 0; index < len(payload); index++ {
		if payload[index] != '\\' {
			continue
		}
		index++
		if index >= len(payload) {
			return false
		}
		if payload[index] != 'u' {
			continue
		}
		if index+4 >= len(payload) {
			return false
		}
		value, err := strconv.ParseUint(payload[index+1:index+5], 16, 16)
		if err != nil {
			return false
		}
		index += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value < 0xd800 || value > 0xdbff {
			continue
		}
		if index+6 >= len(payload) || payload[index+1:index+3] != `\u` {
			return false
		}
		low, err := strconv.ParseUint(payload[index+3:index+7], 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		index += 6
	}
	return true
}
