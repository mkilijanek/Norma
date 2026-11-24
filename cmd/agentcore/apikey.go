package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// resolveAPIKey picks the API key from at most one explicit source. Passing the
// key as an argument leaves it in the process list (ps, /proc/<pid>/cmdline) and
// shell history, so -api-key still works but warns; -api-key-file and
// -api-key-stdin keep it out of both. With no source the empty string is
// returned and the provider falls back to its environment variable.
func resolveAPIKey(flagKey, file string, fromStdin bool, stdin io.Reader, warn io.Writer) (string, error) {
	n := 0
	for _, set := range []bool{flagKey != "", file != "", fromStdin} {
		if set {
			n++
		}
	}
	if n > 1 {
		return "", errors.New("use only one of -api-key, -api-key-file, -api-key-stdin")
	}
	switch {
	case flagKey != "":
		fmt.Fprintln(warn, "warning: -api-key exposes the key in the process list and shell history; prefer the environment variable, -api-key-file or -api-key-stdin")
		return flagKey, nil
	case file != "":
		if fi, err := os.Stat(file); err == nil && fi.Mode().Perm()&0o077 != 0 {
			fmt.Fprintf(warn, "warning: %s is accessible to other users (mode %o); chmod 600 it\n", file, fi.Mode().Perm())
		}
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("reading -api-key-file: %w", err)
		}
		return nonEmptyKey(string(b), "-api-key-file")
	case fromStdin:
		b, err := io.ReadAll(io.LimitReader(stdin, 64<<10))
		if err != nil {
			return "", fmt.Errorf("reading -api-key-stdin: %w", err)
		}
		return nonEmptyKey(string(b), "-api-key-stdin")
	}
	return "", nil
}

func nonEmptyKey(s, src string) (string, error) {
	k := strings.TrimSpace(s)
	if k == "" {
		return "", fmt.Errorf("%s: no key found", src)
	}
	if strings.ContainsAny(k, "\r\n") {
		return "", fmt.Errorf("%s: key must be a single line", src)
	}
	return k, nil
}
