//go:build !windows

package tool

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// hostBase returns a fresh base directory with the given mode, the way a host
// that supplies ManagerOptions.Dir is expected to prepare it.
func hostBase(t *testing.T, mode os.FileMode) string {
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

func modeOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// The default (no host directory) stays exactly as in v0.7.0: a per-user base
// under the temp dir, forced to 0700, with 0700 below it.
func TestDefaultTaskDirUnchanged(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	m, err := NewManager("default-dir")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Cleanup()
	base := filepath.Join(tmp, taskBaseName())
	if want := filepath.Join(base, "default-dir", "tasks"); m.dir != want {
		t.Fatalf("task dir = %q, want %q", m.dir, want)
	}
	for _, d := range []string{base, filepath.Dir(m.dir), m.dir} {
		if got := modeOf(t, d); got != 0o700 {
			t.Fatalf("%s mode = %v, want 0700", d, got)
		}
	}
}

// The same holds for the options form with nothing set.
func TestZeroManagerOptionsIsDefault(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	m, err := NewManagerWithOptions("zero-opts", ManagerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Cleanup()
	if want := filepath.Join(tmp, taskBaseName(), "zero-opts", "tasks"); m.dir != want {
		t.Fatalf("task dir = %q, want %q", m.dir, want)
	}
	if got := modeOf(t, filepath.Join(tmp, taskBaseName())); got != 0o700 {
		t.Fatalf("base mode = %v, want 0700", got)
	}
}

// A host directory is used as given, its own mode is left alone, and the
// directories the SDK creates below it carry the host's mode.
func TestHostTaskDirUsedAndBaseModeUntouched(t *testing.T) {
	base := hostBase(t, 0o755)
	m, err := NewManagerWithOptions("host-dir", ManagerOptions{Dir: base, DirMode: 0o711})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Cleanup()
	if want := filepath.Join(base, "host-dir", "tasks"); m.dir != want {
		t.Fatalf("task dir = %q, want %q", m.dir, want)
	}
	if got := modeOf(t, base); got != 0o755 {
		t.Fatalf("base mode = %v, want 0755 (untouched)", got)
	}
	for _, d := range []string{filepath.Dir(m.dir), m.dir} {
		if got := modeOf(t, d); got != 0o711 {
			t.Fatalf("%s mode = %v, want 0711", d, got)
		}
	}
}

// A base the host made 0700 stays 0700: the SDK never tightens or loosens it.
func TestHostTaskDirRestrictiveBaseUntouched(t *testing.T) {
	base := hostBase(t, 0o700)
	m, err := NewManagerWithOptions("host-dir-700", ManagerOptions{Dir: base, DirMode: 0o755})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Cleanup()
	if got := modeOf(t, base); got != 0o700 {
		t.Fatalf("base mode = %v, want 0700 (untouched)", got)
	}
}

// Output files stay 0600 and are created through the host directory.
func TestHostTaskDirOutputFileMode(t *testing.T) {
	base := hostBase(t, 0o711)
	m, err := NewManagerWithOptions("host-out", ManagerOptions{Dir: base, DirMode: 0o711})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Cleanup()
	task, err := m.Spawn(SpawnSpec{Command: "echo hi", Kind: "bash"})
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(m.dir, task.ID+".output")
	if got := modeOf(t, out); got != 0o600 {
		t.Fatalf("output file mode = %v, want 0600", got)
	}
}

// The mode is exact: the process umask must not narrow it.
func TestHostTaskDirModeIgnoresUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	base := hostBase(t, 0o755)
	m, err := NewManagerWithOptions("host-umask", ManagerOptions{Dir: base, DirMode: 0o755})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Cleanup()
	for _, d := range []string{filepath.Dir(m.dir), m.dir} {
		if got := modeOf(t, d); got != 0o755 {
			t.Fatalf("%s mode = %v, want 0755 despite umask", d, got)
		}
	}
}

// Cleanup removes the session directory and leaves the host's base in place.
func TestHostTaskDirCleanupKeepsBase(t *testing.T) {
	base := hostBase(t, 0o711)
	m, err := NewManagerWithOptions("host-clean", ManagerOptions{Dir: base, DirMode: 0o711})
	if err != nil {
		t.Fatal(err)
	}
	m.Cleanup()
	if _, err := os.Stat(base); err != nil {
		t.Fatalf("base removed by Cleanup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "host-clean")); !os.IsNotExist(err) {
		t.Fatalf("session dir should be gone, stat err = %v", err)
	}
}

// A symlink in place of the base is refused, not followed.
func TestHostTaskDirRejectsSymlink(t *testing.T) {
	real := hostBase(t, 0o711)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	m, err := NewManagerWithOptions("host-link", ManagerOptions{Dir: link, DirMode: 0o711})
	if err == nil {
		m.Cleanup()
		t.Fatal("symlinked host directory must be refused")
	}
	if entries, _ := os.ReadDir(real); len(entries) != 0 {
		t.Fatalf("nothing may be created through the symlink, found %v", entries)
	}
}

// A symlink planted where the session directory goes is refused too.
func TestHostTaskDirRejectsSymlinkedSessionDir(t *testing.T) {
	base := hostBase(t, 0o711)
	victim := t.TempDir()
	if err := os.Symlink(victim, filepath.Join(base, "host-sess-link")); err != nil {
		t.Fatal(err)
	}
	m, err := NewManagerWithOptions("host-sess-link", ManagerOptions{Dir: base, DirMode: 0o711})
	if err == nil {
		m.Cleanup()
		t.Fatal("symlinked session directory must be refused")
	}
	if got := modeOf(t, victim); got == 0o711 {
		t.Fatal("the symlink target's mode must not be changed")
	}
}

// A base owned by another user is refused. Needs root to hand a directory to a
// different uid.
func TestHostTaskDirRejectsWrongOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to chown the base directory")
	}
	base := hostBase(t, 0o711)
	if err := os.Chown(base, 12345, 12345); err != nil {
		t.Fatal(err)
	}
	m, err := NewManagerWithOptions("host-owner", ManagerOptions{Dir: base, DirMode: 0o711})
	if err == nil {
		m.Cleanup()
		t.Fatal("a base owned by another uid must be refused")
	}
	if !strings.Contains(err.Error(), "owned") {
		t.Fatalf("error should name the ownership problem, got: %v", err)
	}
	if entries, _ := os.ReadDir(base); len(entries) != 0 {
		t.Fatalf("nothing may be created in a refused base, found %v", entries)
	}
}

