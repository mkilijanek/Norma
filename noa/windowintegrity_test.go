package noa

import (
	"fmt"
	"math/rand"
	"testing"
)

// toolGroups builds n groups of assistant(tool_use) + tool_result + assistant(text),
// preceded by the task-defining user message.
func toolGroups(n int) []CoreMessage {
	msgs := []CoreMessage{msg("u0", RoleUser, CTText, "task")}
	for g := range n {
		cid := fmt.Sprintf("call%03d", g)
		msgs = append(msgs,
			CoreMessage{ID: fmt.Sprintf("g%03dc", g), Role: RoleAssistant, ContentType: CTToolCall, ToolCallID: cid, ToolName: "Read", Text: "{}"},
			CoreMessage{ID: fmt.Sprintf("g%03dr", g), Role: RoleTool, ContentType: CTToolResult, ToolCallID: cid, ToolName: "Read", Text: "out"},
			CoreMessage{ID: fmt.Sprintf("g%03dt", g), Role: RoleAssistant, ContentType: CTText, Text: "done"},
		)
	}
	return msgs
}

// windowViolations lists every way a window is invalid for a strict provider.
func windowViolations(msgs []CoreMessage) []string {
	var v []string
	callAt := map[string]int{}
	resAt := map[string]int{}
	for i, m := range msgs {
		switch m.ContentType {
		case CTToolCall:
			if m.ToolCallID == "" {
				continue
			}
			if _, dup := callAt[m.ToolCallID]; dup {
				v = append(v, "duplicate tool_use "+m.ToolCallID)
			}
			callAt[m.ToolCallID] = i
		case CTToolResult:
			if m.ToolCallID == "" {
				continue
			}
			if _, dup := resAt[m.ToolCallID]; dup {
				v = append(v, "duplicate tool_result "+m.ToolCallID)
			}
			resAt[m.ToolCallID] = i
		}
	}
	for id, r := range resAt {
		c, ok := callAt[id]
		switch {
		case !ok:
			v = append(v, "orphan tool_result "+id)
		case r < c:
			v = append(v, "tool_result before its tool_use "+id)
		default:
			for j := c + 1; j < r; j++ {
				if msgs[j].ContentType != CTToolCall && msgs[j].ContentType != CTToolResult {
					v = append(v, "message between tool_use and its tool_result "+id)
					break
				}
			}
		}
	}
	return v
}

// Blocks that overlap, cut through a group, or leave gaps (the shapes in the
// issue) must still produce a valid window.
func TestPruneWindowIsValidForArbitraryBlockCoverage(t *testing.T) {
	msgs := toolGroups(60)
	rng := rand.New(rand.NewSource(1))
	for trial := range 500 {
		st := CreateInitialState("s", "/tmp")
		for b := range 1 + rng.Intn(6) {
			lo := rng.Intn(len(msgs))
			hi := min(lo+rng.Intn(40), len(msgs)-1)
			var covered []string
			for _, m := range msgs[lo : hi+1] {
				covered = append(covered, m.ID)
			}
			blk := block(fmt.Sprintf("b%d", b+1), 1, covered[0], covered[len(covered)-1])
			blk.DirectMessageIDs, blk.EffectiveMessageIDs = covered, covered
			st.Blocks = append(st.Blocks, blk)
		}
		got := pruneWith(append([]CoreMessage(nil), msgs...), st)
		if v := windowViolations(got); len(v) > 0 {
			t.Fatalf("trial %d: invalid window: %v\nblocks=%d", trial, v, len(st.Blocks))
		}
	}
}
