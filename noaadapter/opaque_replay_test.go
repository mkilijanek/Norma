package noaadapter

import (
	"reflect"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

func TestOpaqueThinkingSurvivesProjectionAndView(t *testing.T) {
	blocks := []llm.ContentBlock{{Type: llm.BlockThinking, Thinking: "", Signature: "signed-empty"}, {Type: llm.BlockRedactedThinking, Data: "opaque-redacted"}, llm.TextBlock("answer")}
	msgs := []llm.Message{{Role: llm.RoleAssistant, Content: blocks}}
	s := simSession(t, 40000)
	view := s.View(msgs)
	if !reflect.DeepEqual(view, msgs) {
		t.Fatalf("opaque replay changed: %#v", view)
	}
	if !reflect.DeepEqual(msgs[0].Content, blocks) {
		t.Fatal("original history changed")
	}
}

func TestSignedThinkingCannotBeRewrittenByView(t *testing.T) {
	original := llm.ContentBlock{Type: llm.BlockThinking, Thinking: "signed text", Signature: "signature"}
	cores, sc := Project([]llm.Message{{Role: llm.RoleAssistant, Content: []llm.ContentBlock{original}}})
	cores[0].Text = "rewritten"
	got := Reassemble(cores, []llm.Message{{Role: llm.RoleAssistant, Content: []llm.ContentBlock{original}}}, sc, ReassembleOptions{})
	if len(got) != 1 || !reflect.DeepEqual(got[0].Content[0], original) {
		t.Fatalf("signed block changed: %+v", got)
	}
}
