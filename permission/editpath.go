package permission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// editPathAllowed reports whether an edit tool call may be auto-allowed under
// acceptEdits: its target must resolve (symlinks followed) to a path strictly
// inside the working directory (the process cwd when none is set), and must not
// touch a ".git" component, whose hooks and config run code. Anything it cannot
// verify (no path, unresolvable cwd) is not auto-allowed and goes to the normal
// approval flow.
func editPathAllowed(input json.RawMessage, workingDir string) bool {
	var in struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	}
	if json.Unmarshal(input, &in) != nil {
		return false
	}
	p := in.FilePath
	if p == "" {
		p = in.NotebookPath
	}
	if p == "" {
		return false
	}
	wd := workingDir
	if wd == "" {
		var err error
		if wd, err = os.Getwd(); err != nil {
			return false
		}
	}
	wdReal, err := filepath.EvalSymlinks(wd)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(wd, p)
	}
	real := resolveThroughExisting(filepath.Clean(p))
	rel, err := filepath.Rel(wdReal, real)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if strings.EqualFold(part, ".git") {
			return false
		}
	}
	return true
}

// resolveThroughExisting resolves symlinks in the longest existing prefix of p
// and re-appends the not-yet-existing remainder, so a file about to be created
// under a symlinked directory is judged by where it will really land.
func resolveThroughExisting(p string) string {
	rest := ""
	for cur := p; ; {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}
