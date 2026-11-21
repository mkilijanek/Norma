package llm

import (
	"strings"
	"testing"
)

func TestReadBodyLimit(t *testing.T) {
	if b, err := readBody(strings.NewReader("abc"), 3); err != nil || string(b) != "abc" {
		t.Fatalf("at limit: %q %v", b, err)
	}
	if _, err := readBody(strings.NewReader("abcd"), 3); err == nil {
		t.Fatal("over limit must error")
	}
}
