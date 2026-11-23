package harness

import (
	"testing"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
)

func contextTokensInput(t *testing.T, c Compactor, nonStreaming bool) QueryInput {
	return QueryInput{
		Messages:       []llm.Message{llm.UserText("go")},
		Tools:          tool.NewRegistry(tool.NewBash()),
		PermissionMode: permission.ModeBypass,
		WorkingDir:     t.TempDir(),
		Compactor:      c,
		NonStreaming:   nonStreaming,
	}
}

// When a response reports a physical footprint (ContextTokens) that differs from
// the billed counters, the compaction gate must see the footprint, on both the
// streaming and the atomic path.
func TestCompactionGateUsesContextTokensStreaming(t *testing.T) {
	first := usageToolTurn("c1", "Bash", `{"command":"echo hi"}`, 100, 20)
	first[0].Usage.ContextTokens = 40
	m := &scriptedModel{turns: [][]llm.StreamEvent{first, usageTextTurn("done", 500, 30)}}
	rc := &recordingCompactor{}
	in := contextTokensInput(t, rc, false)
	if term := drain(t, in, QueryDeps{CallModel: m.call}); term.Reason != ReasonCompleted {
		t.Fatalf("reason=%v", term.Reason)
	}
	if len(rc.seen) != 2 || rc.seen[0] != 0 || rc.seen[1] != 40 {
		t.Fatalf("gate saw %v, want [0 40] (footprint, not billed 120)", rc.seen)
	}
}

func TestCompactionGateUsesContextTokensAtomic(t *testing.T) {
	first := syncToolTurn("c1", "Bash", `{"command":"echo hi"}`)
	first.usage = llm.Usage{InputTokens: 100, OutputTokens: 20, ContextTokens: 40}
	m := &scriptedSyncModel{turns: []syncTurn{first, syncTextTurn("done")}}
	rc := &recordingCompactor{}
	in := contextTokensInput(t, rc, true)
	if term := drain(t, in, QueryDeps{CallModelSync: m.call}); term.Reason != ReasonCompleted {
		t.Fatalf("reason=%v", term.Reason)
	}
	if len(rc.seen) != 2 || rc.seen[1] != 40 {
		t.Fatalf("gate saw %v, want second value 40", rc.seen)
	}
}

// Without ContextTokens the gate keeps using input+output (unchanged behaviour).
func TestCompactionGateWithoutContextTokensUnchanged(t *testing.T) {
	first := syncToolTurn("c1", "Bash", `{"command":"echo hi"}`)
	first.usage = llm.Usage{InputTokens: 100, OutputTokens: 20}
	m := &scriptedSyncModel{turns: []syncTurn{first, syncTextTurn("done")}}
	rc := &recordingCompactor{}
	in := contextTokensInput(t, rc, true)
	drain(t, in, QueryDeps{CallModelSync: m.call})
	if len(rc.seen) != 2 || rc.seen[1] != 120 {
		t.Fatalf("gate saw %v, want second value 120", rc.seen)
	}
}
