package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rhugga/digisafe/internal/safe"
)

const (
	mergeA = "0b6f4f7e-6d1c-4a4e-9a51-6c2f4b1e8a10"
	mergeB = "1b6f4f7e-6d1c-4a4e-9a51-6c2f4b1e8a10"
)

// importInto puts records (JSON) into a safe, keeping their ids and times.
func importInto(h *harness, answers []string, safeName, records string) {
	h.t.Helper()
	h.app.In = strings.NewReader(records)
	h.must(answers, "--safe", safeName, "safe", "import", "-")
}

func TestSafeMerge(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	h.must(nil, "safe", "create", "work") // same directory: same key, one passphrase
	importInto(h, []string{pass}, "mysafe", `[
		{"id":"`+mergeA+`","type":"login","name":"Shared","fields":{"password":"old"},"updated_at":"2026-11-01T00:00:00Z"},
		{"id":"`+mergeB+`","type":"note","name":"Only here","updated_at":"2026-11-01T00:00:00Z"}]`)
	importInto(h, []string{pass}, "work", `[
		{"id":"`+mergeA+`","type":"login","name":"Shared","fields":{"password":"new"},"updated_at":"2026-11-02T00:00:00Z"},
		{"type":"note","name":"Only in work"}]`)

	// --dry-run reports and changes nothing
	backups := len(safe.ExistingBackups(filepath.Join(h.dir, "mysafe.age")))
	out := h.must([]string{pass}, "safe", "merge", "work", "--dry-run")
	if !strings.Contains(out, "would merge") || !strings.Contains(out, "1 added, 1 updated from it") {
		t.Errorf("dry run: %q", out)
	}
	if msgs := strings.Join(h.ui.Messages, "\n"); !strings.Contains(msgs, "add") || !strings.Contains(msgs, "Only in work") {
		t.Errorf("dry run report: %q", h.ui.Messages)
	}
	if n := len(safe.ExistingBackups(filepath.Join(h.dir, "mysafe.age"))); n != backups {
		t.Error("--dry-run saved the safe")
	}

	// the real merge: one passphrase (the safes share the key)
	h.ui.Prompts = nil
	out = h.must([]string{pass}, "s", "m", "work")
	if !strings.Contains(out, "merged") || !strings.Contains(out, "1 added, 1 updated from it, 0 kept") {
		t.Errorf("merge: %q", out)
	}
	if len(h.ui.Prompts) != 1 {
		t.Errorf("prompts = %q, want just one passphrase", h.ui.Prompts)
	}
	if out := h.must([]string{pass}, "ls"); !strings.Contains(out, "Only in work") || !strings.Contains(out, "Only here") {
		t.Errorf("after merge:\n%s", out)
	}
	h.must([]string{pass}, "r", "c", mergeA[:6], "password")
	if c := h.app.Clipboard.(*fakeClipboard); c.value != "new" {
		t.Errorf("password = %q, want the newer one", c.value)
	}
	h.must([]string{pass}, "r", "c", mergeA[:6], "history.password.1")
	if c := h.app.Clipboard.(*fakeClipboard); c.value != "old" {
		t.Errorf("history.password.1 = %q, want the older one", c.value)
	}
	// work is unchanged
	if out := h.must([]string{pass}, "--safe", "work", "ls"); strings.Contains(out, "Only here") {
		t.Errorf("work changed:\n%s", out)
	}

	// again: nothing to do, nothing saved
	backups = len(safe.ExistingBackups(filepath.Join(h.dir, "mysafe.age")))
	h.ui.Messages = nil
	if out := h.must([]string{pass}, "safe", "merge", "work"); !strings.Contains(out, "0 added, 0 updated from it, 0 kept (newer here), 2 the same") {
		t.Errorf("second merge: %q", out)
	}
	if strings.Contains(strings.Join(h.ui.Messages, "\n"), "conflict") {
		t.Errorf("second merge reported a conflict: %q", h.ui.Messages)
	}
	if n := len(safe.ExistingBackups(filepath.Join(h.dir, "mysafe.age"))); n != backups {
		t.Error("a merge that changed nothing saved the safe")
	}

	h.mustFail(nil, "usage", "safe", "merge")
	h.mustFail(nil, "is the current safe", "safe", "merge", "mysafe")
	h.mustFail(nil, "no safe at", "safe", "merge", "missing")
}

// A safe elsewhere has its own key: its passphrase is asked too.
func TestSafeMergeFromAnotherDirectory(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	usb := filepath.Join(t.TempDir(), "usb", "family.age")
	other := "the usb stick passphrase"
	h.must([]string{other, other}, "safe", "create", usb)
	importInto(h, []string{other}, usb, `[{"type":"note","name":"From the stick"}]`)

	h.ui.Prompts = nil
	h.must([]string{pass, other}, "safe", "merge", usb)
	if p := strings.Join(h.ui.Prompts, "|"); p != "passphrase for mysafe.age: |passphrase for family.age: " {
		t.Errorf("prompts = %q", p)
	}
	if out := h.must([]string{pass}, "ls"); !strings.Contains(out, "From the stick") {
		t.Errorf("after merge:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(usb), "identity.age")); err != nil {
		t.Error(err)
	}
}

func TestSafeMergeConflict(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	h.must(nil, "safe", "create", "work")
	importInto(h, []string{pass}, "mysafe", `[{"id":"`+mergeA+`","type":"note","name":"Ours","updated_at":"2026-11-01T00:00:00Z"}]`)
	importInto(h, []string{pass}, "work", `[{"id":"`+mergeA+`","type":"note","name":"Theirs","updated_at":"2026-11-01T00:00:00Z"}]`)
	out := h.must([]string{pass}, "safe", "merge", "work")
	if !strings.Contains(out, "1 kept") || !strings.Contains(strings.Join(h.ui.Messages, "\n"), "conflict") {
		t.Errorf("conflict: %q %q", out, h.ui.Messages)
	}
	if out := h.must([]string{pass}, "ls"); !strings.Contains(out, "Ours") || strings.Contains(out, "Theirs") {
		t.Errorf("after:\n%s", out)
	}
}
