package noaadapter

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/noa"
)

// Live check against a real OpenAI-compatible gateway (issue #12). Skipped unless
// NORMA_LIVE_TEST=1, so neither CI nor a developer who merely has a key exported
// ever makes a network call by accident.
//
//	NORMA_LIVE_TEST=1                 enable
//	NORMA_LIVE_API_KEY                key (falls back to OLLAMA_API_KEY)
//	NORMA_LIVE_BASE_URL               default https://ollama.com/v1
//	NORMA_LIVE_MODEL                  default mistral-large-4 (a gateway that
//	                                  enforces tool-exchange structure; many
//	                                  models accept anything and prove nothing)
func liveProvider(t *testing.T) (llm.Provider, string) {
	t.Helper()
	if os.Getenv("NORMA_LIVE_TEST") != "1" {
		t.Skip("live gateway test disabled (set NORMA_LIVE_TEST=1)")
	}
	key := os.Getenv("NORMA_LIVE_API_KEY")
	if key == "" {
		key = os.Getenv("OLLAMA_API_KEY")
	}
	if key == "" {
		t.Skip("NORMA_LIVE_TEST=1 but no NORMA_LIVE_API_KEY / OLLAMA_API_KEY")
	}
	base := os.Getenv("NORMA_LIVE_BASE_URL")
	if base == "" {
		base = "https://ollama.com/v1"
	}
	model := os.Getenv("NORMA_LIVE_MODEL")
	if model == "" {
		model = "mistral-large-4"
	}
	p, err := llm.NewProvider(llm.Config{Format: llm.FormatOpenAI, BaseURL: base, APIKey: key, Model: model})
	if err != nil {
		t.Fatal(err)
	}
	return p, model
}

func sendLive(p llm.Provider, msgs []llm.Message) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	msgs = append(append([]llm.Message(nil), msgs...),
		llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.TextBlock("continue")}})
	_, _, _, err := p.Complete(ctx, llm.CompletionRequest{MaxTokens: 16, Messages: msgs})
	return err
}

// A rendered summary between a parallel tool_use message and its results is
// normalised into a history the gateway accepts.
func TestLiveGatewayAcceptsNormalizedExchange(t *testing.T) {
	p, model := liveProvider(t)
	bad := []llm.Message{
		{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.TextBlock("task")}},
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
			{Type: llm.BlockToolUse, ID: "a", Name: "Read", Input: []byte(`{}`)},
			{Type: llm.BlockToolUse, ID: "b", Name: "Read", Input: []byte(`{}`)}}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.TextBlock("[compressed summary]")}}, // the interloper
		{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.ToolResultText("a", "out a", false), llm.ToolResultText("b", "out b", false)}},
	}
	// Informational: a strict gateway rejects the raw shape; a lenient one does not.
	t.Logf("%s on the un-normalised history: %v", model, sendLive(p, bad))

	if err := sendLive(p, llm.NormalizeToolExchanges(bad)); err != nil {
		t.Fatalf("%s rejected the normalised history: %v", model, err)
	}
}

// End to end: compress arbitrary blocks of a tool-heavy history, rebuild the
// request window, and send it.
func TestLiveGatewayAcceptsRebuiltWindow(t *testing.T) {
	p, model := liveProvider(t)
	hist := exchanges(12)
	cores, sc := Project(hist)
	for _, span := range [][2]int{{3, 3}, {4, 9}, {1, 14}, {8, 8}} {
		var ids []string
		for _, c := range cores[span[0] : span[1]+1] {
			ids = append(ids, c.ID)
		}
		st := noa.CreateInitialState("s", t.TempDir())
		st.Blocks = []noa.CompressionBlock{{
			BlockID: "b1", Tier: 1, Summary: "earlier work summarised", Active: true,
			DirectMessageIDs: ids, EffectiveMessageIDs: ids, StartRef: "m1", EndRef: "m2",
		}}
		pruned, _ := noa.RunPruneOnly(append([]noa.CoreMessage(nil), cores...), st, noa.DefaultConfig(200000))
		out := Reassemble(pruned, hist, sc, ReassembleOptions{})
		if err := sendLive(p, out); err != nil {
			t.Errorf("%s rejected the window for cores[%d..%d]: %v", model, span[0], span[1], err)
		}
	}
}
