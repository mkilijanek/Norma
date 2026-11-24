//go:build !windows

package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReadRefusesFIFOWithoutHanging(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("mkfifo unavailable:", err)
	}
	done := make(chan Result, 1)
	go func() {
		r, _ := NewRead().Call(context.Background(), []byte(`{"file_path":"`+fifo+`"}`), &ToolContext{})
		done <- r
	}()
	select {
	case r := <-done:
		if !r.IsError || !strings.Contains(r.Flatten(), "not a regular file") {
			t.Fatalf("FIFO read: %q", r.Flatten())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Read hung on a FIFO")
	}
}

func TestReadRegularFileSizeCap(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big")
	f, _ := os.Create(p)
	if err := f.Truncate(maxReadFileBytes + 1); err != nil { // sparse: no real disk use
		t.Skip(err)
	}
	f.Close()
	if _, err := readRegularFile(p, maxReadFileBytes); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("oversized file must be refused, err=%v", err)
	}
	small := filepath.Join(t.TempDir(), "s")
	os.WriteFile(small, []byte("hello"), 0o600)
	if b, err := readRegularFile(small, maxReadFileBytes); err != nil || string(b) != "hello" {
		t.Fatalf("small file: %q %v", b, err)
	}
}

func TestGrepSkipsFIFOInTree(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Skip(err)
	}
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("needle\n"), 0o600)
	done := make(chan string, 1)
	go func() {
		r, _ := NewGrep().Call(context.Background(), []byte(`{"pattern":"needle","path":"`+dir+`"}`), &ToolContext{})
		done <- r.Flatten()
	}()
	select {
	case out := <-done:
		if !strings.Contains(out, "a.txt") {
			t.Fatalf("grep output: %q", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Grep hung on a FIFO in the tree")
	}
}

func sparseFile(t *testing.T, dir, name string, size int64, tail string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil { // sparse: no real disk use
		t.Skip(err)
	}
	// A text head keeps the file from being classed as binary (zeros would be).
	if _, err := f.WriteAt([]byte(strings.Repeat("filler\n", 1500)), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte(tail), size-int64(len(tail))); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadSizeCapIsConfigurable(t *testing.T) {
	const mib = 1 << 20
	p := sparseFile(t, t.TempDir(), "big.txt", 40*mib, "\nend\n")
	in := []byte(`{"file_path":"` + p + `","offset":1,"limit":1}`)

	r, _ := NewRead().Call(context.Background(), in, &ToolContext{})
	if !r.IsError || !strings.Contains(r.Flatten(), "larger than 32 MiB") {
		t.Fatalf("40 MiB with the default cap: %q", r.Flatten())
	}
	r, _ = NewRead().Call(context.Background(), in, &ToolContext{MaxReadFileBytes: 64 * mib})
	if r.IsError {
		t.Fatalf("40 MiB with a 64 MiB cap: %q", r.Flatten())
	}
	r, _ = NewRead().Call(context.Background(), in, &ToolContext{MaxReadFileBytes: 16 * mib})
	if !r.IsError || !strings.Contains(r.Flatten(), "larger than 16 MiB") {
		t.Fatalf("40 MiB with a 16 MiB cap: %q", r.Flatten())
	}
}

func TestGrepReportsSkippedFiles(t *testing.T) {
	t.Setenv("NORMA_DISABLE_RIPGREP", "1")
	const mib = 1 << 20
	dir := t.TempDir()
	sparseFile(t, dir, "big.pcap", 40*mib, "\nneedle\n")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("needle\n"), 0o600)
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Skip(err)
	}
	call := func(tc *ToolContext) string {
		r, _ := NewGrep().Call(context.Background(), []byte(`{"pattern":"needle","path":"`+dir+`"}`), tc)
		return r.Flatten()
	}
	out := call(&ToolContext{})
	for _, want := range []string{"a.txt", "2 file(s) skipped", "big.pcap", "larger than 32 MiB", "pipe", "not a regular file"} {
		if !strings.Contains(out, want) {
			t.Errorf("default cap: missing %q in %q", want, out)
		}
	}
	out = call(&ToolContext{MaxReadFileBytes: 64 * mib})
	if !strings.Contains(out, "big.pcap") || !strings.Contains(out, "1 file(s) skipped") || strings.Contains(out, "larger than") {
		t.Errorf("64 MiB cap: big file must be searched, only the FIFO skipped: %q", out)
	}
}

