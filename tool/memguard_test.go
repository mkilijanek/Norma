package tool

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSrc is a scripted procSource.
type fakeSrc struct {
	mu    sync.Mutex
	table procTable
	anon  map[int]uint64
	exact map[int]uint64
	tags  map[int]string
}

func (f *fakeSrc) Budget() uint64 { return 8 << 30 }
func (f *fakeSrc) Snapshot() (procTable, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := procTable{}
	for k, v := range f.table {
		t[k] = v
	}
	return t, nil
}
func (f *fakeSrc) Anon(pid int) (uint64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.anon[pid]
	return v, ok
}
func (f *fakeSrc) AnonExact(pid int) (uint64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.exact[pid]; ok {
		return v, true
	}
	return 0, false
}
func (f *fakeSrc) HasTag(pid int, tag string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tags[pid] == tag
}

type killed struct {
	id     string
	reason string
	pids   []int
}

func newTestGuard(src *fakeSrc, pol memPolicy, out *[]killed) *memGuard {
	return newMemGuard(context.Background(), src, pol, func(t *Task, reason string, pids []int) {
		*out = append(*out, killed{t.ID, reason, pids})
	})
}

const mib = 1 << 20

func TestGuardKillsOverCapAndNamesLargest(t *testing.T) {
	src := &fakeSrc{
		table: procTable{100: {PPID: 1, PGRP: 100, Comm: "sh"}, 101: {PPID: 100, PGRP: 100, Comm: "python3"}},
		anon:  map[int]uint64{100: 5 * mib, 101: 900 * mib},
	}
	var ks []killed
	g := newTestGuard(src, memPolicy{PerTask: 500 * mib, Total: 800 * mib}, &ks)
	g.ctx = context.Background()
	g.tasks["task_1"] = &guardTask{t: &Task{ID: "task_1"}, pgid: 100, tag: "x", escaped: map[int]uint64{}}
	g.pass(time.Now())
	if len(ks) != 1 || !strings.Contains(ks[0].reason, "python3") || !strings.Contains(ks[0].reason, "limit") {
		t.Fatalf("kills = %+v", ks)
	}
}

// Shared copy-on-write pages inflate the cheap figure; the share-aware
// confirmation must spare the task.
func TestGuardConfirmSpares(t *testing.T) {
	src := &fakeSrc{
		table: procTable{100: {PPID: 1, PGRP: 100}, 101: {PPID: 100, PGRP: 100}},
		anon:  map[int]uint64{100: 400 * mib, 101: 400 * mib},
		exact: map[int]uint64{100: 210 * mib, 101: 190 * mib},
	}
	var ks []killed
	g := newTestGuard(src, memPolicy{PerTask: 500 * mib, Total: 600 * mib}, &ks)
	g.tasks["t"] = &guardTask{t: &Task{ID: "t"}, pgid: 100, tag: "x", escaped: map[int]uint64{}}
	g.pass(time.Now())
	if len(ks) != 0 {
		t.Fatalf("killed despite exact figure under cap: %+v", ks)
	}
}

// A setsid() escapee is outside the group; the tag and the ppid chain find it.
func TestGuardFindsEscapedByTagAndDescendants(t *testing.T) {
	src := &fakeSrc{
		table: procTable{
			100: {PPID: 1, PGRP: 100},
			300: {PPID: 1, PGRP: 300, Start: 7},   // setsid'd, reparented to init, tagged
			301: {PPID: 300, PGRP: 300, Start: 8}, // its child, untagged
			400: {PPID: 1, PGRP: 400},             // unrelated
		},
		anon: map[int]uint64{100: mib, 300: 300 * mib, 301: 300 * mib, 400: 5000 * mib},
		tags: map[int]string{300: "x"},
	}
	var ks []killed
	g := newTestGuard(src, memPolicy{PerTask: 500 * mib, Total: 600 * mib}, &ks)
	g.tasks["t"] = &guardTask{t: &Task{ID: "t"}, pgid: 100, tag: "x", escaped: map[int]uint64{}}
	g.pass(time.Now())
	if len(ks) != 1 {
		t.Fatalf("want 1 kill, got %+v", ks)
	}
	got := map[int]bool{}
	for _, p := range ks[0].pids {
		got[p] = true
	}
	if !got[100] || !got[300] || !got[301] || got[400] {
		t.Fatalf("members = %v (want 100,300,301 and not 400)", ks[0].pids)
	}
}

func TestGuardIgnoresReusedPid(t *testing.T) {
	src := &fakeSrc{
		table: procTable{100: {PPID: 1, PGRP: 100}, 300: {PPID: 1, PGRP: 300, Start: 99}},
		anon:  map[int]uint64{100: mib, 300: 9000 * mib},
	}
	var ks []killed
	g := newTestGuard(src, memPolicy{PerTask: 500 * mib, Total: 600 * mib}, &ks)
	// pid 300 was a tagged escapee with start time 7; now it is a different process.
	g.tasks["t"] = &guardTask{t: &Task{ID: "t"}, pgid: 100, tag: "x", escaped: map[int]uint64{300: 7}}
	g.pass(time.Now())
	if len(ks) != 0 {
		t.Fatalf("killed a reused pid: %+v", ks)
	}
}

// Two tasks each under their cap but over the aggregate: the largest dies.
func TestGuardAggregateKillsLargest(t *testing.T) {
	src := &fakeSrc{
		table: procTable{100: {PPID: 1, PGRP: 100}, 200: {PPID: 1, PGRP: 200}},
		anon:  map[int]uint64{100: 400 * mib, 200: 450 * mib},
		exact: map[int]uint64{100: 400 * mib, 200: 450 * mib},
	}
	var ks []killed
	g := newTestGuard(src, memPolicy{PerTask: 500 * mib, Total: 800 * mib}, &ks)
	g.tasks["a"] = &guardTask{t: &Task{ID: "a"}, pgid: 100, tag: "a", escaped: map[int]uint64{}}
	g.tasks["b"] = &guardTask{t: &Task{ID: "b"}, pgid: 200, tag: "b", escaped: map[int]uint64{}}
	g.pass(time.Now())
	if len(ks) != 1 || ks[0].id != "b" || !strings.Contains(ks[0].reason, "combined") {
		t.Fatalf("kills = %+v", ks)
	}
}

