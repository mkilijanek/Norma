//go:build linux

package tool

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestParseStatOddComm(t *testing.T) {
	// comm containing spaces and parentheses must not shift the fields.
	line := "42 (we ird) (name) S 7 99 99 0 -1 4194304 1 0 0 0 0 0 0 0 20 0 1 0 12345 1000 10 1 2 3"
	e, ok := parseStat([]byte(line))
	if !ok || e.PPID != 7 || e.PGRP != 99 || e.Start != 12345 || e.Comm != "we ird) (name" {
		t.Fatalf("parseStat = %+v ok=%v", e, ok)
	}
	if _, ok := parseStat([]byte("garbage")); ok {
		t.Error("garbage accepted")
	}
	if _, ok := parseStat([]byte("1 (x) S 1")); ok {
		t.Error("truncated line accepted")
	}
}

func TestSumKB(t *testing.T) {
	b := []byte("Name:\tx\nRssAnon:\t   100 kB\nRssFile:\t 9999 kB\nRssShmem:\t 4 kB\nVmSwap:\t 1 kB\n")
	if got, ok := sumKB(b, "RssAnon:", "RssShmem:", "VmSwap:"); !ok || got != 105<<10 {
		t.Fatalf("got %d %v", got, ok)
	}
	if _, ok := sumKB(b, "Nope:"); ok {
		t.Error("missing key reported found")
	}
}

func TestCgroupLimitWalksAncestors(t *testing.T) {
	root := t.TempDir()
	must := func(p, v string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must(filepath.Join(root, "a/b/c/memory.max"), "max\n")      // leaf: unlimited
	must(filepath.Join(root, "a/b/memory.max"), "2147483648\n") // parent: 2 GiB
	must(filepath.Join(root, "a/memory.max"), "8589934592\n")   // grandparent: 8 GiB
	self := filepath.Join(t.TempDir(), "cgroup")
	must(self, "0::/a/b/c\n")
	if got := cgroupLimit(root, self); got != 2<<30 {
		t.Fatalf("limit = %d, want the tightest ancestor (2 GiB)", got)
	}
	must(self, "0::/nonexistent\n")
	if got := cgroupLimit(root, self); got != 0 {
		t.Fatalf("limit = %d, want 0 for no limits", got)
	}
	if got := cgroupLimit(root, filepath.Join(root, "missing")); got != 0 {
		t.Fatalf("limit = %d for unreadable file", got)
	}
}

// End to end: a worker that leaves the task's process group with setsid and
// reparents to init must still be found (env tag) and killed.
func TestMemGuardKillsSetsidEscapee(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns processes")
	}
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid not installed")
	}
	t.Setenv(memCapEnv, "150M")
	m, err := NewManager("memguard-test-setsid-" + strconv.Itoa(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	defer m.KillAll()
	if m.guard == nil {
		t.Skip("guard unavailable")
	}
	exe, _ := os.Executable()
	marker := "NORMA_ESCAPEE_" + strconv.Itoa(os.Getpid())
	task, err := m.Spawn(SpawnSpec{
		// The leaker runs under setsid in the background; the foreground part idles.
		Command: "(NORMA_TEST_ALLOC_MB=400 " + marker + "=1 setsid " + exe + " >/dev/null 2>&1 &) ; sleep 60",
		Kind:    KindBash, WorkingDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-task.done:
	case <-time.After(20 * time.Second):
		t.Fatal("escapee was not found/killed within 20s")
	}
	info, _ := m.Get(task.ID)
	if !strings.Contains(info.KillReason, "memory limit exceeded") {
		t.Fatalf("status=%s reason=%q", info.Status, info.KillReason)
	}
	// The escapee itself must be gone, not just the shell.
	// Poll: a killed process may take a moment to disappear from the table.
	var n int
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		out, _ := exec.Command("pgrep", "-f", "-c", "--", exe).Output()
		if n, _ = strconv.Atoi(strings.TrimSpace(string(out))); n <= 1 || time.Now().After(deadline) { // 1 = this test binary
			break
		}
	}
	if n > 1 {
		t.Fatalf("%d copies of the leaker still alive", n-1)
	}
}

func BenchmarkSnapshot(b *testing.B) {
	src := newProcSource()
	for i := 0; i < b.N; i++ {
		if _, err := src.Snapshot(); err != nil {
			b.Fatal(err)
		}
	}
}

// The synchronous Bash path must be guarded like a background task.
func TestRunBashSyncIsGuarded(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a process")
	}
	t.Setenv(memCapEnv, "150M")
	old := syncShared
	syncShared = &syncBox{} // rebuild the shared manager under the test's cap
	defer func() { syncShared = old }()
	if m := syncManager(); m == nil || m.guard == nil {
		t.Skip("guard unavailable")
	}
	exe, _ := os.Executable()
	done := make(chan Result, 1)
	go func() {
		r, _ := runBashSync(context.Background(), &ToolContext{WorkingDir: t.TempDir()}, "NORMA_TEST_ALLOC_MB=400 exec "+exe, time.Minute)
		done <- r
	}()
	select {
	case r := <-done:
		if !r.IsError || !strings.Contains(r.Flatten(), "memory limit exceeded") {
			t.Fatalf("result: %+v", r)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("sync command was not killed within 20s")
	}
}

func TestRunBashSyncStillWorks(t *testing.T) {
	r, _ := runBashSync(context.Background(), &ToolContext{WorkingDir: t.TempDir()}, "echo hi; exit 0", 10*time.Second)
	if r.IsError || !strings.Contains(r.Flatten(), "hi") {
		t.Fatalf("ok: %+v", r)
	}
	r, _ = runBashSync(context.Background(), &ToolContext{WorkingDir: t.TempDir()}, "echo bad; exit 3", 10*time.Second)
	if !r.IsError || !strings.Contains(r.Flatten(), "[exit code 3]") {
		t.Fatalf("exit code: %+v", r)
	}
	r, _ = runBashSync(context.Background(), &ToolContext{WorkingDir: t.TempDir()}, "sleep 30", 300*time.Millisecond)
	if !r.IsError || !strings.Contains(r.Flatten(), "timed out") {
		t.Fatalf("timeout: %+v", r)
	}
}
