//go:build windows

package tool

import (
	"os"
	"os/exec"
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