func TestNextInterval(t *testing.T) {
	const limit = 1000 * mib
	if d := nextInterval(limit, 10*mib, 0); d != memTickMax {
		t.Errorf("idle = %v", d)
	}
	if d := nextInterval(limit, 600*mib, 0); d != 300*time.Millisecond {
		t.Errorf("big but flat = %v", d)
	}
	if d := nextInterval(limit, 100*mib, 8000*mib); d != memTickMin {
		t.Errorf("fast leak = %v, want the floor", d)
	}
	if d := nextInterval(limit, 100*mib, 900*mib); d <= memTickMin || d >= memTickMax {
		t.Errorf("moderate leak = %v, want strictly between the clamps", d)
	}
	if d := nextInterval(limit, 2*limit, 0); d != memTickMin {
		t.Errorf("over cap = %v", d)
	}
}

func TestParseMemCap(t *testing.T) {
	const budget = 10 << 30
	cases := []struct {
		in   string
		want uint64
		off  bool
		ok   bool
	}{
		{"2G", 2 << 30, false, true}, {"512MiB", 512 << 20, false, true}, {"1.5g", 3 << 29, false, true},
		{"40%", 4 << 30, false, true}, {"off", 0, true, true}, {"0", 0, true, true},
		{"banana", 0, false, false}, {"-5G", 0, false, false}, {"150%", 0, false, false}, {"", 0, false, false},
	}
	for _, c := range cases {
		got, off, ok := parseMemCap(c.in, budget)
		if got != c.want || off != c.off || ok != c.ok {
			t.Errorf("parseMemCap(%q) = %d,%v,%v want %d,%v,%v", c.in, got, off, ok, c.want, c.off, c.ok)
		}
	}
}

func TestLoadMemPolicy(t *testing.T) {
	src := &fakeSrc{}
	t.Setenv(memCapEnv, "")
	p := loadMemPolicy(src)
	if p.PerTask != 4<<30 || p.Total != uint64(float64(src.Budget())*0.8) {
		t.Fatalf("defaults = %+v", p)
	}
	t.Setenv(memCapEnv, "garbage")
	if q := loadMemPolicy(src); q != p {
		t.Errorf("invalid value must fall back to defaults, got %+v", q)
	}
	t.Setenv(memCapEnv, "off")
	if q := loadMemPolicy(src); q.enabled() {
		t.Errorf("off must disable, got %+v", q)
	}
	t.Setenv(memCapEnv, "7G")
	if q := loadMemPolicy(src); q.PerTask != 7<<30 || q.Total < q.PerTask {
		t.Errorf("explicit cap: %+v (total must not be below per-task)", q)
	}
	if q := loadMemPolicy(nil); q.enabled() {
		t.Error("no source must disable")
	}
}

// helper process: allocate and touch MB megabytes, optionally setsid-escape, then sleep.
func TestMain(m *testing.M) {
	if v := os.Getenv("NORMA_TEST_ALLOC_MB"); v != "" {
		mb, _ := strconv.Atoi(v)
		buf := make([]byte, mb<<20)
		for i := 0; i < len(buf); i += 4096 {
			buf[i] = 1
		}
		time.Sleep(time.Minute)
		_ = buf[0]
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestMemGuardKillsRealProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs /proc")
	}
	if testing.Short() {
		t.Skip("spawns a process")
	}
	t.Setenv(memCapEnv, "150M")
	m, err := NewManager("memguard-test-" + strconv.Itoa(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	defer m.KillAll()
	if m.guard == nil {
		t.Skip("guard unavailable on this host")
	}
	exe, _ := os.Executable()
	task, err := m.Spawn(SpawnSpec{
		Command: "NORMA_TEST_ALLOC_MB=400 exec " + exe,
		Kind:    KindBash, WorkingDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-task.done:
	case <-time.After(15 * time.Second):
		t.Fatal("task not killed within 15s")
	}
	info, _ := m.Get(task.ID)
	if info.Status != TaskFailed || !strings.Contains(info.KillReason, "memory limit exceeded") {
		t.Fatalf("status=%s reason=%q", info.Status, info.KillReason)
	}
}

// A file mapping must not count: a process that touches lots of file pages stays alive.
func TestMemGuardIgnoresFileMappings(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs /proc")
	}
	if testing.Short() {
		t.Skip("spawns a process")
	}
	t.Setenv(memCapEnv, "100M")
	m, err := NewManager("memguard-test-file-" + strconv.Itoa(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	defer m.KillAll()
	if m.guard == nil {
		t.Skip("guard unavailable on this host")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	f := t.TempDir() + "/big"
	if err := os.WriteFile(f, make([]byte, 300<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	task, err := m.Spawn(SpawnSpec{
		Command: `python3 -c "import mmap,sys,time; f=open(sys.argv[1],'rb'); m=mmap.mmap(f.fileno(),0,access=mmap.ACCESS_READ); s=sum(m[i] for i in range(0,len(m),4096)); time.sleep(3)" ` + f,
		Kind:    KindBash, WorkingDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	<-task.done
	info, _ := m.Get(task.ID)
	if info.KillReason != "" || info.Status != TaskCompleted {
		t.Fatalf("file-backed memory was counted: status=%s reason=%q", info.Status, info.KillReason)
	}
}
