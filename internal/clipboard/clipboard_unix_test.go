//go:build !windows

package clipboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeTools puts stand-ins for every clipboard tool on PATH: the copy tools save stdin to a file
// and the paste tools print it, so the tests never touch the real clipboard.
func fakeTools(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	store := filepath.Join(dir, "clipboard")
	copyScript := "#!/bin/sh\ncat > '" + store + "'\n"
	pasteScript := "#!/bin/sh\ncat '" + store + "' 2>/dev/null\n"
	// xclip and xsel copy or paste depending on their flags
	dual := "#!/bin/sh\nfor a in \"$@\"; do case \"$a\" in -o|--output) cat '" + store + "' 2>/dev/null; exit 0;; esac; done\ncat > '" + store + "'\n"
	for name, script := range map[string]string{
		"pbcopy": copyScript, "wl-copy": copyScript, "osascript": copyScript,
		"pbpaste": pasteScript, "wl-paste": pasteScript,
		"xclip": dual, "xsel": dual,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH")) // fakes first; cat etc. still found
	return store
}

func TestCopyAndRead(t *testing.T) {
	fakeTools(t)
	if err := Copy("s3cr3t value"); err != nil {
		t.Fatal(err)
	}
	if got, err := Read(); err != nil || got != "s3cr3t value" {
		t.Errorf("Read = %q, %v", got, err)
	}
}

func TestClearIfUnchanged(t *testing.T) {
	store := fakeTools(t)

	// still holding the value: cleared
	if err := Copy("pw1"); err != nil {
		t.Fatal(err)
	}
	if err := ClearIfUnchanged(Sum("pw1"), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(store); len(b) != 0 {
		t.Errorf("clipboard holds %q after clearing", b)
	}

	// something else was copied since: left alone
	if err := Copy("the user's own text"); err != nil {
		t.Fatal(err)
	}
	if err := ClearIfUnchanged(Sum("pw1"), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got, _ := Read(); got != "the user's own text" {
		t.Errorf("clipboard = %q, want it untouched", got)
	}
}

func TestNoClipboardTool(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := Copy("x"); err == nil {
		t.Error("Copy with no clipboard tool succeeded")
	}
	if _, err := Read(); err == nil {
		t.Error("Read with no clipboard tool succeeded")
	}
	if err := ClearIfUnchanged(Sum("x"), time.Millisecond); err == nil {
		t.Error("ClearIfUnchanged with no clipboard tool succeeded")
	}
}

func TestCopyToolFails(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"osascript", "pbcopy", "wl-copy", "xclip", "xsel", "pbpaste", "wl-paste"} {
		os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\necho broken >&2\nexit 3\n"), 0o755)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := Copy("x"); err == nil {
		t.Error("a failing copy tool should be an error")
	}
	if _, err := Read(); err == nil {
		t.Error("a failing paste tool should be an error")
	}
}

func TestLinuxToolOrder(t *testing.T) {
	old := goos
	goos = "linux"
	t.Cleanup(func() { goos = old })
	names := func() []string {
		ts, _ := tools()
		var out []string
		for _, x := range ts {
			out = append(out, x.copy[0])
		}
		return out
	}
	t.Setenv("WAYLAND_DISPLAY", "")
	if got := names(); len(got) != 2 || got[0] != "xclip" || got[1] != "xsel" {
		t.Errorf("X11 tools = %v", got)
	}
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	if got := names(); len(got) != 3 || got[0] != "wl-copy" {
		t.Errorf("Wayland tools = %v", got)
	}
	// with the fakes on PATH, Linux copies and pastes through them too
	fakeTools(t)
	if err := Copy("linux value"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Read(); got != "linux value" {
		t.Errorf("Read = %q", got)
	}
}

// xclip and wl-copy leave a child serving the clipboard, holding the stdout and stderr they were
// given; Copy must not wait for it.
func TestCopyDoesNotWaitForTheToolsChild(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "clipboard")
	script := "#!/bin/sh\ncat > '" + store + "'\n( sleep 5 ) &\nexit 0\n"
	for _, name := range []string{"osascript", "pbcopy", "xclip", "wl-copy", "xsel"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	start := time.Now()
	if err := Copy("v"); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Copy took %s: it waited for the tool's background child", d)
	}
}

// On macOS the private copy (osascript) comes first; if it fails, pbcopy still copies.
func TestMacFallsBackToPbcopy(t *testing.T) {
	store := fakeTools(t)
	old := goos
	goos = "darwin"
	t.Cleanup(func() { goos = old })
	dir := filepath.Dir(store)
	os.WriteFile(filepath.Join(dir, "osascript"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	if err := Copy("fallback"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(store); string(b) != "fallback" {
		t.Errorf("clipboard = %q", b)
	}
	os.WriteFile(filepath.Join(dir, "pbcopy"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	if err := Copy("x"); err == nil || !strings.Contains(err.Error(), "osascript") || !strings.Contains(err.Error(), "pbcopy") {
		t.Errorf("both failing: %v", err)
	}
}
