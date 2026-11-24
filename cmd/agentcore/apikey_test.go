package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveAPIKey(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "k")
	os.WriteFile(good, []byte("  sk-file\n"), 0o600)
	open := filepath.Join(dir, "open")
	os.WriteFile(open, []byte("sk-open"), 0o644)
	empty := filepath.Join(dir, "empty")
	os.WriteFile(empty, []byte("\n"), 0o600)
	multi := filepath.Join(dir, "multi")
	os.WriteFile(multi, []byte("a\nb\n"), 0o600)

	cases := []struct {
		name     string
		flag, f  string
		stdin    bool
		in       string
		want     string
		wantErr  bool
		wantWarn string
	}{
		{name: "none"},
		{name: "flag", flag: "sk-flag", want: "sk-flag", wantWarn: "process list"},
		{name: "file", f: good, want: "sk-file"},
		{name: "file open perms", f: open, want: "sk-open", wantWarn: "accessible to other users"},
		{name: "stdin", stdin: true, in: "sk-stdin\n", want: "sk-stdin"},
		{name: "missing file", f: filepath.Join(dir, "nope"), wantErr: true},
		{name: "empty file", f: empty, wantErr: true},
		{name: "multiline", f: multi, wantErr: true},
		{name: "empty stdin", stdin: true, wantErr: true},
		{name: "two sources", flag: "x", f: good, wantErr: true},
	}
	for _, c := range cases {
		var warn bytes.Buffer
		got, err := resolveAPIKey(c.flag, c.f, c.stdin, strings.NewReader(c.in), &warn)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("%s: got %q err=%v", c.name, got, err)
		}
		if c.wantWarn != "" && !strings.Contains(warn.String(), c.wantWarn) {
			t.Errorf("%s: warning %q missing %q", c.name, warn.String(), c.wantWarn)
		}
		if c.wantWarn == "" && warn.Len() > 0 {
			t.Errorf("%s: unexpected warning %q", c.name, warn.String())
		}
		if err != nil && strings.Contains(err.Error(), c.want+"x") {
			t.Errorf("%s: error leaks key", c.name)
		}
	}
}
