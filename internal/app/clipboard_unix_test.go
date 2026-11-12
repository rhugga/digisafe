//go:build !windows

package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rhugga/digisafe/internal/clipboard"
)

// The real clipboard wrappers, against fake clipboard tools on PATH.
func TestSystemClipboard(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "clip")
	for name, script := range map[string]string{
		"pbcopy": "#!/bin/sh\ncat > '" + store + "'\n", "pbpaste": "#!/bin/sh\ncat '" + store + "'\n",
		"osascript": "#!/bin/sh\ncat > '" + store + "'\n", // the private macOS copy; never the real one
		"xclip":     "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = -o ] && { cat '" + store + "'; exit 0; }; done\ncat > '" + store + "'\n",
	} {
		os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WAYLAND_DISPLAY", "")

	c := systemClipboard{}
	if err := c.Copy("pw"); err != nil {
		t.Fatal(err)
	}
	if err := c.ClearIfUnchanged(clipboard.Sum("pw"), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(store); len(b) != 0 {
		t.Errorf("clipboard holds %q after clearing", b)
	}
}
