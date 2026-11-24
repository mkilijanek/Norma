//go:build windows

package tool

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

// createNewProcessGroup isolates the child from our console so it survives the
// per-turn context.
const createNewProcessGroup = 0x00000200 // CREATE_NEW_PROCESS_GROUP

func setDetached(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroup
}

// treeKill terminates the process and its descendants via taskkill.
func treeKill(p *os.Process) {
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(p.Pid)).Run()
}

// Windows: no ownership/symlink semantics to check here; per-user %TEMP already applies.
const outputFlags = os.O_CREATE | os.O_EXCL | os.O_WRONLY

func taskBaseName() string { return "norma" }

func secureDir(dir string) error { return os.MkdirAll(dir, 0o700) }

// checkHostDir verifies a host-supplied base is an existing directory and not
// a symlink. Windows has no ownership check here.
func checkHostDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("task dir: %w", err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("task dir %s is not a directory (symlink?)", dir)
	}
	return nil
}

// ensureDirMode makes dir (one level). Windows does not honour Unix modes.
func ensureDirMode(dir string, mode os.FileMode) error {
	if err := os.Mkdir(dir, mode); err != nil && !os.IsExist(err) {
		return err
	}
	return checkHostDir(dir)
}

// isOwnSessionDir reports whether dir looks like a session directory: a real
// directory with a real "tasks" directory inside (no ownership check on Windows).
func isOwnSessionDir(dir string) bool {
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() {
		return false
	}
	ti, err := os.Lstat(filepath.Join(dir, "tasks"))
	return err == nil && ti.IsDir()
}
