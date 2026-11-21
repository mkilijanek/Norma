//go:build !windows

package tool

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSecureDirRejectsSymlinkAndFixesPerms(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := secureDir(link); err == nil {
		t.Fatal("symlinked base dir must be rejected")
	}
	if err := secureDir(real); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(real); fi.Mode().Perm() != 0o700 {
		t.Fatalf("perms not tightened: %v", fi.Mode().Perm())
	}
}

func TestSpawnOutputNotWrittenThroughSymlink(t *testing.T) {
	m, err := NewManager("symlink-test")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Cleanup()
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Plant a symlink where the next task's output file will go.
	if err := os.Symlink(victim, filepath.Join(m.dir, "task_1.output")); err != nil {
		t.Fatal(err)
	}
	task, err := m.Spawn(SpawnSpec{Command: "echo pwned", Kind: "bash"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	m.Wait(context.Background(), task.ID, 10*time.Second)
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatalf("symlink target overwritten: %q", b)
	}
	if fi, _ := os.Stat(m.dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("task dir perms %v", fi.Mode().Perm())
	}
}
