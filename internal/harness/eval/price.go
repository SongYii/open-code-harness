package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"time"
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

	// Scaled rates preserve prices below one currency microunit per token.
	// They are mutually exclusive with the legacy per-token fields. The
	// final sum is rounded up once to a whole currency microunit.
	RateUnitTokens                   uint64 `json:"rateUnitTokens,omitempty"`
	InputMicrounitsPerRateUnit       int64  `json:"inputMicrounitsPerRateUnit,omitempty"`
	OutputMicrounitsPerRateUnit      int64  `json:"outputMicrounitsPerRateUnit,omitempty"`
	CachedInputMicrounitsPerRateUnit int64  `json:"cachedInputMicrounitsPerRateUnit,omitempty"`
}

// PriceTable is a frozen, digestible set of per-model prices. A model's
// own absence from Entries is how "price unavailable" is represented
// (design: "unavailable price is explicit, never zero") — there is
// deliberately no zero-value PriceEntry fallback anywhere in this file;
// EstimateCostMicrounits' own ok return is the only way a caller learns
// a price was unavailable, and it must never substitute a zero cost for
// that case.
type PriceTable struct {
	Currency   string       `json:"currency"`
	ID         string       `json:"id,omitempty"`
	Version    string       `json:"version,omitempty"`
	SourceURL  string       `json:"sourceUrl,omitempty"`
	ObservedAt string       `json:"observedAt,omitempty"`
	RateBasis  string       `json:"rateBasis,omitempty"`
	Entries    []PriceEntry `json:"entries"`
}

// DecodePriceTable rejects unknown or ambiguous rate fields before a table can
// become billing evidence.
func DecodePriceTable(data []byte) (PriceTable, error) {
	var table PriceTable
	if err := decodeStrict(data, &table); err != nil {
		return PriceTable{}, fmt.Errorf("eval: price table: %w", err)
	}
	if err := table.validate(); err != nil {
		return PriceTable{}, fmt.Errorf("eval: price table: %w", err)
	}
	return table, nil
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
	if err := table.validate(); err != nil {
		return "", fmt.Errorf("eval: price table digest: %w", err)
	}
	data, err := json.Marshal(table)
	if err != nil {
		return "", fmt.Errorf("eval: price table digest: %w", err)
	}
	sum := sha256.Sum256(data)
	return Digest("sha256:" + hex.EncodeToString(sum[:])), nil
}

// EstimateCostMicrounits computes one model call's own cost in integer
// microunits of table's Currency from recorded token counts. ok is false when
// the table is invalid, modelID is absent, or the total cannot fit the report's
// signed integer — the caller must leave cost unreported, never publish the
// returned 0 as if it were computed.
func EstimateCostMicrounits(table PriceTable, modelID string, inputTokens, outputTokens, cachedInputTokens uint64) (microunits int64, ok bool) {
	if err := table.validate(); err != nil {
		return 0, false
	}
	entry, found := table.lookup(modelID)
	if !found {
		return 0, false
	}
	unit := uint64(1)
	inputRate := entry.InputMicrounitsPerToken
	outputRate := entry.OutputMicrounitsPerToken
	cachedRate := entry.CachedInputMicrounitsPerToken
	if entry.RateUnitTokens > 0 {
		unit = entry.RateUnitTokens
		inputRate = entry.InputMicrounitsPerRateUnit
		outputRate = entry.OutputMicrounitsPerRateUnit
		cachedRate = entry.CachedInputMicrounitsPerRateUnit
	}
	total := new(big.Int)
	for _, term := range []struct {
		tokens uint64
		rate   int64
	}{{inputTokens, inputRate}, {outputTokens, outputRate}, {cachedInputTokens, cachedRate}} {
		product := new(big.Int).SetUint64(term.tokens)
		product.Mul(product, big.NewInt(term.rate))
		total.Add(total, product)
	}
	if unit > 1 {
		divisor := new(big.Int).SetUint64(unit)
		// All terms are non-negative, so adding unit-1 implements one
		// conservative ceiling after the complete call cost is summed.
		total.Add(total, new(big.Int).Sub(divisor, big.NewInt(1)))
		total.Quo(total, divisor)
	}
	if !total.IsInt64() {
		return 0, false
	}
	return total.Int64(), true
}

