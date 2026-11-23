package plan

import (
	"context"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
)

func TestControllerTransitions(t *testing.T) {
	c := NewController(permission.ModeDefault, permission.ModeAcceptEdits)
	if c.InPlan() {
		t.Fatal("should not start in plan")
	}
	c.Enter()
	if c.Mode() != permission.ModePlan {
		t.Fatalf("mode=%v", c.Mode())
	}
	c.approve()
	if c.Mode() != permission.ModeAcceptEdits {
		t.Fatalf("after approve mode=%v", c.Mode())
	}
}

func TestExitToolApprove(t *testing.T) {
	c := NewController(permission.ModePlan, permission.ModeAcceptEdits)
	exit := ExitTool(c, func(_ context.Context, _ string) (bool, string) { return true, "" })
	res, _ := exit.Call(context.Background(), []byte(`{"plan":"step 1"}`), &tool.ToolContext{})
	if res.IsError || !strings.Contains(res.Flatten(), "approved") {
		t.Fatalf("approve result: %q (err=%v)", res.Flatten(), res.IsError)
	}
	if c.Mode() != permission.ModeAcceptEdits {
		t.Fatalf("mode after approve=%v", c.Mode())
	}
}

func TestExitToolReject(t *testing.T) {
	c := NewController(permission.ModePlan, permission.ModeAcceptEdits)
	exit := ExitTool(c, func(_ context.Context, _ string) (bool, string) { return false, "needs tests" })
	res, _ := exit.Call(context.Background(), []byte(`{"plan":"x"}`), &tool.ToolContext{})
	if !res.IsError || !strings.Contains(res.Flatten(), "needs tests") {
		t.Fatalf("reject result: %q", res.Flatten())
	}
	if c.Mode() != permission.ModePlan {
		t.Fatalf("should stay in plan, mode=%v", c.Mode())
	}
}

// With no approver the model must not be able to leave plan mode on its own.
func TestExitToolNilApproverFailsClosed(t *testing.T) {
	c := NewController(permission.ModeDontAsk, permission.ModeAcceptEdits)
	c.Enter()
	exit := ExitTool(c, nil)
	res, _ := exit.Call(context.Background(), []byte(`{"plan":"x"}`), &tool.ToolContext{})
	if !res.IsError || !strings.Contains(res.Flatten(), "no plan approver") {
		t.Fatalf("nil approver must refuse: %q", res.Flatten())
	}
	if c.Mode() != permission.ModePlan {
		t.Fatalf("must stay in plan mode, got %v", c.Mode())
	}
}

func TestAutoApproveIsExplicitOptIn(t *testing.T) {
	c := NewController(permission.ModePlan, permission.ModeAcceptEdits)
	res, _ := ExitTool(c, AutoApprove).Call(context.Background(), []byte(`{"plan":"x"}`), &tool.ToolContext{})
	if res.IsError || c.Mode() != permission.ModeAcceptEdits {
		t.Fatalf("AutoApprove should exit: %q mode=%v", res.Flatten(), c.Mode())
	}
}
