//go:build !windows

package noaadapter

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/Autumn-27/norma/noa"
)

func dirModeOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// Default: tier directories stay 0700 and archive files 0600, as before.
func TestArchiveTierDirDefaultMode(t *testing.T) {
	base := t.TempDir()
	sess, err := newSession(Options{ArchiveBaseDir: base, SessionID: "s"})
	if err != nil {
		t.Fatal(err)
	}
	abs, _, err := sess.archiver.Write(1, "b1", "m00001", "m00002", []byte("body"))
	if err != nil {
		t.Fatal(err)
	}
	if got := dirModeOf(t, filepath.Dir(abs)); got != 0o700 {
		t.Fatalf("tier dir mode = %v, want 0700", got)
	}
	if got := dirModeOf(t, abs); got != 0o600 {
		t.Fatalf("archive file mode = %v, want 0600", got)
	}
}

// ArchiveDirMode sets the tier directory mode, exactly (the umask does not
// narrow it), and leaves archive files at 0600.
func TestArchiveTierDirHonoursOptionMode(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	base := t.TempDir()
	sess, err := newSession(Options{ArchiveBaseDir: base, SessionID: "s", ArchiveDirMode: 0o711})
	if err != nil {
		t.Fatal(err)
	}
	for _, tier := range []noa.Tier{1, 2} {
		abs, _, err := sess.archiver.Write(tier, "b"+noa.ArchiveTierDir(tier), "m00001", "m00002", []byte("body"))
		if err != nil {
			t.Fatal(err)
		}
		if got := dirModeOf(t, filepath.Dir(abs)); got != 0o711 {
			t.Fatalf("tier %d dir mode = %v, want 0711", tier, got)
		}
		if got := dirModeOf(t, abs); got != 0o600 {
			t.Fatalf("tier %d archive file mode = %v, want 0600", tier, got)
		}
	}
}

// An existing tier directory is brought to the requested mode on the next
// write, so a host that changes the option does not keep stale modes.
func TestArchiveTierDirModeAppliesToExistingDir(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "s")
	tierDir := filepath.Join(root, noa.ArchiveTierDir(1))
	if err := os.MkdirAll(tierDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sess, err := newSession(Options{ArchiveBaseDir: base, SessionID: "s", ArchiveDirMode: 0o711})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := sess.archiver.Write(1, "b1", "m00001", "m00002", []byte("body")); err != nil {
		t.Fatal(err)
	}
	if got := dirModeOf(t, tierDir); got != 0o711 {
		t.Fatalf("tier dir mode = %v, want 0711", got)
	}
}

// Archives adopted or regenerated while rebuilding state use the same mode.
func TestReuseArchiverHonoursDirMode(t *testing.T) {
	root := filepath.Join(t.TempDir(), "s")
	a := newReuseArchiver(root, 0o711)
	abs, _, err := a.Write(1, "b1", "m00001", "m00002", []byte("body"))
	if err != nil {
		t.Fatal(err)
	}
	if got := dirModeOf(t, filepath.Dir(abs)); got != 0o711 {
		t.Fatalf("tier dir mode = %v, want 0711", got)
	}
}

func TestArchiveDirModeValidation(t *testing.T) {
	for name, mode := range map[string]os.FileMode{
		"owner cannot write": 0o511,
		"bits beyond perm":   os.ModeSetgid | 0o755,
		"directory bit":      os.ModeDir | 0o755,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := newSession(Options{ArchiveBaseDir: t.TempDir(), SessionID: "s", ArchiveDirMode: mode}); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

// A tier directory replaced by a symlink is refused when a host mode is set,
// and the symlink target is not chmod-ed.
func TestArchiveTierDirModeRefusesSymlink(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "s")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, noa.ArchiveTierDir(1))); err != nil {
		t.Fatal(err)
	}
	sess, err := newSession(Options{ArchiveBaseDir: base, SessionID: "s", ArchiveDirMode: 0o711})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := sess.archiver.Write(1, "b1", "m00001", "m00002", []byte("body")); err == nil {
		t.Fatal("write through a symlinked tier dir succeeded, want refusal")
	}
	if got := dirModeOf(t, target); got != 0o700 {
		t.Fatalf("symlink target mode = %v, want 0700 (untouched)", got)
	}
}
