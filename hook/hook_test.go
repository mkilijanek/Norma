package hook

import (
	"context"
	"testing"
)

func TestPreToolUseBlockAndRewrite(t *testing.T) {
	r := NewRegistry()
	r.On(PreToolUse, func(_ context.Context, ev Event) Result {
		if ev.ToolName == "Bash" {
			return Result{Decision: "block", Message: "no bash"}
		}
		return Result{UpdatedInput: []byte(`{"x":1}`)}
	})
	if block, msg, _ := r.PreToolUse(context.Background(), "Bash", nil); !block || msg != "no bash" {
		t.Fatalf("expected block, got %v %q", block, msg)
	}
	if block, _, upd := r.PreToolUse(context.Background(), "Read", nil); block || string(upd) != `{"x":1}` {
		t.Fatalf("expected rewrite, got block=%v upd=%s", block, upd)
	}
}

func TestStopHookBlockingContinues(t *testing.T) {
	r := NewRegistry()
	calls := 0
	r.On(Stop, func(_ context.Context, _ Event) Result {
		calls++
		if calls == 1 {
			return Result{Decision: "block", AdditionalContext: "keep going"}
		}
		return Result{}
	})
	// First call: a "block" decision → continue with an injected message.
	prevent, blocking, _ := r.Stop(context.Background(), nil)
	if prevent || len(blocking) != 1 || blocking[0] != "keep going" {
		t.Fatalf("blocking path wrong: prevent=%v blocking=%v", prevent, blocking)
	}
	// Second call: no decision → allow stop.
	if prevent, blocking, _ := r.Stop(context.Background(), nil); prevent || len(blocking) != 0 {
		t.Fatalf("second Stop should allow: prevent=%v blocking=%v", prevent, blocking)
	}
}

func TestStopHookPreventTerminates(t *testing.T) {
	r := NewRegistry()
	r.On(Stop, func(_ context.Context, _ Event) Result {
		return Result{PreventContinuation: true, Message: "halt"}
	})
	prevent, _, msg := r.Stop(context.Background(), nil)
	if !prevent || msg != "halt" {
		t.Fatalf("prevent path wrong: prevent=%v msg=%q", prevent, msg)
	}
}

func TestPostToolUseFiresFailureHooksOnlyOnError(t *testing.T) {
	r := NewRegistry()
	var post, failure []Event
	r.On(PostToolUse, func(_ context.Context, ev Event) Result { post = append(post, ev); return Result{} })
	r.On(PostToolUseFailure, func(_ context.Context, ev Event) Result { failure = append(failure, ev); return Result{} })

	r.PostToolUse(context.Background(), "Read", []byte(`{"p":1}`), []byte("ok"), false)
	if len(post) != 1 || len(failure) != 0 {
		t.Fatalf("success: post=%d failure=%d, want 1 and 0", len(post), len(failure))
	}
	r.PostToolUse(context.Background(), "Bash", []byte(`{"c":"x"}`), []byte("boom"), true)
	if len(post) != 2 || len(failure) != 1 {
		t.Fatalf("error: post=%d failure=%d, want 2 and 1", len(post), len(failure))
	}
	ev := failure[0]
	if ev.Type != PostToolUseFailure || ev.ToolName != "Bash" || !ev.IsError ||
		string(ev.Input) != `{"c":"x"}` || string(ev.Result) != "boom" {
		t.Fatalf("failure event payload wrong: %+v", ev)
	}
}

func TestFireUserPromptSubmitJoinsNonEmptyContexts(t *testing.T) {
	r := NewRegistry()
	var gotPrompt string
	r.On(UserPromptSubmit, func(_ context.Context, ev Event) Result {
		gotPrompt = ev.Prompt
		return Result{AdditionalContext: "first"}
	})
	r.On(UserPromptSubmit, func(context.Context, Event) Result { return Result{} }) // contributes nothing
	r.On(UserPromptSubmit, func(context.Context, Event) Result { return Result{AdditionalContext: "second"} })

	if got := r.FireUserPromptSubmit(context.Background(), "hello"); got != "first\nsecond\n" {
		t.Fatalf("context = %q, want %q", got, "first\nsecond\n")
	}
	if gotPrompt != "hello" {
		t.Fatalf("hook saw prompt %q, want %q", gotPrompt, "hello")
	}
	if got := NewRegistry().FireUserPromptSubmit(context.Background(), "x"); got != "" {
		t.Fatalf("no hooks registered: context = %q, want empty", got)
	}
}

func TestSessionAndSubagentNotifications(t *testing.T) {
	r := NewRegistry()
	var seen []Event
	rec := func(_ context.Context, ev Event) Result { seen = append(seen, ev); return Result{} }
	r.On(SessionStart, rec).On(SessionEnd, rec).On(SubagentStart, rec)

	r.FireSessionStart(context.Background())
	r.FireSubagentStart(context.Background(), "researcher")
	r.FireSessionEnd(context.Background())

	want := []EventType{SessionStart, SubagentStart, SessionEnd}
	if len(seen) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(seen), len(want), seen)
	}
	for i, w := range want {
		if seen[i].Type != w {
			t.Errorf("event %d type = %s, want %s", i, seen[i].Type, w)
		}
	}
	if seen[1].AgentType != "researcher" {
		t.Errorf("SubagentStart AgentType = %q, want %q", seen[1].AgentType, "researcher")
	}
}

func TestPreToolUseLastUpdatedInputWinsAndBlockShortCircuits(t *testing.T) {
	r := NewRegistry()
	r.On(PreToolUse, func(context.Context, Event) Result { return Result{UpdatedInput: []byte(`{"v":1}`)} })
	r.On(PreToolUse, func(context.Context, Event) Result { return Result{UpdatedInput: []byte(`{"v":2}`)} })
	if _, _, upd := r.PreToolUse(context.Background(), "T", nil); string(upd) != `{"v":2}` {
		t.Fatalf("updated input = %s, want the last non-empty one", upd)
	}
	r.On(PreToolUse, func(context.Context, Event) Result { return Result{Decision: "block", Message: "stop"} })
	if block, msg, upd := r.PreToolUse(context.Background(), "T", nil); !block || msg != "stop" || upd != nil {
		t.Fatalf("block = %v msg = %q upd = %s; a block must discard rewrites", block, msg, upd)
	}
}
