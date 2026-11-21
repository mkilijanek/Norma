//go:build linux

package tool

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Kernel tier of the memory guard.
//
// The /proc sampler (memguard.go) cannot beat a leak faster than one tick. A
// cgroup v2 memory limit can: the kernel refuses the allocation and OOM-kills the
// group at the moment the limit is hit. When the host lets us create cgroups, each
// task runs in its own group with memory.max = the per-task cap, under a
// per-Manager parent group with memory.max = the combined cap. The sampler keeps
// running on top: it gives nicer reasons and covers hosts without delegation.
//
// Layout, inside the cgroup this process lives in or the nearest delegated ancestor:
//
//	<own cgroup>/norma-<pid>-<nanos>/          memory.max = combined cap, no processes
//	<own cgroup>/norma-<pid>-<nanos>/task_N/   memory.max = per-task cap, oom.group = 1
//
// The parent holds no processes, so enabling the memory controller for its
// children never trips the "no internal processes" rule. Tasks are created
// straight into their group (clone3 CLONE_INTO_CGROUP via SysProcAttr.CgroupFD),
// so there is no window in which a fresh process runs outside it.
//
// Verified environments (see PR notes for how each was run):
//
//	systemd unit, Delegate=yes + DelegateSubgroup=   works (kernel kill in <0.1 s)
//	systemd scope, Delegate=yes (no subgroup)        unavailable: our cgroup holds
//	                                                 processes; sampler only
//	Docker, private cgroupns, default (read-only
//	  or process-holding root cgroup)                unavailable; sampler only
//	Docker --privileged, private cgroupns, entrypoint
//	  moved to /init and +memory enabled at the root works (the cgroupns root is the
//	                                                 sole-subgroup parent)
//	real kernel 5.14.0-741.el9 (RHEL 9 line, qemu)  works; kernel kill in 0.4 s,
//	                                                 memory.peak present
//	real kernel 5.4 (Ubuntu 20.04, qemu)             no clone3: Spawn falls back, the
//	                                                 sampler kills the allocator
//	kernels without cgroup.kill / memory.peak        also covered by unit tests that
//	                                                 emulate the missing files
//
// scripts/qemu-kernel-test.sh reruns the kernel checks under any kernel image.
//
// Everything is best effort: any failure means "no cgroup for this task" and the
// sampler alone guards it. NORMA_TASK_CGROUP=off disables the tier.

const (
	cgroupEnv    = "NORMA_TASK_CGROUP"
	cgroupPrefix = "norma-"
)

type cgroupTier struct {
	base string // directory of our own cgroup (v2)
	pol  memPolicy

	once sync.Once
	mgr  string // parent group of this Manager; "" when setup failed
}

// newCgroupTier probes for a writable cgroup v2 hierarchy. nil means unavailable.
func newCgroupTier(pol memPolicy) *cgroupTier {
	if !pol.enabled() {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(cgroupEnv))) {
	case "0", "off", "none", "disabled", "false":
		return nil
	}
	base := selfCgroupV2("/sys/fs/cgroup", "/proc/self/cgroup")
	if base == "" || syscall.Access(base, 2 /* W_OK */) != nil {
		return nil
	}
	return &cgroupTier{base: base, pol: pol}
}

// selfCgroupV2 resolves the cgroup v2 directory of the process described by
// selfFile (a /proc/<pid>/cgroup file). "" when the host is not on cgroup v2.
func selfCgroupV2(root, selfFile string) string {
	data, err := os.ReadFile(selfFile)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if parts := strings.SplitN(line, ":", 3); len(parts) == 3 && parts[0] == "0" && parts[1] == "" {
			return filepath.Join(root, parts[2])
		}
	}
	return ""
}

func writeCgroup(dir, file, val string) error {
	return os.WriteFile(filepath.Join(dir, file), []byte(val), 0o644)
}

func hasController(dir, name string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "cgroup.controllers"))
	if err != nil {
		return false
	}
	for _, c := range strings.Fields(string(b)) {
		if c == name {
			return true
		}
	}
	return false
}

func (c *cgroupTier) ensure() string {
	c.once.Do(func() { c.mgr = c.setup() })
	return c.mgr
}

