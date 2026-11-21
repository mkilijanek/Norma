package tool

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Memory guard for background tasks.
//
// A runaway command (a leaking script, an accidental `sort` of a huge file) must
// not take the agent — or the host — down with it. The guard watches every
// running task and kills the whole process tree when it holds too much memory.
//
// Design (see memguard_linux.go for the /proc side):
//
//   - Metric: anonymous memory (RssAnon + RssShmem + VmSwap), not RSS. A process
//     that mmaps a 1 GiB file read-only shows 1 GiB of RSS but ~0 anonymous
//     memory; counting file pages kills healthy grep/sqlite/mmap workloads. Swap
//     is counted so a leak that has been pushed to swap is still seen.
//     Before any kill the figure is re-measured with Pss_Anon + Pss_Shmem +
//     SwapPss, which divides pages shared by forked children instead of counting
//     them once per process.
//   - Membership: process group, plus descendants (ppid tree), plus processes
//     carrying the task's environment tag. setsid()/daemonizing escapees leave
//     the group but keep the tag and the ppid chain, so they are still counted
//     and killed.
//   - Cadence: one sampler goroutine per Manager. It samples sooner as a task
//     approaches its cap, using the observed growth rate (headroom / rate / 4,
//     clamped to [memTickMin, memTickMax]). Polling cannot stop a leak faster
//     than one tick, but a fast leaker is watched fast.
//   - Budget: a per-task cap AND an aggregate cap across all tasks, derived from
//     the effective limit (the tightest cgroup ancestor limit, else MemTotal).
//     Over the aggregate, the largest task dies.
//   - Kernel tier (cgroup_linux.go): where the host delegates a cgroup v2 with the
//     memory controller, each task also runs under a kernel memory.max, so a leak
//     faster than one sampler tick is stopped by the kernel itself. Best effort;
//     NORMA_TASK_CGROUP=off disables it, and the sampler covers every other host.
//   - Report: the kill reason names the peak, the growth rate and the largest
//     process, and reaches the model on both the foreground and background paths.

const (
	memTickMin  = 100 * time.Millisecond
	memTickMax  = time.Second
	memTagEvery = 2 * time.Second // how often processes outside the group are checked for the tag

	defaultPerTaskFrac = 0.50
	defaultTotalFrac   = 0.80

	// memCapEnv overrides the per-task cap: a size ("2G", "512MiB"), a percentage
	// of the budget ("40%"), or "off". An unparseable value falls back to the
	// default rather than silently disabling the guard.
	memCapEnv = "NORMA_TASK_MEM_CAP"

	// memTagEnv is set in every task's environment; its value identifies the task.
	memTagEnv = "NORMA_TASK_TAG"

	// memReserveEnv overrides the host headroom floor (see memPolicy.MinAvail): a
	// size, a percentage of the budget, or "off".
	memReserveEnv = "NORMA_TASK_MEM_RESERVE"
)

// memPolicy is the resolved set of limits in bytes. Zero PerTask means disabled.
type memPolicy struct {
	PerTask uint64
	Total   uint64
	// MinAvail is the host headroom floor: when MemAvailable drops below it and the
	// tasks hold enough memory to close the gap, the largest task is killed. Caps
	// are fractions of the budget, so they cannot see memory that other processes
	// already use. Zero disables the check.
	MinAvail uint64
}

func (p memPolicy) enabled() bool { return p.PerTask > 0 }

// loadMemPolicy resolves limits from the environment and the host budget.
func loadMemPolicy(src procSource) memPolicy {
	if src == nil {
		return memPolicy{}
	}
	budget := src.Budget()
	if budget == 0 {
		return memPolicy{}
	}
	perTask := uint64(float64(budget) * defaultPerTaskFrac)
	if raw := strings.TrimSpace(os.Getenv(memCapEnv)); raw != "" {
		c, off, ok := parseMemCap(raw, budget)
		switch {
		case ok && off:
			return memPolicy{}
		case ok:
			perTask = c
		}
	}
	total := uint64(float64(budget) * defaultTotalFrac)
	if total < perTask {
		total = perTask
	}
	return memPolicy{PerTask: perTask, Total: total, MinAvail: loadReserve(budget)}
}

