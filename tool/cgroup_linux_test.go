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

func TestSelfCgroupV2(t *testing.T) {
	f := filepath.Join(t.TempDir(), "cgroup")
	_ = os.WriteFile(f, []byte("12:memory:/old\n0::/user.slice/x.scope\n"), 0o644)
	if got := selfCgroupV2("/sys/fs/cgroup", f); got != "/sys/fs/cgroup/user.slice/x.scope" {
		t.Fatalf("got %q", got)
	}
	_ = os.WriteFile(f, []byte("12:memory:/old\n"), 0o644) // v1 only
	if got := selfCgroupV2("/sys/fs/cgroup", f); got != "" {
		t.Fatalf("v1 host must not match, got %q", got)
	}
}

// A plain directory stands in for the cgroup filesystem: it records what we write.
func fakeTier(t *testing.T) (*cgroupTier, string) {
	dir := t.TempDir()
	c := &cgroupTier{base: dir, pol: memPolicy{PerTask: 200 << 20, Total: 600 << 20}}
	c.once.Do(func() {})
	c.mgr = dir
	return c, dir
}

func TestCgroupPrepareWritesLimits(t *testing.T) {
	c, dir := fakeTier(t)
	cmd := exec.Command("true")
	g := c.prepare("task_1", cmd)
	if g == nil {
		t.Fatal("prepare failed")
	}
	defer g.discard()
	rd := func(f string) string {
		b, _ := os.ReadFile(filepath.Join(dir, "task_1", f))
		return strings.TrimSpace(string(b))
	}
	if rd("memory.max") != strconv.Itoa(200<<20) || rd("memory.swap.max") != "0" || rd("memory.oom.group") != "1" {
		t.Fatalf("limits: max=%q swap=%q group=%q", rd("memory.max"), rd("memory.swap.max"), rd("memory.oom.group"))
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.UseCgroupFD || cmd.SysProcAttr.CgroupFD <= 0 {
		t.Fatalf("cmd not pointed at the cgroup: %+v", cmd.SysProcAttr)
	}
}

func TestCgroupNilSafe(t *testing.T) {
	var c *cgroupTier
	if c.prepare("x", exec.Command("true")) != nil {
		t.Fatal("nil tier must yield no cgroup")
	}
	var g *cgroupTask
	g.started()
	g.discard()
	g.kill()
	if g.finish(context.Background()) != "" {
		t.Fatal("nil task reports nothing")
	}
}

func TestCgroupFinishReportsOOM(t *testing.T) {
	c, dir := fakeTier(t)
	g := c.prepare("task_1", exec.Command("true"))
	td := filepath.Join(dir, "task_1")

	_ = os.WriteFile(filepath.Join(td, "memory.events"), []byte("low 0\nhigh 0\nmax 3\noom 1\noom_kill 0\n"), 0o644)
	if r := g.finish(context.Background()); r != "" {
		t.Fatalf("no oom_kill, got %q", r)
	}

	g = &cgroupTask{dir: td, limit: 200 << 20}
	_ = os.WriteFile(filepath.Join(td, "memory.events"), []byte("max 9\noom 1\noom_kill 2\n"), 0o644)
	_ = os.WriteFile(filepath.Join(td, "memory.peak"), []byte(strconv.Itoa(200<<20)+"\n"), 0o644)
	r := g.finish(context.Background())
	if !strings.Contains(r, "memory limit exceeded") || !strings.Contains(r, "per-task limit") || !strings.Contains(r, "peak 200.0 MiB") {
		t.Fatalf("reason: %q", r)
	}

	_ = os.WriteFile(filepath.Join(td, "memory.peak"), []byte(strconv.Itoa(90<<20)), 0o644)
	if r := g.oomReason(); !strings.Contains(r, "combined limit") {
		t.Fatalf("a task far below its own cap died of the aggregate: %q", r)
	}
}

func TestSweepStaleCgroups(t *testing.T) {
	base := t.TempDir()
	dead := filepath.Join(base, cgroupPrefix+"2147483000-1") // no such pid
	live := filepath.Join(base, cgroupPrefix+strconv.Itoa(os.Getpid())+"-1")
	other := filepath.Join(base, "systemd-thing")
	for _, d := range []string{dead, filepath.Join(dead, "task_1"), live, other} {
		_ = os.MkdirAll(d, 0o755)
	}
	sweepStaleCgroups(base)
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Error("stale group of a dead manager should be removed")
	}
	for _, d := range []string{live, other} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("%s must survive", d)
		}
	}
}