// setup creates the Manager's parent group, in our own cgroup if the kernel lets
// the memory controller be enabled there, else in its parent.
//
// Our own cgroup usually holds processes, and the kernel refuses to enable
// controllers where processes live (no internal processes). The layout that
// works is a delegated unit that keeps its main process in a subgroup
// (systemd's DelegateSubgroup=): the unit cgroup is process-free and ours.
// Nothing marks delegation reliably, so the parent is accepted only when it has
// that exact shape: writable, no processes, and our cgroup is its sole subgroup.
// A slice or a shared parent has other children and is never touched, which
// keeps task groups from escaping the accounting and the stop/kill of the unit
// they belong to.
func (c *cgroupTier) setup() string {
	if syscall.Access(c.base, 2) == nil {
		if mgr := c.setupIn(c.base); mgr != "" {
			return mgr
		}
	}
	parent := filepath.Dir(c.base)
	if parent == c.base || !soleSubgroup(parent, filepath.Base(c.base)) ||
		syscall.Access(parent, 2) != nil {
		return ""
	}
	return c.setupIn(parent)
}

// soleSubgroup reports whether parent holds no processes and child is its only
// subgroup (ignoring groups of Norma Managers).
func soleSubgroup(parent, child string) bool {
	if b, err := os.ReadFile(filepath.Join(parent, "cgroup.procs")); err != nil || len(bytes.TrimSpace(b)) != 0 {
		return false
	}
	ents, err := os.ReadDir(parent)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if e.IsDir() && e.Name() != child && !strings.HasPrefix(e.Name(), cgroupPrefix) {
			return false
		}
	}
	return true
}

// close removes this Manager's groups once their processes are gone. Groups that
// still hold a process (a daemon that was started on purpose) are left in place.
func (c *cgroupTier) close() {
	if c == nil || c.mgr == "" {
		return
	}
	for i := 0; i < 20; i++ {
		kids, _ := os.ReadDir(c.mgr)
		left := false
		for _, k := range kids {
			if k.IsDir() && os.Remove(filepath.Join(c.mgr, k.Name())) != nil {
				left = true
			}
		}
		if !left && os.Remove(c.mgr) == nil {
			return
		}
		time.Sleep(50 * time.Millisecond) // killed processes take a moment to leave
	}
}

func (c *cgroupTier) setupIn(parent string) string {
	sweepStaleCgroups(parent)
	dir := filepath.Join(parent, fmt.Sprintf("%s%d-%d", cgroupPrefix, os.Getpid(), time.Now().UnixNano()))
	if os.Mkdir(dir, 0o755) != nil {
		return ""
	}
	fail := func() string { _ = os.Remove(dir); return "" }
	if !hasController(dir, "memory") {
		_ = writeCgroup(parent, "cgroup.subtree_control", "+memory")
		if !hasController(dir, "memory") {
			return fail()
		}
	}
	if writeCgroup(dir, "cgroup.subtree_control", "+memory") != nil ||
		writeCgroup(dir, "memory.max", strconv.FormatUint(c.pol.Total, 10)) != nil {
		return fail()
	}
	return dir
}

// sweepStaleCgroups removes groups left behind by Managers that no longer exist
// (crash, SIGKILL). rmdir only succeeds on empty groups, so a group that still
// holds a process is never touched; groups of live pids are skipped outright.
func sweepStaleCgroups(base string) {
	ents, err := os.ReadDir(base)
	if err != nil {
		return
	}
	for _, e := range ents {
		name := e.Name()
		if !e.IsDir() || !strings.HasPrefix(name, cgroupPrefix) {
			continue
		}
		pidStr, _, _ := strings.Cut(strings.TrimPrefix(name, cgroupPrefix), "-")
		if pid, err := strconv.Atoi(pidStr); err != nil || (pid > 0 && syscall.Kill(pid, 0) == nil) {
			continue // not ours to judge, or its owner is alive
		}
		dir := filepath.Join(base, name)
		if kids, err := os.ReadDir(dir); err == nil {
			for _, k := range kids {
				if k.IsDir() {
					_ = os.Remove(filepath.Join(dir, k.Name()))
				}
			}
		}
		_ = os.Remove(dir)
	}
}

