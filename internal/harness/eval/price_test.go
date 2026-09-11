package eval

import (
	"encoding/json"
	"math"
	"testing"
)

func TestEstimateCostMicrounitsComputesFromTable(t *testing.T) {
	table := PriceTable{
		Currency: "usd",
		Entries: []PriceEntry{
			{ModelID: "model-a", InputMicrounitsPerToken: 10, OutputMicrounitsPerToken: 30, CachedInputMicrounitsPerToken: 2},
		},
	}
	cost, ok := EstimateCostMicrounits(table, "model-a", 100, 50, 20)
	if !ok {
		t.Fatal("EstimateCostMicrounits() ok = false, want true")
	}
	want := int64(100*10 + 50*30 + 20*2)
	if cost != want {
		t.Fatalf("cost = %d, want %d", cost, want)
	}
}

// TestEstimateCostMicrounitsExplicitlyUnavailableForAnUnpricedModel is
// design's own "unavailable price is explicit, never zero": a model
// absent from the table must never silently price as zero.
func TestEstimateCostMicrounitsExplicitlyUnavailableForAnUnpricedModel(t *testing.T) {
	table := PriceTable{Currency: "usd", Entries: []PriceEntry{{ModelID: "model-a", InputMicrounitsPerToken: 10}}}
	cost, ok := EstimateCostMicrounits(table, "model-b", 100, 50, 0)
	if ok {
		t.Fatalf("EstimateCostMicrounits() ok = true for an unpriced model, want false (cost = %d)", cost)
	}
}

func TestPriceTableDigestIsDeterministicAndSensitive(t *testing.T) {
	table := PriceTable{Currency: "usd", Entries: []PriceEntry{{ModelID: "model-a", InputMicrounitsPerToken: 10, OutputMicrounitsPerToken: 30}}}
	first, err := PriceTableDigest(table)
	if err != nil {
		t.Fatalf("PriceTableDigest: %v", err)
	}
	second, err := PriceTableDigest(table)
	if err != nil {
		t.Fatalf("PriceTableDigest: %v", err)
	}
	if first != second {
		t.Fatalf("PriceTableDigest is not deterministic: %q != %q", first, second)
	}

	changed := table
	changed.Entries = append([]PriceEntry(nil), table.Entries...)
	changed.Entries[0].OutputMicrounitsPerToken = 31
	changedDigest, err := PriceTableDigest(changed)
	if err != nil {
		t.Fatalf("PriceTableDigest: %v", err)
	}
	if changedDigest == first {
		t.Fatal("PriceTableDigest did not change after a price field changed")
	}
}

func TestEstimateCostSupportsFractionalMicrounitsPerToken(t *testing.T) {
	table := PriceTable{Currency: "USD", Entries: []PriceEntry{{
		ModelID: "cheap-model", RateUnitTokens: 1_000_000,
		InputMicrounitsPerRateUnit: 660_000, OutputMicrounitsPerRateUnit: 1_980_000,
	}}}
	// $0.66/M input and $1.98/M output: 1,000 input + 100 output is
	// $0.000858, or exactly 858 microUSD.
	cost, ok := EstimateCostMicrounits(table, "cheap-model", 1_000, 100, 0)
	if !ok || cost != 858 {
		t.Fatalf("cost=%d ok=%t, want 858,true", cost, ok)
	}
	// Fractions below one microUSD are rounded up once on the total, never
	// silently truncated to a computed zero.
	cost, ok = EstimateCostMicrounits(table, "cheap-model", 1, 0, 0)
	if !ok || cost != 1 {
		t.Fatalf("small cost=%d ok=%t, want 1,true", cost, ok)
	}
}

func TestPriceTableDigestRejectsAmbiguousOrUnsafeRates(t *testing.T) {
	for _, test := range []struct {
		name  string
		entry PriceEntry
	}{
		{"mixed legacy and scaled rates", PriceEntry{ModelID: "m", InputMicrounitsPerToken: 1, RateUnitTokens: 1_000_000, InputMicrounitsPerRateUnit: 1}},
		{"scaled rates without unit", PriceEntry{ModelID: "m", InputMicrounitsPerRateUnit: 1}},
		{"unit without scaled rates", PriceEntry{ModelID: "m", RateUnitTokens: 1_000_000}},
		{"negative legacy rate", PriceEntry{ModelID: "m", InputMicrounitsPerToken: -1}},
		{"negative scaled rate", PriceEntry{ModelID: "m", RateUnitTokens: 1_000_000, OutputMicrounitsPerRateUnit: -1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := PriceTableDigest(PriceTable{Currency: "USD", Entries: []PriceEntry{test.entry}}); err == nil {
				t.Fatal("PriceTableDigest() error = nil")
			}
		})
	}
	duplicate := PriceTable{Currency: "USD", Entries: []PriceEntry{{ModelID: "m"}, {ModelID: "m"}}}
	if _, err := PriceTableDigest(duplicate); err == nil {
		t.Fatal("PriceTableDigest accepted duplicate model")
	}
	partialProvenance := PriceTable{Currency: "USD", SourceURL: "https://example.com/pricing", Entries: []PriceEntry{{ModelID: "m"}}}
	if _, err := PriceTableDigest(partialProvenance); err == nil {
		t.Fatal("PriceTableDigest accepted partial provenance")
	}
	badProvenance := PriceTable{Currency: "USD", ID: "p", Version: "v1", SourceURL: "http://example.com", ObservedAt: "today", RateBasis: "standard", Entries: []PriceEntry{{ModelID: "m"}}}
	if _, err := PriceTableDigest(badProvenance); err == nil {
		t.Fatal("PriceTableDigest accepted invalid provenance")
	}
}

func TestEstimateCostRejectsOverflow(t *testing.T) {
	table := PriceTable{Currency: "USD", Entries: []PriceEntry{{ModelID: "m", InputMicrounitsPerToken: math.MaxInt64}}}
	if cost, ok := EstimateCostMicrounits(table, "m", 2, 0, 0); ok || cost != 0 {
		t.Fatalf("overflow cost=%d ok=%t, want 0,false", cost, ok)
	}
}

func TestDecodePriceTableIsStrict(t *testing.T) {
	table := PriceTable{Currency: "USD", Entries: []PriceEntry{{ModelID: "m", InputMicrounitsPerToken: 1}}}
	data, err := json.Marshal(table)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePriceTable(data); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePriceTable(append(data[:len(data)-1], []byte(`,"unknown":true}`)...)); err == nil {
		t.Fatal("DecodePriceTable accepted an unknown field")
	}
}
