package llm

import "testing"

// ContextTokens is metadata describing the latest physical response; it is
// replaced, never summed, and the four billing counters stay additive.
func TestUsageContextTokensIsNotAdditive(t *testing.T) {
	var u Usage
	u.Add(Usage{InputTokens: 10, OutputTokens: 2, ContextTokens: 12})
	u.Add(Usage{InputTokens: 20, OutputTokens: 3, ContextTokens: 23})
	if u.InputTokens != 30 || u.OutputTokens != 5 || u.ContextTokens != 23 {
		t.Fatalf("got %+v", u)
	}
	// A response without a footprint keeps the last one reported.
	u.Add(Usage{InputTokens: 1})
	if u.ContextTokens != 23 {
		t.Fatalf("zero ContextTokens overwrote footprint: %+v", u)
	}
}

func TestUsageMergeEchoCarriesContextTokens(t *testing.T) {
	u := Usage{InputTokens: 5}
	u.MergeEcho(Usage{OutputTokens: 2, ContextTokens: 7})
	if u.ContextTokens != 7 || u.OutputTokens != 2 || u.InputTokens != 5 {
		t.Fatalf("got %+v", u)
	}
}
