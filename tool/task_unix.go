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

// checkHostDir verifies a host-supplied base is an existing real directory (not
// a symlink) owned by this user. It never creates or chmods it. A base that
// group or others can write to is refused unless the sticky bit is set: without
// it another user could swap the session directory for a symlink.
func checkHostDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("task dir: %w", err)
	}
	if err := checkOwnedDir(dir, fi); err != nil {
		return err
	}
	if fi.Mode().Perm()&0o022 != 0 && fi.Mode()&os.ModeSticky == 0 {
		return fmt.Errorf("task dir %s is writable by group or others without the sticky bit (mode %04o); make it 0700/0755 or set the sticky bit", dir, uint32(fi.Mode().Perm()))
	}
	return nil
}

func checkOwnedDir(dir string, fi os.FileInfo) error {
	if !fi.IsDir() {
		return fmt.Errorf("task dir %s is not a directory (symlink?)", dir)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Getuid() {
		return fmt.Errorf("task dir %s is not owned by the current user", dir)
	}
	return nil
}

// ensureDirMode makes dir (one level, its parent already checked) with exactly
// mode. An existing dir must be a real directory owned by this user and is
// brought to mode; a symlink is refused rather than followed. The directory is
// opened O_NOFOLLOW and checked and chmod-ed through the descriptor, so a swap
// after the Mkdir cannot redirect the chmod.
func ensureDirMode(dir string, mode os.FileMode) error {
	if err := os.Mkdir(dir, mode); err != nil && !os.IsExist(err) {
		return err
	}
	fd, err := syscall.Open(dir, syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("task dir %s is not a directory (symlink?): %w", dir, err)
	}
	defer syscall.Close(fd)
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		return fmt.Errorf("task dir %s is not a directory (symlink?)", dir)
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("task dir %s is not owned by the current user", dir)
	}
	if os.FileMode(st.Mode).Perm() != mode {
		return syscall.Fchmod(fd, uint32(mode)) // also corrects the umask's effect on Mkdir
	}
	return nil
}
