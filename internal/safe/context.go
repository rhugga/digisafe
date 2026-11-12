package safe

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ContextFile is the file in the base directory (~/.digisafe) naming the current safe.
const ContextFile = "context"

// ReadContext returns the current safe's path, if a context is set in dir.
func ReadContext(dir string) (path string, ok bool, err error) {
	b, err := os.ReadFile(filepath.Join(dir, ContextFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	path = strings.TrimSpace(string(b))
	if path == "" {
		return "", false, nil
	}
	return path, true, nil
}

// SetContext makes safePath the current safe for dir. The safe must exist; the path is stored
// absolute, so the context holds whatever the working directory.
func SetContext(dir, safePath string) (string, error) {
	if err := CheckSafeName(safePath); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(safePath)
	if err != nil {
		return "", err
	}
	if st, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("no safe at %s: %w", abs, err)
	} else if st.IsDir() {
		return "", fmt.Errorf("%s is a directory, not a safe", abs)
	}
	if _, err := EnsureDir(dir); err != nil {
		return "", err
	}
	return abs, WriteAtomic(filepath.Join(dir, ContextFile), []byte(abs+"\n"), false)
}

// ClearContext removes dir's context; it reports whether one was set.
func ClearContext(dir string) (bool, error) {
	err := os.Remove(filepath.Join(dir, ContextFile))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// ListSafes lists the safes in dir (*.age, not the key file), sorted.
func ListSafes(dir string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.age"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range matches {
		if filepath.Base(m) != "identity.age" && !strings.HasPrefix(filepath.Base(m), ".") {
			out = append(out, m)
		}
	}
	return out, nil
}
