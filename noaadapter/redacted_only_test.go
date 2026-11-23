package noaadapter

import (
	"github.com/Autumn-27/norma/llm"
	"testing"
)

func TestDropEmptyRetainsRedactedOnlyAssistant(t *testing.T) {
	input := []llm.Message{{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockRedactedThinking, Data: "opaque"}}}}
	out := dropEmpty(input)
	if len(out) != 1 || out[0].Content[0].Data != "opaque" {
		t.Fatal("redacted-only history lost")
	}
}
