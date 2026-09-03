package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// PriceEntry is one model's own integer-microunit price (design's own
// "optional integer-microunit price table digests"). A microunit is
// 1e-6 of PriceTable's own declared Currency; this package never assumes
// a specific currency.
type PriceEntry struct {
	ModelID                       string `json:"modelId"`
	InputMicrounitsPerToken       int64  `json:"inputMicrounitsPerToken"`
	OutputMicrounitsPerToken      int64  `json:"outputMicrounitsPerToken"`
	CachedInputMicrounitsPerToken int64  `json:"cachedInputMicrounitsPerToken"`
}

// PriceTable is a frozen, digestible set of per-model prices. A model's
// own absence from Entries is how "price unavailable" is represented
// (design: "unavailable price is explicit, never zero") — there is
// deliberately no zero-value PriceEntry fallback anywhere in this file;
// EstimateCostMicrounits' own ok return is the only way a caller learns
// a price was unavailable, and it must never substitute a zero cost for
// that case.
type PriceTable struct {
	Currency string       `json:"currency"`
	Entries  []PriceEntry `json:"entries"`
}

func (table PriceTable) lookup(modelID string) (PriceEntry, bool) {
	for _, entry := range table.Entries {
		if entry.ModelID == modelID {
			return entry, true
		}
	}
	return PriceEntry{}, false
}

// PriceTableDigest is table's own canonical SHA-256, following this
// package's established digest.go convention (SHA-256 over canonical
// JSON bytes) so a price table can be frozen and referenced by digest
// exactly like Scenario/Subject/Executor.
func PriceTableDigest(table PriceTable) (Digest, error) {
	data, err := json.Marshal(table)
	if err != nil {
		return "", fmt.Errorf("eval: price table digest: %w", err)
	}
	sum := sha256.Sum256(data)
	return Digest("sha256:" + hex.EncodeToString(sum[:])), nil
}

// EstimateCostMicrounits computes one model call's own cost in
// integer microunits of table's own Currency from token counts already
// recorded as usage facts. ok is false when modelID has no entry in
// table at all — the caller must then leave cost unreported (never
// report the returned 0 as if it were a real, computed zero cost).
func EstimateCostMicrounits(table PriceTable, modelID string, inputTokens, outputTokens, cachedInputTokens uint64) (microunits int64, ok bool) {
	entry, found := table.lookup(modelID)
	if !found {
		return 0, false
	}
	total := int64(inputTokens)*entry.InputMicrounitsPerToken +
		int64(outputTokens)*entry.OutputMicrounitsPerToken +
		int64(cachedInputTokens)*entry.CachedInputMicrounitsPerToken
	return total, true
}
