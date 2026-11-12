package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContext(t *testing.T) {
	h := newHarness(t)
	work := filepath.Join(h.dir, "work.age")

	// no context: the default
	if out := h.must(nil, "context", "show"); !strings.Contains(out, "no context: using the default "+filepath.Join(h.dir, "mysafe.age")+" (doesn't exist yet)") {
		t.Errorf("show: %q", out)
	}
	h.mustFail(nil, "no safe at", "context", "set", "work")

	// create two safes; --use makes work current (they share the directory's key)
	h.must([]string{pass, pass}, "safe", "create")
	if out := h.must(nil, "s", "c", "work", "--use"); !strings.Contains(out, "uses the existing key") || !strings.Contains(out, "context: "+work) {
		t.Errorf("create --use: %q", out)
	}
	if out := h.must(nil, "ctx", "show"); !strings.Contains(out, "context: "+work) {
		t.Errorf("show: %q", out)
	}

	// commands now use work.age: the passphrase prompt and the listing say so
	h.must([]string{pass, "Work thing", "", "", "x", "", ""}, "r", "a", "note")
	if !strings.Contains(strings.Join(h.ui.Prompts, "\n"), "passphrase for work.age: ") {
		t.Errorf("prompts don't name the safe: %q", h.ui.Prompts)
	}
	h.must([]string{pass}, "ls")
	if !strings.Contains(strings.Join(h.ui.Messages, "\n"), "safe: "+work) {
		t.Errorf("record list doesn't say which safe: %q", h.ui.Messages)
	}
	if out := h.must([]string{pass}, "--safe", "mysafe", "record", "list"); strings.Contains(out, "Work thing") {
		t.Errorf("--safe should override the context:\n%s", out)
	}
	t.Setenv("DIGISAFE_SAFE", "mysafe")
	if out := h.must(nil, "context", "show"); !strings.Contains(out, "--safe: "+filepath.Join(h.dir, "mysafe.age")) {
		t.Errorf("DIGISAFE_SAFE should override the context: %q", out)
	}
	os.Unsetenv("DIGISAFE_SAFE")

	// safe list marks the current one
	out := h.must(nil, "safe", "list")
	if !strings.Contains(out, "*  work.age") || !strings.Contains(out, "mysafe.age") || strings.Contains(out, "identity.age") {
		t.Errorf("safe list:\n%s", out)
	}

	// set back to mysafe by name, then clear
	if out := h.must(nil, "context", "set", "mysafe"); !strings.Contains(out, filepath.Join(h.dir, "mysafe.age")) {
		t.Errorf("set: %q", out)
	}
	if out := h.must(nil, "context", "clear"); !strings.Contains(out, "context cleared") {
		t.Errorf("clear: %q", out)
	}
	if out := h.must(nil, "context", "clear"); !strings.Contains(out, "no context was set") {
		t.Errorf("second clear: %q", out)
	}
	h.must(nil, "safe", "list")
	if !strings.Contains(strings.Join(h.ui.Messages, "\n"), "no context set") {
		t.Errorf("safe list with no context: %q", h.ui.Messages)
	}
	h.mustFail(nil, "usage", "context", "set")
	h.mustFail(nil, "usage", "safe", "create", "a", "b")
}

// A context to a safe that's since been deleted says so, rather than "run safe create".
func TestContextToAMissingSafe(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create", "gone", "--use")
	os.Remove(filepath.Join(h.dir, "gone.age"))
	h.mustFail(nil, "the context points to", "record", "list")
	if out := h.must(nil, "context", "show"); !strings.Contains(out, "(doesn't exist yet)") {
		t.Errorf("show: %q", out)
	}
	os.WriteFile(filepath.Join(h.dir, "context"), []byte("\n"), 0o600) // an empty context is no context
	if out := h.must(nil, "context", "show"); !strings.Contains(out, "no context") {
		t.Errorf("empty context file: %q", out)
	}
}

// A safe outside the base directory keeps its own key beside it, e.g. on a USB stick.
func TestSafeElsewhereHasItsOwnKey(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	usb := filepath.Join(t.TempDir(), "usb")
	family := filepath.Join(usb, "family.age")
	other := "another long passphrase"
	if out := h.must([]string{other, other}, "safe", "create", family, "--use"); !strings.Contains(out, filepath.Join(usb, "identity.age")) {
		t.Errorf("create elsewhere: %q", out)
	}
	for _, f := range []string{"identity.age", "identity.pub", "family.age"} {
		if _, err := os.Stat(filepath.Join(usb, f)); err != nil {
			t.Errorf("%s not beside the safe: %v", f, err)
		}
	}
	// it opens with its own passphrase, not the base directory's
	h.must([]string{other}, "record", "list")
	h.mustFail([]string{pass, pass, pass}, "wrong passphrase", "record", "list")
	// safe list shows it, though it's outside the base directory
	if out := h.must(nil, "safe", "list"); !strings.Contains(out, "*  family.age") {
		t.Errorf("safe list:\n%s", out)
	}
}

func TestEmptySafeList(t *testing.T) {
	h := newHarness(t)
	h.must(nil, "safe", "list")
	if !strings.Contains(strings.Join(h.ui.Messages, "\n"), "no safes in") {
		t.Errorf("messages: %q", h.ui.Messages)
	}
}