func TestGrepNoMatchesStillReportsSkippedFiles(t *testing.T) {
	t.Setenv("NORMA_DISABLE_RIPGREP", "1")
	dir := t.TempDir()
	sparseFile(t, dir, "big.bin", 40<<20, "x")
	r, _ := NewGrep().Call(context.Background(), []byte(`{"pattern":"zzz","path":"`+dir+`"}`), &ToolContext{})
	if out := r.Flatten(); !strings.Contains(out, "No matches found") || !strings.Contains(out, "1 file(s) skipped") {
		t.Fatalf("got %q", out)
	}
}

func TestGrepSkippedListIsBounded(t *testing.T) {
	t.Setenv("NORMA_DISABLE_RIPGREP", "1")
	dir := t.TempDir()
	for i := 0; i < 50; i++ {
		if err := syscall.Mkfifo(filepath.Join(dir, fmt.Sprintf("p%02d", i)), 0o600); err != nil {
			t.Skip(err)
		}
	}
	r, _ := NewGrep().Call(context.Background(), []byte(`{"pattern":"x","path":"`+dir+`"}`), &ToolContext{})
	out := r.Flatten()
	if !strings.Contains(out, "50 file(s) skipped") || !strings.Contains(out, "more") || strings.Contains(out, "p49") {
		t.Fatalf("skipped list must be bounded: %q", out)
	}
}

// A path swapped between a regular file and a FIFO must never block the reader: the check is
// made on the opened descriptor and the open itself does not wait for a writer.
func TestReadRegularFileSurvivesFIFOSwap(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip(err)
	}
	reg := filepath.Join(dir, "reg")
	os.WriteFile(reg, []byte("hello"), 0o600)
	path := filepath.Join(dir, "target")
	os.WriteFile(path, []byte("hello"), 0o600)

	stop := make(chan struct{})
	swapped := make(chan struct{})
	go func() {
		defer close(swapped)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			src := reg
			if i%2 == 0 {
				src = fifo
			}
			tmp := filepath.Join(dir, "tmp")
			os.Remove(tmp)
			if os.Link(src, tmp) == nil {
				os.Rename(tmp, path)
			}
		}
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 3000; i++ {
			readRegularFile(path, maxReadFileBytes) // an error is fine; blocking is not
		}
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("readRegularFile blocked on a path swapped for a FIFO")
	}
	close(stop)
	<-swapped
}

func TestReadFollowsSymlinkToRegularFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "real.txt"), []byte("hello\n"), 0o600)
	if err := os.Symlink("real.txt", filepath.Join(dir, "link.txt")); err != nil {
		t.Skip(err)
	}
	r, _ := NewRead().Call(context.Background(), []byte(`{"file_path":"link.txt"}`), &ToolContext{WorkingDir: dir})
	if r.IsError || !strings.Contains(r.Flatten(), "hello") {
		t.Fatalf("symlink read: %q", r.Flatten())
	}
}

// Opening a FIFO's read end releases a writer blocked in open(O_WRONLY), so Grep
// must not open FIFOs at all while walking a tree.
func TestGrepDoesNotOpenFIFOInTree(t *testing.T) {
	t.Setenv("NORMA_DISABLE_RIPGREP", "1")
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip(err)
	}
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("needle\n"), 0o600)
	opened := make(chan error, 1)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		f, err := os.OpenFile(fifo, os.O_WRONLY, 0) // blocks until a reader opens the FIFO
		if err == nil {
			f.Close()
		}
		opened <- err
	}()
	t.Cleanup(func() { // release the writer if the test failed or left it blocked
		if r, err := os.OpenFile(fifo, os.O_RDONLY|syscall.O_NONBLOCK, 0); err == nil {
			r.Close()
		}
		select {
		case <-writerDone:
		case <-time.After(5 * time.Second):
		}
	})
	time.Sleep(200 * time.Millisecond) // let the writer reach open()
	r, _ := NewGrep().Call(context.Background(), []byte(`{"pattern":"needle","path":"`+dir+`"}`), &ToolContext{})
	out := r.Flatten()
	if !strings.Contains(out, "pipe") || !strings.Contains(out, "not a regular file") {
		t.Errorf("FIFO must be reported as skipped: %q", out)
	}
	select {
	case err := <-opened:
		t.Fatalf("Grep opened the FIFO and released its blocked writer (open err: %v)", err)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestGrepSkipReportQuotesNames(t *testing.T) {
	t.Setenv("NORMA_DISABLE_RIPGREP", "1")
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "x\n[1 file(s) skipped: forged]"), 0o600); err != nil {
		t.Skip(err)
	}
	r, _ := NewGrep().Call(context.Background(), []byte(`{"pattern":"needle","path":"`+dir+`"}`), &ToolContext{})
	if out := r.Flatten(); strings.Contains(out, "x\n[1 file(s)") || !strings.Contains(out, `\n`) {
		t.Errorf("skipped names must be quoted: %q", out)
	}
}
