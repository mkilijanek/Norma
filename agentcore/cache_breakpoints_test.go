package agentcore

import (
	"context"
	"iter"
	"reflect"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

type breakpointProvider struct{ seen [][]int }

func (p *breakpointProvider) Stream(_ context.Context, r llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	p.seen = append(p.seen, r.CacheBreakpoints)
	return func(yield func(llm.StreamEvent, error) bool) {
		for _, ev := range []llm.StreamEvent{
			{Type: llm.SETextDelta, Text: "ok"},
			{Type: llm.SEMessageDelta, StopReason: "end_turn"},
			{Type: llm.SEMessageStop},
		} {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

func (p *breakpointProvider) Complete(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	return llm.Message{}, "", llm.Usage{}, nil
}

// Options.SystemCacheBreakpoints reaches the provider request unchanged, and is
// absent when unset.
func TestSystemCacheBreakpointsReachProvider(t *testing.T) {
	for _, tc := range []struct {
		name string
		opt  []int
		want []int
	}{
		{"set", []int{1, 2}, []int{1, 2}},
		{"unset", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &breakpointProvider{}
			s := NewSession(Options{
				Provider:               p,
				SystemPrompt:           []string{"a", "b", "c"},
				DynamicBoundary:        3,
				SystemCacheBreakpoints: tc.opt,
				WorkingDir:             t.TempDir(),
			})
			defer s.Close()
			for _, err := range s.Prompt(context.Background(), "hi") {
				if err != nil {
					t.Fatal(err)
				}
			}
			if len(p.seen) != 1 || !reflect.DeepEqual(p.seen[0], tc.want) && len(p.seen[0])+len(tc.want) != 0 {
				t.Fatalf("provider saw %v, want [%v]", p.seen, tc.want)
			}
		})
	}
}
