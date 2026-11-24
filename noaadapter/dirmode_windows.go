//go:build windows

package noaadapter

import "os"

// chmodDirNoFollow: Windows has no descriptor-based chmod or symlink-swap
// exposure of this kind; behaviour is unchanged.
func chmodDirNoFollow(dir string, mode os.FileMode) error { return os.Chmod(dir, mode) }