func (table PriceTable) validate() error {
	if !hasText(table.Currency) {
		return fmt.Errorf("%w: price table currency is required", errInvalidDocument)
	}
	if len(table.Entries) == 0 {
		return fmt.Errorf("%w: price table requires at least one entry", errInvalidDocument)
	}
	provenance := []string{table.ID, table.Version, table.SourceURL, table.ObservedAt, table.RateBasis}
	if anyText(provenance) {
		for index, value := range provenance {
			if !hasText(value) {
				return fmt.Errorf("%w: price table provenance field %d is missing", errInvalidDocument, index)
			}
		}
		source, err := url.Parse(table.SourceURL)
		if err != nil || source.Scheme != "https" || source.Host == "" || source.User != nil || source.RawQuery != "" || source.Fragment != "" {
			return fmt.Errorf("%w: price table sourceUrl must be an absolute credential-free HTTPS URL", errInvalidDocument)
		}
		if _, err := time.Parse("2006-01-02", table.ObservedAt); err != nil {
			return fmt.Errorf("%w: price table observedAt must be YYYY-MM-DD", errInvalidDocument)
		}
		if len(table.RateBasis) > 4096 {
			return fmt.Errorf("%w: price table rateBasis is too long", errInvalidDocument)
		}
	}
	seen := make(map[string]bool, len(table.Entries))
	for index, entry := range table.Entries {
		if !hasText(entry.ModelID) {
			return fmt.Errorf("%w: price entry %d modelId is required", errInvalidDocument, index)
		}
		if seen[entry.ModelID] {
			return fmt.Errorf("%w: price entry modelId %q is duplicated", errInvalidDocument, entry.ModelID)
		}
		seen[entry.ModelID] = true
		legacy := []int64{entry.InputMicrounitsPerToken, entry.OutputMicrounitsPerToken, entry.CachedInputMicrounitsPerToken}
		scaled := []int64{entry.InputMicrounitsPerRateUnit, entry.OutputMicrounitsPerRateUnit, entry.CachedInputMicrounitsPerRateUnit}
		for _, rate := range append(append([]int64(nil), legacy...), scaled...) {
			if rate < 0 {
				return fmt.Errorf("%w: price entry %q carries a negative rate", errInvalidDocument, entry.ModelID)
			}
		}
		hasLegacy := anyNonZero(legacy)
		hasScaled := anyNonZero(scaled)
		if hasLegacy && (entry.RateUnitTokens != 0 || hasScaled) {
			return fmt.Errorf("%w: price entry %q mixes legacy and scaled rates", errInvalidDocument, entry.ModelID)
		}
		if hasScaled != (entry.RateUnitTokens != 0) {
			return fmt.Errorf("%w: price entry %q must supply both rateUnitTokens and a scaled rate", errInvalidDocument, entry.ModelID)
		}
	}
	return nil
}

func anyText(values []string) bool {
	for _, value := range values {
		if hasText(value) {
			return true
		}
	}
	return false
}

func anyNonZero(values []int64) bool {
	for _, value := range values {
		if value != 0 {
			return true
		}
	}
	return false
}

// ResolveScorerCost turns a token count and an optional price table into
// the explicit cost triple a published ScorerUsage carries. It is the
// only place a caller should derive those three fields together, because
// keeping them in one function is what guarantees an unavailable price
// can never be published as a computed zero: a nil table, a model with no
// entry, or a table with no declared currency all return
// CostStatusUnavailable with a zero cost and no currency, while a free
// model that really does cost nothing returns CostStatusComputed with a
// genuine zero.
func ResolveScorerCost(table *PriceTable, modelID string, inputTokens, outputTokens, cachedInputTokens uint64) (CostStatus, int64, string) {
	if table == nil || !hasText(table.Currency) {
		return CostStatusUnavailable, 0, ""
	}
	microunits, ok := EstimateCostMicrounits(*table, modelID, inputTokens, outputTokens, cachedInputTokens)
	if !ok {
		return CostStatusUnavailable, 0, ""
	}
	return CostStatusComputed, microunits, table.Currency
}
