//go:build !windows

package tool

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// setDetached puts the child in its own process group so it survives the
// per-turn context and can be killed as a whole tree.
func setDetached(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// treeKill terminates the process group, falling back to the lone process.
func treeKill(p *os.Process) {
	if err := syscall.Kill(-p.Pid, syscall.SIGKILL); err != nil {
		_ = p.Kill()
	}
}

// Output files are opened O_EXCL|O_NOFOLLOW so a pre-planted file or symlink is
// never written through.
const outputFlags = os.O_CREATE | os.O_EXCL | os.O_WRONLY | syscall.O_NOFOLLOW

// taskBaseName is per-user so users on a shared /tmp never collide or share.
func taskBaseName() string { return fmt.Sprintf("norma-%d", os.Getuid()) }

// secureDir creates dir (and parents) and verifies the final component is a
// real directory (not a symlink) owned by this user, tightening it to 0700.
func secureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("task dir %s is not a directory (symlink?)", dir)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Getuid() {
		return fmt.Errorf("task dir %s is not owned by the current user", dir)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return os.Chmod(dir, 0o700)
	}
	return nil
}
