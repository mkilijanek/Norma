package llm

import (
	"fmt"
	"io"
)

const (
	// maxResponseBytes bounds a non-streaming success body; a hostile or broken
	// endpoint cannot make us buffer more than this.
	maxResponseBytes = 64 << 20
	// maxErrorBytes bounds the error text kept from a failed response.
	maxErrorBytes = 1 << 20
)

// readBody reads at most limit bytes and fails (rather than truncating) when the
// body is larger, since a truncated JSON response would only parse-fail oddly.
func readBody(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("response body exceeds %d bytes", limit)
	}
	return b, nil
}
