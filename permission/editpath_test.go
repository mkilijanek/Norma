package permission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func editInput(t *testing.T, path string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]string{"file_path": path})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEditPathAllowedPlainPaths(t *testing.T) {
	wd := t.TempDir()
	if err := os.WriteFile(filepath.Join(wd, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a.txt", "new.txt", "sub/new.txt"} {
		if !editPathAllowed(editInput(t, p), wd) {
			t.Errorf("%s inside the working directory must be allowed", p)
		}
	}
	for _, p := range []string{"../outside.txt", ".git/config"} {
		if editPathAllowed(editInput(t, p), wd) {
			t.Errorf("%s must not be auto-allowed", p)
		}
	}
}

func TestEditPathDanglingSymlinkNotAutoAllowed(t *testing.T) {
	wd := t.TempDir()
	outside := t.TempDir()
	// The link points outside the working directory at a file that does not exist yet.
	if err := os.Symlink(filepath.Join(outside, "target.txt"), filepath.Join(wd, "link")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if editPathAllowed(editInput(t, "link"), wd) {
		t.Error("a dangling symlink leaving the working directory was auto-allowed")
	}
	// A dangling link that stays inside is still unresolvable, so it goes to approval too.
	if err := os.Symlink(filepath.Join(wd, "later.txt"), filepath.Join(wd, "inlink")); err != nil {
		t.Fatal(err)
	}
	if editPathAllowed(editInput(t, "inlink"), wd) {
		t.Error("a dangling symlink was auto-allowed")
	}
	// The same through a directory component that is a dangling link.
	if err := os.Symlink(filepath.Join(outside, "nodir"), filepath.Join(wd, "dlink")); err != nil {
		t.Fatal(err)
	}
	if editPathAllowed(editInput(t, "dlink/file.txt"), wd) {
		t.Error("a path under a dangling symlink directory was auto-allowed")
	}
}

func TestEditPathSymlinkLoopNotAutoAllowed(t *testing.T) {
	wd := t.TempDir()
	if err := os.Symlink(filepath.Join(wd, "b"), filepath.Join(wd, "a")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if err := os.Symlink(filepath.Join(wd, "a"), filepath.Join(wd, "b")); err != nil {
		t.Fatal(err)
	}
	if editPathAllowed(editInput(t, "a"), wd) {
		t.Error("a symlink loop was auto-allowed")
	}
}
