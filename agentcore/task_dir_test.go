//go:build !windows

package agentcore

import (
	"context"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/tool"
)

func taskDirOptions(dir string, mode os.FileMode, id string) Options {
	return Options{
		SystemPrompt:   []string{"go"},
		Tools:          []tool.CoreTool{tool.NewBash()},
		PermissionMode: permission.ModeBypass,
		SessionID:      id,
		TaskDir:        dir,
		TaskDirMode:    mode,
	}
}

func makeBase(t *testing.T, mode os.FileMode) string {
	t.Helper()
	base := filepath.Join(t.TempDir(), "base")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(base, mode); err != nil {
		t.Fatal(err)
	}
	return base
}

// TaskDir and TaskDirMode reach the task manager: the session's tasks live
// under the host directory with the host's mode, and Close removes them.
func TestTaskDirOptionReachesTaskManager(t *testing.T) {
	base := makeBase(t, 0o755)
	sess, err := NewSessionE(taskDirOptions(base, 0o711, "td-ok"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sess.registry.Get("TaskOutput"); !ok {
		t.Fatal("task tools must be registered")
	}
	tasks := filepath.Join(base, "td-ok", "tasks")
	fi, err := os.Stat(tasks)
	if err != nil {
		t.Fatalf("task dir not under the host directory: %v", err)
	}
	if fi.Mode().Perm() != 0o711 {
		t.Fatalf("tasks mode = %v, want 0711", fi.Mode().Perm())
	}
	if bi, _ := os.Stat(base); bi.Mode().Perm() != 0o755 {
		t.Fatalf("base mode changed to %v", bi.Mode().Perm())
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, "td-ok")); !os.IsNotExist(err) {
		t.Fatalf("session dir should be removed by Close, stat err = %v", err)
	}
}

// An unusable host directory is an error the host sees at construction.
func TestNewSessionEReturnsTaskDirError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	sess, err := NewSessionE(taskDirOptions(missing, 0o711, "td-bad"))
	if err == nil {
		sess.Close()
		t.Fatal("expected a construction error for an unusable TaskDir")
	}
	if sess != nil {
		t.Fatal("no session may be returned alongside an error")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error should name the directory, got: %v", err)
	}
}

// NewSession cannot return an error; with an unusable TaskDir the session is
// inert and the first Prompt reports the failure instead of running without
// background tasks.
func TestNewSessionTaskDirErrorSurfacesOnPrompt(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	opts := taskDirOptions(missing, 0o711, "td-bad2")
	opts.Deps = harness.QueryDeps{CallModel: func(context.Context, llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
		t.Error("the model must not be called when the task directory is unusable")
		return nil
	}}
	sess := NewSession(opts)
	defer sess.Close()
	var got error
	for _, err := range sess.Prompt(context.Background(), "hello") {
		if err != nil {
			got = err
			break
		}
	}
	if got == nil || !strings.Contains(got.Error(), missing) {
		t.Fatalf("Prompt should fail naming the directory, got: %v", got)
	}
}

// Without the option nothing changes: NewSessionE succeeds and the manager is
// in the per-user default location.
func TestNewSessionEDefaultTaskDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	sess, err := NewSessionE(Options{SystemPrompt: []string{"go"}, SessionID: "td-default"})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	m, _ := filepath.Glob(filepath.Join(tmp, "norma*", "td-default", "tasks"))
	if len(m) != 1 {
		t.Fatalf("default task dir not found under %s: %v", tmp, m)
	}
	if fi, _ := os.Stat(filepath.Dir(filepath.Dir(m[0]))); fi.Mode().Perm() != 0o700 {
		t.Fatalf("default base mode = %v, want 0700", fi.Mode().Perm())
	}
}

// TaskDirMode without TaskDir would be silently ignored; it is an error.
func TestNewSessionETaskDirModeWithoutDir(t *testing.T) {
	sess, err := NewSessionE(Options{SystemPrompt: []string{"go"}, SessionID: "td-nodir", TaskDirMode: 0o711})
	if err == nil {
		sess.Close()
		t.Fatal("TaskDirMode without TaskDir must be an error")
	}
}
