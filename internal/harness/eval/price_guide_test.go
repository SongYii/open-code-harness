package eval

import (
	"encoding/json"
	"testing"
)

// The exact JSON the operations guide shows an operator must decode into a
// PriceTable and behave the way the guide says it does.
func TestGuidePriceTableExampleDecodes(t *testing.T) {
	const example = `{
  "currency": "USD",
  "entries": [
    {
      "modelId": "replace-with-the-exact-model-id-the-provider-returns",
      "inputMicrounitsPerToken": 0,
      "outputMicrounitsPerToken": 0,
      "cachedInputMicrounitsPerToken": 0
    }
  ]
}`
	var table PriceTable
	if err := json.Unmarshal([]byte(example), &table); err != nil {
		t.Fatalf("the guide's example does not decode: %v", err)
	}
	if table.Currency != "USD" || len(table.Entries) != 1 {
		t.Fatalf("decoded = %+v", table)
	}
	// An explicit zero rate is a computed zero, not an unavailable price.
	status, cost, currency := ResolveScorerCost(&table, table.Entries[0].ModelID, 1000, 100, 900)
	if status != CostStatusComputed || cost != 0 || currency != "USD" {
		t.Fatalf("ResolveScorerCost = (%q, %d, %q); an explicit zero rate is a computed zero", status, cost, currency)
	}
	// A model absent from entries is unavailable, never a computed zero.
	status, cost, currency = ResolveScorerCost(&table, "some-other-model", 1000, 100, 900)
	if status != CostStatusUnavailable || cost != 0 || currency != "" {
		t.Fatalf("ResolveScorerCost = (%q, %d, %q); an absent model must be unavailable", status, cost, currency)
	}
}