// Real kernel enforcement. Needs a delegated cgroup v2 whose unit keeps its main
// process in a subgroup, for example (run from a directory that is not part of
// the test binary's path, so pgrep-based tests do not match systemd-run itself):
//
//	go test -c -o tool.test ./tool && cd <dir> && systemd-run --user --wait --collect --pipe \
//	    -p Delegate=yes -p DelegateSubgroup=sup --working-directory=$PWD ./tool.test -test.run Cgroup -test.v
//
// Skipped elsewhere (CI included). The sampler is switched off so only the kernel
// can stop the process.
func TestCgroupKernelKillsAllocator(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a process")
	}
	t.Setenv(memCapEnv, "150M")
	m, err := NewManager("cgroup-test-" + strconv.Itoa(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Cleanup()
	if m.cgroups == nil || m.cgroups.ensure() == "" {
		t.Skip("no delegated cgroup v2 with the memory controller on this host")
	}
	m.guard = nil // kernel only
	exe, _ := os.Executable()
	task, err := m.Spawn(SpawnSpec{Command: "NORMA_TEST_ALLOC_MB=400 exec " + exe, Kind: KindBash, WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if task.cg == nil {
		t.Fatal("task did not get a cgroup although the tier is available")
	}
	select {
	case <-task.done:
	case <-time.After(15 * time.Second):
		t.Fatal("kernel did not stop the task within 15s")
	}
	info, _ := m.Get(task.ID)
	if info.Status != TaskFailed || !strings.Contains(info.KillReason, "enforced by the kernel") {
		t.Fatalf("status=%s reason=%q", info.Status, info.KillReason)
	}
	t.Log(info.KillReason)
}

// An allocation under the cap must run to completion inside its cgroup.
func TestCgroupKernelSparesSmallTask(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a process")
	}
	t.Setenv(memCapEnv, "300M")
	m, err := NewManager("cgroup-test2-" + strconv.Itoa(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Cleanup()
	if m.cgroups == nil || m.cgroups.ensure() == "" {
		t.Skip("no delegated cgroup v2 with the memory controller on this host")
	}
	task, err := m.Spawn(SpawnSpec{Command: "echo hi; cat /proc/self/cgroup", Kind: KindBash, WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	<-task.done
	info, _ := m.Get(task.ID)
	out, _ := m.Output(task.ID, 0)
	if info.Status != TaskCompleted || info.KillReason != "" || !strings.Contains(out, "/norma-") {
		t.Fatalf("status=%s reason=%q out=%q", info.Status, info.KillReason, out)
	}
	if _, err := os.Stat(task.cg.dir); !os.IsNotExist(err) {
		t.Errorf("group %s should be gone after the task exits", task.cg.dir)
	}
	mgr := m.cgroups.mgr
	m.Cleanup()
	if _, err := os.Stat(mgr); !os.IsNotExist(err) {
		t.Errorf("manager group %s should be gone after Cleanup", mgr)
	}
}

func TestSoleSubgroup(t *testing.T) {
	parent := t.TempDir()
	for _, d := range []string{"sup", cgroupPrefix + "1-1"} {
		_ = os.Mkdir(filepath.Join(parent, d), 0o755)
	}
	_ = os.WriteFile(filepath.Join(parent, "cgroup.procs"), nil, 0o644)
	if !soleSubgroup(parent, "sup") {
		t.Error("process-free parent with our group as only child must qualify")
	}
	_ = os.Mkdir(filepath.Join(parent, "other.service"), 0o755)
	if soleSubgroup(parent, "sup") {
		t.Error("a parent shared with other groups (a slice) must not qualify")
	}
	_ = os.Remove(filepath.Join(parent, "other.service"))
	_ = os.WriteFile(filepath.Join(parent, "cgroup.procs"), []byte("42\n"), 0o644)
	if soleSubgroup(parent, "sup") {
		t.Error("a parent that holds processes must not qualify")
	}
}

func TestCgroupCloseRemovesManagerGroup(t *testing.T) {
	c, dir := fakeTier(t)
	c.mgr = filepath.Join(dir, "norma-1-1")
	_ = os.MkdirAll(filepath.Join(c.mgr, "task_1"), 0o755)
	c.close()
	if _, err := os.Stat(c.mgr); !os.IsNotExist(err) {
		t.Error("manager group should be removed")
	}
	(*cgroupTier)(nil).close() // nil-safe
}

// Kernels before 5.14 have no cgroup.kill: the fallback walks cgroup.procs.
// Making cgroup.kill a directory emulates "the write fails".
func TestCgroupKillFallbackWalksProcs(t *testing.T) {
	var pids []string
	var cmds []*exec.Cmd
	for i := 0; i < 2; i++ {
		c := exec.Command("sleep", "60")
		if err := c.Start(); err != nil {
			t.Skip("no sleep binary")
		}
		cmds = append(cmds, c)
		pids = append(pids, strconv.Itoa(c.Process.Pid))
	}
	defer func() {
		for _, c := range cmds {
			_ = c.Process.Kill()
			_, _ = c.Process.Wait()
		}
	}()
	dir := t.TempDir()
	_ = os.Mkdir(filepath.Join(dir, "cgroup.kill"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "cgroup.procs"), []byte(strings.Join(pids, "\n")+"\n"), 0o644)
	(&cgroupTask{dir: dir}).kill()
	for _, c := range cmds {
		done := make(chan error, 1)
		go func() { _, err := c.Process.Wait(); done <- err }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatalf("pid %d survived the fallback kill", c.Process.Pid)
		}
	}
}

// Kernels before 5.19 have no memory.peak: the reason must still be sensible.
func TestCgroupOOMReasonWithoutPeak(t *testing.T) {
	g := &cgroupTask{dir: t.TempDir(), limit: 200 << 20}
	r := g.oomReason()
	if !strings.Contains(r, "memory limit exceeded") || strings.Contains(r, "peak") || strings.Contains(r, "combined") {
		t.Fatalf("reason without peak: %q", r)
	}
}

// When the kernel refuses CLONE_INTO_CGROUP (clone3 needs 5.7+, or the group is
// not a real cgroup), Spawn must retry without the cgroup and run the task under
// the sampler alone. A plain directory stands in for a cgroup the kernel rejects.
func TestSpawnFallsBackWhenKernelRejectsCgroup(t *testing.T) {
	m, err := NewManager("cgroup-fallback-" + strconv.Itoa(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Cleanup()
	fake, _ := fakeTier(t)
	m.cgroups = fake
	task, err := m.Spawn(SpawnSpec{Command: "echo ran", Kind: KindBash, WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Spawn must fall back, got %v", err)
	}
	<-task.done
	out, _ := m.Output(task.ID, 0)
	info, _ := m.Get(task.ID)
	if info.Status != TaskCompleted || !strings.Contains(out, "ran") {
		t.Fatalf("status=%s out=%q", info.Status, out)
	}
	if task.cg != nil {
		t.Error("the fallback run must not carry a cgroup")
	}
}
