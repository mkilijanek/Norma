//go:build !windows

package tool

import (
	"context"
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
	if _, err := readRegularFile(p); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("oversized file must be refused, err=%v", err)
	}
	small := filepath.Join(t.TempDir(), "s")
	os.WriteFile(small, []byte("hello"), 0o600)
	if b, err := readRegularFile(small); err != nil || string(b) != "hello" {
		t.Fatalf("small file: %q %v", b, err)
	}
}

func TestGrepSkipsFIFOInTree(t *testing.T) {
	// Exercise the Go fallback deterministically. Without this the first Grep
	// call resolves ripgrep, which on a runner without rg runs `npm install -g`
	// and can exceed the 5s hang detector below.
	t.Setenv("NORMA_DISABLE_RIPGREP", "1")
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
