package tool

import (
	"fmt"
	"io"
	"os"
	"syscall"
)

// maxReadFileBytes is the default bound on how much of one file Read/Grep will
// load. A model-controlled path (or a hostile file in a repo) must not be able
// to exhaust memory with a huge file or hang a tool call on a FIFO / device.
// A host that must handle larger files sets ToolContext.MaxReadFileBytes.
const maxReadFileBytes = 32 << 20

// readFileLimit is the size cap for this call: the host's MaxReadFileBytes, or
// the default when it is unset.
func (tc *ToolContext) readFileLimit() int64 {
	if tc != nil && tc.MaxReadFileBytes > 0 {
		return tc.MaxReadFileBytes
	}
	return maxReadFileBytes
}

// refusedFile is the error for a file readRegularFile declines to read (as
// opposed to one it failed to read). Grep reports these instead of dropping the
// file silently.
type refusedFile struct{ msg string }

func (e *refusedFile) Error() string { return e.msg }

// readRegularFile reads a regular file of at most maxBytes. Anything else
// (FIFO, socket, device, directory) or anything larger is refused rather than
// read: os.ReadFile on a FIFO with no writer blocks forever and ignores the
// tool's context.
//
// The file is opened first (O_NONBLOCK, so opening a FIFO with no writer
// returns at once; O_NOCTTY, so a terminal device cannot become the controlling
// terminal) and then checked through the descriptor, so a path swapped
// between a check and the open cannot be misjudged. A symlink is followed, as
// Read always did; the check applies to what it points at. The size limit is
// also enforced while reading, so files that report size 0 (e.g. /proc) are
// bounded too.
func readRegularFile(path string, maxBytes int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, &refusedFile{fmt.Sprintf("not a regular file (%s)", fi.Mode().Type())}
	}
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, &refusedFile{fmt.Sprintf("file larger than %d MiB; use Grep or Bash (sed -n / head) for part of it", maxBytes>>20)}
	}
	return data, nil
}
