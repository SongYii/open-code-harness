package eval

import "testing"

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
