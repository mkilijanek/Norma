package tool

import (
	"fmt"
	"io"
	"os"
)

// maxReadFileBytes bounds how much of one file Read/Grep will load. A model-
// controlled path (or a hostile file in a repo) must not be able to exhaust
// memory with a huge file or hang a tool call on a FIFO / device.
const maxReadFileBytes = 32 << 20

// readRegularFile reads a regular file of at most maxReadFileBytes. Anything else
// (FIFO, socket, device, directory) or anything larger is refused rather than
// read: os.ReadFile on a FIFO with no writer blocks forever and ignores the
// tool's context. The size limit is enforced while reading, not from Stat, so
// files that report size 0 (e.g. /proc) are bounded too.
func readRegularFile(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file (%s)", fi.Mode().Type())
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxReadFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxReadFileBytes {
		return nil, fmt.Errorf("file larger than %d MiB; use Grep or Bash (sed -n / head) for part of it", maxReadFileBytes>>20)
	}
	return data, nil
}
