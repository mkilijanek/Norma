//go:build !windows

package transcript

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTranscriptFilesArePrivate(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "t"))
	p := s.MainPath("sess")
	if err := s.Append(p, Record{}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{p: 0o600, filepath.Dir(p): 0o700} {
		fi, err := os.Stat(path)
		if err != nil || fi.Mode().Perm() != want {
			t.Fatalf("%s: %v %v, want %v", path, fi.Mode().Perm(), err, want)
		}
	}
}
