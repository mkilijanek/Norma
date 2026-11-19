//go:build linux

package tool

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// linuxProc reads process memory from /proc.
type linuxProc struct{}

func newProcSource() procSource { return linuxProc{} }

// Budget is the tightest memory limit that applies to this process: the smallest
// memory.max on the way from our own cgroup up to the root (cgroup v2), the v1
// limit, or MemTotal.
func (linuxProc) Budget() uint64 {
	budget := memTotal()
	if lim := cgroupLimit("/sys/fs/cgroup", "/proc/self/cgroup"); lim > 0 && (budget == 0 || lim < budget) {
		budget = lim
	}
	return budget
}

func memTotal() uint64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				n, _ := strconv.ParseUint(f[1], 10, 64)
				return n << 10
			}
		}
	}
	return 0
}

// cgroupLimit returns the smallest finite memory limit among our cgroup and its
// ancestors (0 when none is set). root is the cgroup mount, selfFile a
// /proc/<pid>/cgroup-format file.
func cgroupLimit(root, selfFile string) uint64 {
	var best uint64
	consider := func(raw []byte) {
		s := strings.TrimSpace(string(raw))
		if s == "" || s == "max" {
			return
		}
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil || n == 0 || n >= 1<<60 { // v1 reports ~2^63 for "no limit"
			return
		}
		if best == 0 || n < best {
			best = n
		}
	}
	data, err := os.ReadFile(selfFile)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		switch {
		case parts[0] == "0" && parts[1] == "": // cgroup v2
			for dir := filepath.Join(root, parts[2]); ; dir = filepath.Dir(dir) {
				if b, err := os.ReadFile(filepath.Join(dir, "memory.max")); err == nil {
					consider(b)
				}
				if dir == root || dir == "/" || dir == "." {
					break
				}
			}
		case strings.Contains(parts[1], "memory"): // cgroup v1
			for dir := filepath.Join(root, "memory", parts[2]); ; dir = filepath.Dir(dir) {
				if b, err := os.ReadFile(filepath.Join(dir, "memory.limit_in_bytes")); err == nil {
					consider(b)
				}
				if dir == filepath.Join(root, "memory") || dir == "/" || dir == "." {
					break
				}
			}
		}
	}
	return best
}

func (linuxProc) Snapshot() (procTable, error) {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	table := make(procTable, len(ents))
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if pe, ok := readStat(pid); ok {
			table[pid] = pe
		}
	}
	return table, nil
}

// readStat parses /proc/<pid>/stat. The comm field may contain spaces and
// parentheses, so everything after the LAST ')' is split on whitespace.
func readStat(pid int) (procEntry, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return procEntry{}, false
	}
	return parseStat(b)
}

func parseStat(b []byte) (procEntry, bool) {
	l, r := bytes.IndexByte(b, '('), bytes.LastIndexByte(b, ')')
	if l < 0 || r < l {
		return procEntry{}, false
	}
	f := strings.Fields(string(b[r+1:]))
	// f[0]=state f[1]=ppid f[2]=pgrp ... f[19]=starttime
	if len(f) < 20 {
		return procEntry{}, false
	}
	ppid, e1 := strconv.Atoi(f[1])
	pgrp, e2 := strconv.Atoi(f[2])
	start, e3 := strconv.ParseUint(f[19], 10, 64)
	if e1 != nil || e2 != nil || e3 != nil {
		return procEntry{}, false
	}
	return procEntry{PPID: ppid, PGRP: pgrp, Start: start, Comm: string(b[l+1 : r])}, true
}

// Anon sums RssAnon + RssShmem + VmSwap from /proc/<pid>/status.
func (linuxProc) Anon(pid int) (uint64, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return 0, false
	}
	return sumKB(b, "RssAnon:", "RssShmem:", "VmSwap:")
}

// AnonExact sums Pss_Anon + Pss_Shmem + SwapPss from /proc/<pid>/smaps_rollup.
func (linuxProc) AnonExact(pid int) (uint64, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/smaps_rollup")
	if err != nil {
		return 0, false
	}
	return sumKB(b, "Pss_Anon:", "Pss_Shmem:", "SwapPss:")
}

// sumKB adds the values (in kB) of the named "Key:   123 kB" lines and returns
// bytes. ok is false when none of the keys is present.
func sumKB(b []byte, keys ...string) (uint64, bool) {
	var total uint64
	found := false
	for _, line := range strings.Split(string(b), "\n") {
		for _, k := range keys {
			if strings.HasPrefix(line, k) {
				f := strings.Fields(line[len(k):])
				if len(f) > 0 {
					if n, err := strconv.ParseUint(f[0], 10, 64); err == nil {
						total += n << 10
						found = true
					}
				}
				break
			}
		}
	}
	return total, found
}

// HasTag looks for "<memTagEnv>=<tag>" among the NUL-separated environment.
func (linuxProc) HasTag(pid int, tag string) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return false
	}
	return bytes.Contains(b, []byte("\x00"+memTagEnv+"="+tag+"\x00")) ||
		bytes.HasPrefix(b, []byte(memTagEnv+"="+tag+"\x00"))
}