// cgroupTask is the cgroup of one task.
type cgroupTask struct {
	dir   string
	fd    *os.File
	limit uint64
}

// prepare creates the task's group and points cmd at it. nil means "run without".
func (c *cgroupTier) prepare(id string, cmd *exec.Cmd) *cgroupTask {
	if c == nil {
		return nil
	}
	mgr := c.ensure()
	if mgr == "" {
		return nil
	}
	dir := filepath.Join(mgr, id)
	if os.Mkdir(dir, 0o755) != nil {
		return nil
	}
	if writeCgroup(dir, "memory.max", strconv.FormatUint(c.pol.PerTask, 10)) != nil {
		_ = os.Remove(dir)
		return nil
	}
	_ = writeCgroup(dir, "memory.swap.max", "0")  // else the limit is not a limit: a leak would just swap
	_ = writeCgroup(dir, "memory.oom.group", "1") // kill the whole tree, never one random process of it
	f, err := os.Open(dir)
	if err != nil {
		_ = os.Remove(dir)
		return nil
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.UseCgroupFD = true
	cmd.SysProcAttr.CgroupFD = int(f.Fd())
	return &cgroupTask{dir: dir, fd: f, limit: c.pol.PerTask}
}

// started releases the directory handle once the child exists.
func (g *cgroupTask) started() {
	if g != nil && g.fd != nil {
		_ = g.fd.Close()
		g.fd = nil
	}
}

// discard drops a group whose process never started.
func (g *cgroupTask) discard() {
	if g == nil {
		return
	}
	g.started()
	_ = os.Remove(g.dir)
}

// kill terminates every process in the group, including setsid() escapees and
// anything forked while the kill is under way (cgroup.kill, Linux 5.14+; older
// kernels get a pass over cgroup.procs).
func (g *cgroupTask) kill() {
	if g == nil {
		return
	}
	if writeCgroup(g.dir, "cgroup.kill", "1") == nil {
		return
	}
	for i := 0; i < 3; i++ {
		b, err := os.ReadFile(filepath.Join(g.dir, "cgroup.procs"))
		if err != nil || len(bytes.TrimSpace(b)) == 0 {
			return
		}
		for _, f := range strings.Fields(string(b)) {
			if pid, err := strconv.Atoi(f); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}
}

// finish runs after the task's main process was reaped. It reports whether the
// kernel OOM killer fired in this group and removes the group once it is empty.
// A group that still holds a deliberately daemonized process stays (its limit
// keeps applying) and is retried until it empties or ctx ends.
func (g *cgroupTask) finish(ctx context.Context) (oomReason string) {
	if g == nil {
		return ""
	}
	g.started()
	if cgroupStat(g.dir, "memory.events", "oom_kill") > 0 {
		oomReason = g.oomReason()
	}
	if e := os.Remove(g.dir); e != nil && !os.IsNotExist(e) {
		go func() {
			t := time.NewTicker(10 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					if e := os.Remove(g.dir); e == nil || os.IsNotExist(e) {
						return
					}
				}
			}
		}()
	}
	return oomReason
}

func (g *cgroupTask) oomReason() string {
	peak := cgroupStat(g.dir, "memory.peak", "")
	which := "the task's memory cgroup limit"
	if peak > 0 {
		if peak >= g.limit-g.limit/50 {
			which = "per-task limit"
		} else {
			which = "combined limit for all background tasks"
		}
	}
	msg := fmt.Sprintf("memory limit exceeded (%s, enforced by the kernel): the command's process tree hit the cgroup memory limit and the kernel OOM killer terminated it; per-task limit %s",
		which, humanBytes(g.limit))
	if peak > 0 {
		msg += fmt.Sprintf(", peak %s", humanBytes(peak))
	}
	return msg + ". Process the data in chunks or streams, or lower the working set, then retry."
}

// cgroupStat reads one number: the value of key in a flat-keyed file
// (memory.events), or the whole file when key is "" (memory.peak).
func cgroupStat(dir, file, key string) uint64 {
	b, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return 0
	}
	if key == "" {
		n, _ := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
		return n
	}
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[0] == key {
			n, _ := strconv.ParseUint(f[1], 10, 64)
			return n
		}
	}
	return 0
}
