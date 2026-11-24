package permission

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// readTools are the read-only built-ins that take a filesystem path.
var readTools = map[string]bool{"Read": true, "Grep": true, "Glob": true}

var procSecretRe = regexp.MustCompile(`^/proc/[^/]+/(environ|mem)$`)

// sensitiveDirs are path components whose contents hold credentials.
var sensitiveDirs = map[string]bool{
	".ssh": true, ".aws": true, ".gnupg": true, ".kube": true, ".azure": true,
}

// sensitiveFiles are base names that hold credentials wherever they live.
var sensitiveFiles = map[string]bool{
	".netrc": true, ".git-credentials": true, ".npmrc": true, ".pypirc": true,
	".pgpass": true,
}

// sensitivePath reports whether a resolved path names a well-known credential
// store. These always need approval for Read/Grep/Glob, even when the path is
// inside the working directory, because reading them hands the model secrets
// that env scrubbing and tool allowlists are meant to keep out of reach.
func sensitivePath(p string) bool {
	p = filepath.ToSlash(filepath.Clean(p))
	if procSecretRe.MatchString(p) {
		return true
	}
	if p == "/etc/shadow" || p == "/etc/gshadow" || strings.HasPrefix(p, "/etc/sudoers") {
		return true
	}
	parts := strings.Split(p, "/")
	for i, part := range parts {
		lp := strings.ToLower(part)
		if sensitiveDirs[lp] {
			return true
		}
		if lp == "gcloud" && i > 0 && strings.EqualFold(parts[i-1], ".config") {
			return true
		}
		if lp == "config.json" && i > 0 && strings.EqualFold(parts[i-1], ".docker") {
			return true
		}
	}
	base := strings.ToLower(parts[len(parts)-1])
	if sensitiveFiles[base] {
		return true
	}
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		for _, ok := range []string{".example", ".sample", ".template", ".dist"} {
			if strings.HasSuffix(base, ok) {
				return false
			}
		}
		return true
	}
	return false
}

// readNeedsApproval reports whether a Read/Grep/Glob call must go through the
// approval flow instead of being auto-allowed as read-only: its target (symlinks
// resolved) is a sensitive credential path, or, with confine set, lies outside
// the working directory. A call whose path cannot be determined is not held back
// (an empty path means the working directory); unresolvable working directories
// are held back only when confining.
func readNeedsApproval(toolName string, input json.RawMessage, workingDir string, confine bool) bool {
	if !readTools[toolName] {
		return false
	}
	var in struct {
		FilePath string `json:"file_path"`
		Path     string `json:"path"`
	}
	if json.Unmarshal(input, &in) != nil {
		return confine
	}
	p := in.FilePath
	if p == "" {
		p = in.Path
	}
	wd := workingDir
	if wd == "" {
		wd, _ = os.Getwd()
	}
	if p == "" {
		return false // searches the working directory itself
	}
	if !filepath.IsAbs(p) && wd != "" {
		p = filepath.Join(wd, p)
	}
	p = filepath.Clean(p)
	real := resolveThroughExisting(p)
	if sensitivePath(p) || sensitivePath(real) {
		return true
	}
	if !confine {
		return false
	}
	wdReal, err := filepath.EvalSymlinks(wd)
	if err != nil {
		return true
	}
	rel, err := filepath.Rel(wdReal, real)
	return err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