// The SDK does not create or repair the base.
func TestHostTaskDirMissingBaseIsAnError(t *testing.T) {
	base := filepath.Join(t.TempDir(), "absent")
	m, err := NewManagerWithOptions("host-missing", ManagerOptions{Dir: base, DirMode: 0o711})
	if err == nil {
		m.Cleanup()
		t.Fatal("a missing host directory must be an error")
	}
	if _, serr := os.Lstat(base); !os.IsNotExist(serr) {
		t.Fatalf("the SDK must not create the base, stat err = %v", serr)
	}
}

func TestHostTaskDirRejectsRegularFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if m, err := NewManagerWithOptions("host-file", ManagerOptions{Dir: f, DirMode: 0o711}); err == nil {
		m.Cleanup()
		t.Fatal("a regular file is not a usable base directory")
	}
}

func TestManagerOptionsValidation(t *testing.T) {
	base := hostBase(t, 0o711)
	cases := []struct {
		name string
		opts ManagerOptions
	}{
		{"relative dir", ManagerOptions{Dir: "relative/base", DirMode: 0o711}},
		{"mode without dir", ManagerOptions{DirMode: 0o711}},
		{"owner cannot traverse and write", ManagerOptions{Dir: base, DirMode: 0o511}},
		{"bits beyond permissions", ManagerOptions{Dir: base, DirMode: os.ModeSetuid | 0o755}},
		{"directory bit", ManagerOptions{Dir: base, DirMode: os.ModeDir | 0o755}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if m, err := NewManagerWithOptions("opt-check", tc.opts); err == nil {
				m.Cleanup()
				t.Fatal("expected an error")
			}
		})
	}
}

// A zero mode with a directory means the safe default, 0700.
func TestHostTaskDirZeroModeDefaultsTo0700(t *testing.T) {
	base := hostBase(t, 0o755)
	m, err := NewManagerWithOptions("host-zero-mode", ManagerOptions{Dir: base})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Cleanup()
	if got := modeOf(t, m.dir); got != 0o700 {
		t.Fatalf("tasks dir mode = %v, want 0700", got)
	}
}

// The session id rules still apply with a host directory.
func TestHostTaskDirRejectsPathSessionID(t *testing.T) {
	base := hostBase(t, 0o711)
	if m, err := NewManagerWithOptions("../escape", ManagerOptions{Dir: base, DirMode: 0o711}); err == nil {
		m.Cleanup()
		t.Fatal("a session id with a path separator must be refused")
	}
}

// A base writable by group or others without the sticky bit lets another user
// swap the session directory for a symlink, so it is refused.
func TestHostTaskDirRejectsWritableBase(t *testing.T) {
	for _, mode := range []os.FileMode{0o777, 0o770, 0o707, 0o722} {
		base := hostBase(t, mode)
		_, err := NewManagerWithOptions("writable-base", ManagerOptions{Dir: base})
		if err == nil {
			t.Fatalf("mode %v: base accepted, want refusal", mode)
		}
		if !strings.Contains(err.Error(), "writable") {
			t.Fatalf("mode %v: error %q does not explain the refusal", mode, err)
		}
	}
}

// With the sticky bit only the owner can rename or remove entries, so a
// shared base (like /tmp) is accepted.
func TestHostTaskDirAcceptsStickyWritableBase(t *testing.T) {
	base := hostBase(t, 0o777|os.ModeSticky)
	m, err := NewManagerWithOptions("sticky-base", ManagerOptions{Dir: base})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Cleanup()
}

// Group/other access without write permission is fine.
func TestHostTaskDirAcceptsNonWritableBase(t *testing.T) {
	base := hostBase(t, 0o711)
	m, err := NewManagerWithOptions("711-base", ManagerOptions{Dir: base})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Cleanup()
}
