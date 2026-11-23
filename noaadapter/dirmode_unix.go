//go:build !windows

package noaadapter

import (
	"fmt"
	"os"
	"syscall"
)

// chmodDirNoFollow sets dir's mode through a descriptor opened O_NOFOLLOW, so a
// symlink swapped in after the directory was created is refused, not followed.
// The directory must be owned by the current user.
func chmodDirNoFollow(dir string, mode os.FileMode) error {
	fd, err := syscall.Open(dir, syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", dir, err)
	}
	defer syscall.Close(fd)
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s is not owned by the current user", dir)
	}
	return syscall.Fchmod(fd, uint32(mode))
}
