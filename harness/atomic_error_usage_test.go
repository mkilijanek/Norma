package harness

import (
	"context"
	"errors"
	"testing"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
)

// A failed atomic call may still report billed usage; the loop must keep it.
func TestAtomicErrorUsageIsRetained(t *testing.T) {
	call := func(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
		return llm.Message{}, "", llm.Usage{InputTokens: 11, OutputTokens: 2}, errors.New("invalid api key")
	}
	in := QueryInput{
		Messages:       []llm.Message{llm.UserText("go")},
		PermissionMode: permission.ModeBypass,
		WorkingDir:     t.TempDir(),
		NonStreaming:   true,
	}
	var usage llm.Usage
	for ev := range Query(context.Background(), in, QueryDeps{CallModelSync: call}) {
		if ev.Kind == KindResult && ev.Terminal != nil {
			usage = ev.Terminal.Usage
		}
	}
	if usage.InputTokens != 11 || usage.OutputTokens != 2 {
		t.Fatalf("failed-call usage lost: %+v", usage)
	}
}
