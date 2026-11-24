package permission

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func readReq(tool, path string, wd string, mode Mode, confine bool, ask CanUseTool) Request {
	in, _ := json.Marshal(map[string]string{"file_path": path, "path": path})
	return Request{
		ToolName: tool, Input: in, IsReadOnly: true,
		ToolDecision: Allowed(),
		Ctx:          Context{Mode: mode, WorkingDir: wd, ConfineReads: confine},
		Ask:          ask,
	}
}

func TestReadSensitivePathsNeedApproval(t *testing.T) {
	wd := t.TempDir()
	home := t.TempDir()
	for _, p := range []string{
		"/proc/self/environ", "/proc/1/environ", "/etc/shadow",
		filepath.Join(home, ".ssh", "id_rsa"), filepath.Join(home, ".aws", "credentials"),
		filepath.Join(home, ".config", "gcloud", "x.json"), filepath.Join(home, ".docker", "config.json"),
		filepath.Join(wd, ".env"), filepath.Join(wd, ".env.production"), filepath.Join(wd, ".netrc"),
	} {
		asked := false
		ask := func(context.Context, string, json.RawMessage, Context) Decision { asked = true; return Denied("no") }
		d := Evaluate(context.Background(), readReq("Read", p, wd, ModeDefault, false, ask))
		if !asked || d.Behavior == Allow {
			t.Errorf("%s: want approval prompt, asked=%v behavior=%v", p, asked, d.Behavior)
		}
	}
}

func TestReadOrdinaryPathsStayAuto(t *testing.T) {
	wd := t.TempDir()
	for _, p := range []string{"main.go", ".env.example", "/usr/include/stdio.h", "/etc/hostname"} {
		d := Evaluate(context.Background(), readReq("Read", p, wd, ModeDefault, false, nil))
		if d.Behavior != Allow {
			t.Errorf("%s: want auto-allow without confinement, got %v", p, d.Behavior)
		}
	}
	// Empty Grep path searches the working directory.
	in := json.RawMessage(`{"pattern":"x"}`)
	d := Evaluate(context.Background(), Request{ToolName: "Grep", Input: in, IsReadOnly: true,
		ToolDecision: Allowed(), Ctx: Context{WorkingDir: wd, ConfineReads: true}})
	if d.Behavior != Allow {
		t.Errorf("empty-path Grep under confinement: got %v", d.Behavior)
	}
}

func TestConfineReads(t *testing.T) {
	wd := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(wd, "link")); err != nil {
		t.Skip(err)
	}
	cases := []struct {
		path   string
		inside bool
	}{
		{"a.txt", true}, {"sub/b.txt", true}, {filepath.Join(wd, "c"), true},
		{"../x", false}, {"/etc/hostname", false}, {"link/secret", false},
		{filepath.Join(outside, "f"), false},
	}
	for _, c := range cases {
		for _, tool := range []string{"Read", "Grep", "Glob"} {
			asked := false
			ask := func(context.Context, string, json.RawMessage, Context) Decision { asked = true; return Allowed() }
			Evaluate(context.Background(), readReq(tool, c.path, wd, ModeDefault, true, ask))
			if asked == c.inside {
				t.Errorf("%s %s: asked=%v, inside=%v", tool, c.path, asked, c.inside)
			}
		}
	}
}

func TestReadGuardModes(t *testing.T) {
	wd := t.TempDir()
	// Plan mode denies outright; dontAsk denies; bypass and explicit allow rule are honoured.
	if d := Evaluate(context.Background(), readReq("Read", "/proc/self/environ", wd, ModePlan, false, nil)); d.Behavior == Allow {
		t.Error("plan mode allowed /proc/self/environ")
	}
	if d := Evaluate(context.Background(), readReq("Read", "/proc/self/environ", wd, ModeDontAsk, false, nil)); d.Behavior == Allow {
		t.Error("dontAsk allowed /proc/self/environ")
	}
	if d := Evaluate(context.Background(), readReq("Read", "/proc/self/environ", wd, ModeBypass, false, nil)); d.Behavior != Allow {
		t.Error("bypass should allow")
	}
	r := readReq("Read", "/proc/self/environ", wd, ModeDefault, false, nil)
	r.Ctx.Allowed = []string{"Read"}
	if d := Evaluate(context.Background(), r); d.Behavior != Allow {
		t.Error("explicit Read allow rule should be honoured")
	}
	// Non-path tools are untouched.
	if readNeedsApproval("Bash", json.RawMessage(`{"file_path":"/proc/self/environ"}`), wd, true) {
		t.Error("Bash must not be affected")
	}
}
