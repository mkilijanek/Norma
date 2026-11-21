package noaadapter

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/noa"
)

// exchanges builds a history of tool exchanges: single calls and parallel
// pairs, each followed by an assistant text, as an OpenAI-style transcript.
func exchanges(n int) []llm.Message {
	msgs := []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.TextBlock("task")}}}
	for g := range n {
		if g%3 == 2 { // parallel pair
			a, b := fmt.Sprintf("p%03da", g), fmt.Sprintf("p%03db", g)
			msgs = append(msgs,
				llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
					{Type: llm.BlockToolUse, ID: a, Name: "Read", Input: []byte(`{}`)},
					{Type: llm.BlockToolUse, ID: b, Name: "Read", Input: []byte(`{}`)}}},
				llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{
					llm.ToolResultText(a, "out a "+a, false), llm.ToolResultText(b, "out b "+b, false)}})
		} else {
			id := fmt.Sprintf("c%03d", g)
			msgs = append(msgs,
				llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: id, Name: "Read", Input: []byte(`{}`)}}},
				llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{llm.ToolResultText(id, "out "+id, false)}})
		}
		msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock(fmt.Sprintf("done %d", g))}})
	}
	return msgs
}

// providerViolations applies the rules measured in the issue: a tool_result must
// follow the message declaring it, nothing may sit between them, and ids are unique.
func providerViolations(msgs []llm.Message) []string {
	var v []string
	useAt := map[string]int{}
	resAt := map[string]int{}
	for i, m := range msgs {
		for _, b := range m.Content {
			switch b.Type {
			case llm.BlockToolUse:
				if _, d := useAt[b.ID]; d {
					v = append(v, "duplicate tool_use "+b.ID)
				}
				useAt[b.ID] = i
			case llm.BlockToolResult:
				if _, d := resAt[b.ToolUseID]; d {
					v = append(v, "duplicate tool_result "+b.ToolUseID)
				}
				resAt[b.ToolUseID] = i
			}
		}
	}
	for id, r := range resAt {
		u, ok := useAt[id]
		switch {
		case !ok:
			v = append(v, "orphan tool_result "+id)
		case r < u:
			v = append(v, "tool_result before tool_use "+id)
		case r != u+1:
			v = append(v, fmt.Sprintf("message(s) between tool_use and tool_result %s (%d..%d)", id, u, r))
		}
	}
	for id := range useAt {
		if _, ok := resAt[id]; !ok {
			v = append(v, "tool_use without result "+id)
		}
	}
	return v
}

func TestReassembledWindowIsValidForArbitraryBlockCoverage(t *testing.T) {
	hist := exchanges(40)
	cores, sc := Project(hist)
	rng := rand.New(rand.NewSource(7))
	for trial := range 1000 {
		st := noa.CreateInitialState("s", t.TempDir())
		for b := range 1 + rng.Intn(6) {
			lo := rng.Intn(len(cores))
			hi := min(lo+rng.Intn(30), len(cores)-1)
			var ids []string
			for _, c := range cores[lo : hi+1] {
				ids = append(ids, c.ID)
			}
			st.Blocks = append(st.Blocks, noa.CompressionBlock{
				BlockID: fmt.Sprintf("b%d", b+1), Tier: 1, Summary: "s", Active: true,
				DirectMessageIDs: ids, EffectiveMessageIDs: ids, StartRef: "m1", EndRef: "m2",
			})
		}
		pruned, _ := noa.RunPruneOnly(append([]noa.CoreMessage(nil), cores...), st, noa.DefaultConfig(200000))
		out := Reassemble(pruned, hist, sc, ReassembleOptions{})
		if v := providerViolations(out); len(v) > 0 {
			t.Fatalf("trial %d: invalid window: %v", trial, v)
		}
	}
}