// loadReserve resolves the headroom floor: 5% of the budget, at least 256 MiB,
// at most a quarter of the budget, unless NORMA_TASK_MEM_RESERVE says otherwise.
func loadReserve(budget uint64) uint64 {
	if raw := strings.TrimSpace(os.Getenv(memReserveEnv)); raw != "" {
		if c, off, ok := parseMemCap(raw, budget); ok {
			if off {
				return 0
			}
			return c
		}
	}
	r := budget / 20
	if r < 256<<20 {
		r = 256 << 20
	}
	if q := budget / 4; r > q {
		r = q
	}
	return r
}

// parseMemCap parses "2G", "512MiB", "1.5g", "40%" or an off-word. ok=false means
// the string was not understood (the caller keeps its default).
func parseMemCap(s string, budget uint64) (limit uint64, off, ok bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "0", "off", "none", "disabled", "false":
		return 0, true, true
	}
	if strings.HasSuffix(s, "%") {
		f, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
		if err != nil || f <= 0 || f > 100 {
			return 0, false, false
		}
		return uint64(float64(budget) * f / 100), false, true
	}
	s = strings.TrimSuffix(s, "b")
	s = strings.TrimSuffix(s, "i")
	mult := uint64(1)
	if n := len(s); n > 0 {
		switch s[n-1] {
		case 'k':
			mult, s = 1<<10, s[:n-1]
		case 'm':
			mult, s = 1<<20, s[:n-1]
		case 'g':
			mult, s = 1<<30, s[:n-1]
		case 't':
			mult, s = 1<<40, s[:n-1]
		}
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f <= 0 {
		return 0, false, false
	}
	return uint64(f * float64(mult)), false, true
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// procEntry is one row of the process table.
type procEntry struct {
	PPID, PGRP int
	Start      uint64 // process start time (clock ticks since boot); guards against pid reuse
	Comm       string
}

type procTable map[int]procEntry

// procSource is the OS seam: everything the guard needs to read about processes.
type procSource interface {
	// Budget is the effective memory limit in bytes (0 = unknown).
	Budget() uint64
	// Snapshot lists all processes.
	Snapshot() (procTable, error)
	// Anon is the cheap per-process figure: anonymous + shmem + swap bytes.
	Anon(pid int) (uint64, bool)
	// AnonExact is the slower, share-aware figure (Pss_Anon + Pss_Shmem + SwapPss).
	AnonExact(pid int) (uint64, bool)
	// Available is the host's MemAvailable in bytes (false = unknown).
	Available() (uint64, bool)
	// HasTag reports whether the process environment carries tag.
	HasTag(pid int, tag string) bool
}

type guardTask struct {
	t       *Task
	pgid    int
	tag     string
	escaped map[int]uint64 // pid -> start time of tag-matched processes outside the group

	lastTagScan time.Time
	members     []int
	anon, peak  uint64
	rate        float64 // EWMA of growth, bytes/second
	topComm     string
	topPID      int
	topAnon     uint64
	last, next  time.Time
}

// memGuard is the single sampler for one Manager.
type memGuard struct {
	src  procSource
	pol  memPolicy
	kill func(t *Task, reason string, pids []int)

	mu      sync.Mutex
	tasks   map[string]*guardTask
	running bool
	ctx     context.Context
}

func newMemGuard(ctx context.Context, src procSource, pol memPolicy, kill func(*Task, string, []int)) *memGuard {
	if src == nil || !pol.enabled() {
		return nil
	}
	return &memGuard{src: src, pol: pol, kill: kill, tasks: map[string]*guardTask{}, ctx: ctx}
}

// add starts watching t (whose process group id is pgid).
func (g *memGuard) add(t *Task, pgid int, tag string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.tasks[t.ID] = &guardTask{t: t, pgid: pgid, tag: tag, escaped: map[int]uint64{}, next: time.Now().Add(memTickMin)}
	start := !g.running
	g.running = true
	g.mu.Unlock()
	if start {
		go g.loop()
	}
}

func (g *memGuard) remove(id string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	delete(g.tasks, id)
	g.mu.Unlock()
}

// loop runs until no tasks remain or the Manager shuts down.
func (g *memGuard) loop() {
	for {
		g.mu.Lock()
		if len(g.tasks) == 0 || g.ctx.Err() != nil {
			g.running = false
			g.mu.Unlock()
			return
		}
		wake := time.Now().Add(memTickMax)
		for _, gt := range g.tasks {
			if gt.next.Before(wake) {
				wake = gt.next
			}
		}
		g.mu.Unlock()

		if d := time.Until(wake); d > 0 {
			select {
			case <-time.After(d):
			case <-g.ctx.Done():
			}
		}
		g.pass(time.Now())
	}
}

type memKill struct {
	gt     *guardTask
	reason string
	pids   []int
}

// pass samples every task that is due and kills offenders. Kills run after the
// lock is released.
func (g *memGuard) pass(now time.Time) {
	g.mu.Lock()
	var due []*guardTask
	for _, gt := range g.tasks {
		if !gt.next.After(now) {
			due = append(due, gt)
		}
	}
	if len(due) == 0 {
		g.mu.Unlock()
		return
	}
	table, err := g.src.Snapshot()
	if err != nil {
		for _, gt := range due {
			gt.next = now.Add(memTickMax)
		}
		g.mu.Unlock()
		return
	}

	var kills []memKill
	for _, gt := range due {
		g.sample(gt, table, now, false)
		if gt.anon > g.pol.PerTask {
			g.sample(gt, table, now, true) // confirm with the share-aware figure
			if gt.anon > g.pol.PerTask {
				kills = append(kills, memKill{gt, g.reason(gt, g.pol.PerTask, "per-task limit"), gt.members})
				continue
			}
		}
		gt.next = now.Add(nextInterval(g.pol.PerTask, gt.anon, gt.rate))
	}

	if len(kills) == 0 {
		// Aggregate budget across every watched task (including not-yet-due ones).
		var total uint64
		var largest *guardTask
		for _, gt := range g.tasks {
			total += gt.anon
			if largest == nil || gt.anon > largest.anon {
				largest = gt
			}
		}
		if total > g.pol.Total && largest != nil {
			// Confirm: re-measure everyone share-aware before killing anyone.
			total = 0
			largest = nil
			for _, gt := range g.tasks {
				g.sample(gt, table, now, true)
				total += gt.anon
				if largest == nil || gt.anon > largest.anon {
					largest = gt
				}
			}
			if total > g.pol.Total && largest != nil {
				reason := g.reason(largest, g.pol.Total, "combined limit for all background tasks")
				reason += fmt.Sprintf(" (all tasks together held %s)", humanBytes(total))
				kills = append(kills, memKill{largest, reason, largest.members})
			}
		}
	}
	if len(kills) == 0 {
		if k, ok := g.pressure(table, now); ok {
			kills = append(kills, k)
		}
	}
	for _, k := range kills {
		delete(g.tasks, k.gt.t.ID)
	}
	g.mu.Unlock()

	for _, k := range kills {
		g.kill(k.gt.t, k.reason, k.pids)
	}
}

// pressure checks host headroom. When MemAvailable is under the floor and the
// tasks together hold at least the missing amount (so killing them can help), the
// largest task is killed. The figures are re-measured share-aware first, like the
// other kill paths, and one task per pass: the next pass sees the new headroom.
func (g *memGuard) pressure(table procTable, now time.Time) (memKill, bool) {
	if g.pol.MinAvail == 0 || len(g.tasks) == 0 {
		return memKill{}, false
	}
	avail, ok := g.src.Available()
	if !ok || avail >= g.pol.MinAvail {
		return memKill{}, false
	}
	var total uint64
	var largest *guardTask
	for _, gt := range g.tasks {
		g.sample(gt, table, now, true)
		total += gt.anon
		if largest == nil || gt.anon > largest.anon {
			largest = gt
		}
	}
	shortfall := g.pol.MinAvail - avail
	if largest == nil || total < shortfall {
		return memKill{}, false // the pressure is not ours; killing tasks would not fix it
	}
	reason := g.reason(largest, g.pol.MinAvail, "host memory pressure")
	reason = strings.Replace(reason, fmt.Sprintf(", limit %s", humanBytes(g.pol.MinAvail)), "", 1)
	reason += fmt.Sprintf(" (the host had only %s available, below its %s reserve; background tasks held %s)",
		humanBytes(avail), humanBytes(g.pol.MinAvail), humanBytes(total))
	return memKill{largest, reason, largest.members}, true
}

// sample refreshes gt's membership and memory figures. exact selects the
// share-aware (smaps_rollup) measurement.
func (g *memGuard) sample(gt *guardTask, table procTable, now time.Time, exact bool) {
	gt.members = g.membersOf(gt, table, now)
	var sum, top uint64
	gt.topPID, gt.topComm = 0, ""
	for _, pid := range gt.members {
		var v uint64
		var ok bool
		if exact {
			v, ok = g.src.AnonExact(pid)
			if !ok {
				v, ok = g.src.Anon(pid)
			}
		} else {
			v, ok = g.src.Anon(pid)
		}
		if !ok {
			continue
		}
		sum += v
		if v >= top {
			top, gt.topPID, gt.topComm = v, pid, table[pid].Comm
		}
	}
	gt.topAnon = top
	if !exact && !gt.last.IsZero() {
		if dt := now.Sub(gt.last).Seconds(); dt > 0 {
			inst := (float64(sum) - float64(gt.anon)) / dt
			gt.rate = 0.5*gt.rate + 0.5*inst
		}
	}
	if !exact {
		gt.last = now
	}
	gt.anon = sum
	if sum > gt.peak {
		gt.peak = sum
	}
}

// membersOf returns the pids that belong to the task: its process group, every
// descendant of those, and tagged escapees (with their descendants).
func (g *memGuard) membersOf(gt *guardTask, table procTable, now time.Time) []int {
	in := map[int]bool{}
	for pid, e := range table {
		if e.PGRP == gt.pgid {
			in[pid] = true
		}
	}
	if now.Sub(gt.lastTagScan) >= memTagEvery {
		gt.lastTagScan = now
		self := os.Getpid()
		for pid, e := range table {
			if in[pid] || pid == self || e.PPID == 2 || pid == 2 {
				continue // already a member, ourselves, or a kernel thread
			}
			if _, known := gt.escaped[pid]; known {
				continue
			}
			if g.src.HasTag(pid, gt.tag) {
				gt.escaped[pid] = e.Start
			}
		}
	}
	for pid, start := range gt.escaped {
		if e, ok := table[pid]; ok && e.Start == start {
			in[pid] = true
		} else {
			delete(gt.escaped, pid) // gone, or the pid was reused
		}
	}
	for changed := true; changed; {
		changed = false
		for pid, e := range table {
			if !in[pid] && pid > 1 && in[e.PPID] {
				in[pid] = true
				changed = true
			}
		}
	}
	out := make([]int, 0, len(in))
	for pid := range in {
		out = append(out, pid)
	}
	sort.Ints(out)
	return out
}

// nextInterval picks how long to wait before sampling again: a quarter of the
// predicted time to reach the cap, clamped. With no growth it relaxes, but stays
// alert when the task already holds over half the cap.
func nextInterval(limit, anon uint64, rate float64) time.Duration {
	if anon >= limit {
		return memTickMin
	}
	headroom := float64(limit - anon)
	if rate > 0 {
		d := time.Duration(headroom / rate / 4 * float64(time.Second))
		return clampDur(d, memTickMin, memTickMax)
	}
	if anon > limit/2 {
		return 300 * time.Millisecond
	}
	return memTickMax
}

func clampDur(d, lo, hi time.Duration) time.Duration {
	if d < lo {
		return lo
	}
	if d > hi {
		return hi
	}
	return d
}

func (g *memGuard) reason(gt *guardTask, limit uint64, which string) string {
	msg := fmt.Sprintf("memory limit exceeded (%s): the command's process tree held %s of memory, limit %s",
		which, humanBytes(gt.anon), humanBytes(limit))
	if gt.rate > 1<<20 {
		msg += fmt.Sprintf(", growing about %s/s", humanBytes(uint64(gt.rate)))
	}
	if gt.topPID != 0 && len(gt.members) > 1 {
		msg += fmt.Sprintf("; largest process: %s (pid %d, %s)", gt.topComm, gt.topPID, humanBytes(gt.topAnon))
	}
	return msg + ". The command was killed; process the data in chunks or streams, or lower the working set, then retry."
}
