package harness

import (
	"context"
	"iter"
	"reflect"
	"testing"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
)

// QueryInput.CacheBreakpoints must reach every model request, as a copy so a
// provider cannot mutate the host's slice, on the streaming and atomic paths.
func TestCacheBreakpointsReachModelRequest(t *testing.T) {
	hint := []int{1, 2}
	in := QueryInput{
		Messages:         []llm.Message{llm.UserText("go")},
		System:           []string{"a", "b", "c"},
		DynamicBoundary:  3,
		CacheBreakpoints: hint,
		PermissionMode:   permission.ModeBypass,
		WorkingDir:       t.TempDir(),
	}

	var streamed []int
	stream := func(_ context.Context, r llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
		streamed = append([]int(nil), r.CacheBreakpoints...)
		if len(r.CacheBreakpoints) > 0 {
			r.CacheBreakpoints[0] = 99 // must not leak into the host's slice
		}
		return (&scriptedModel{turns: [][]llm.StreamEvent{textTurn("done")}}).call(context.Background(), r)
	}
	drain(t, in, QueryDeps{CallModel: stream})
	if !reflect.DeepEqual(streamed, []int{1, 2}) {
		t.Fatalf("streaming request hint=%v", streamed)
	}
	if !reflect.DeepEqual(hint, []int{1, 2}) {
		t.Fatalf("host slice mutated: %v", hint)
	}

	var atomic []int
	in.NonStreaming = true
	sync := func(_ context.Context, r llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
		atomic = r.CacheBreakpoints
		return syncTextTurn("done").msg, "end_turn", llm.Usage{}, nil
	}
	drain(t, in, QueryDeps{CallModelSync: sync})
	if !reflect.DeepEqual(atomic, []int{1, 2}) {
		t.Fatalf("atomic request hint=%v", atomic)
	}
}

// No hint, no field: requests keep the legacy shape.
func TestNoCacheBreakpointsLeavesRequestEmpty(t *testing.T) {
	var got []int
	call := func(_ context.Context, r llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
		got = r.CacheBreakpoints
		return (&scriptedModel{turns: [][]llm.StreamEvent{textTurn("done")}}).call(context.Background(), r)
	}
	drain(t, QueryInput{Messages: []llm.Message{llm.UserText("go")}, WorkingDir: t.TempDir()}, QueryDeps{CallModel: call})
	if len(got) != 0 {
		t.Fatalf("hint=%v", got)
	}
}
