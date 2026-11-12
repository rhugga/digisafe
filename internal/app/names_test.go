package app

import (
	"strings"
	"testing"

	"github.com/rhugga/digisafe/internal/record"
)

func TestUniqueNames(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	h.must([]string{pass}, "r", "a", "note", "--name", "My GitHub", "--no-prompt")
	h.must([]string{pass}, "r", "a", "note", "--name", "Bank", "--no-prompt")
	gh := h.id("my github")

	// flags: refused before the secret prompts (only the passphrase is asked)
	h.ui.Prompts = nil
	h.mustFail([]string{pass}, `already called "My GitHub"`, "r", "a", "login", "--name", " my  github ")
	if len(h.ui.Prompts) != 1 {
		t.Errorf("prompts = %q, want only the passphrase", h.ui.Prompts)
	}
	// plain prompts: a taken name is asked again
	h.must([]string{pass, "MY GITHUB", "Other", "", "", "", "", ""}, "r", "a", "note", "--plain")
	if !strings.Contains(strings.Join(h.ui.Messages, "\n"), "already called") {
		t.Errorf("no message for the taken name: %q", h.ui.Messages)
	}
	// plain edit: renaming onto a taken name is asked again; keeping its own name is fine
	h.must([]string{pass, "bank", "Renamed", "", "", "", "", ""}, "r", "e", gh[:6], "--plain")
	h.must([]string{pass, "renamed", "", "", "", "", ""}, "r", "e", gh[:6], "--plain") // its own name, other case
	// the form gets the check, and a rename onto a taken name isn't saved
	form := &fakeForm{edit: func(r *record.Record) error { r.Name = "BANK"; return nil }}
	h.app.Form = form
	h.mustFail([]string{pass}, "already called \"Bank\"; nothing was saved", "r", "e", gh[:6])
	if form.nameOK == nil || form.nameOK("bank") == nil || form.nameOK("free name") != nil || form.nameOK("renamed") != nil {
		t.Error("the form's name check is wrong")
	}
	h.mustFail([]string{pass}, "already called", "r", "a", "note") // the form path of add: checked by store too
	h.app.Form = nil

	// import: a taken name, or the same name twice in the file, imports nothing
	h.app.In = strings.NewReader(`[{"type":"note","name":"Fresh"},{"type":"note","name":"bank"}]`)
	h.mustFail([]string{pass}, "record 2 (bank)", "safe", "import", "-")
	h.app.In = strings.NewReader(`[{"type":"note","name":"Twice"},{"type":"note","name":"twice"}]`)
	h.mustFail([]string{pass}, "nothing was imported", "safe", "import", "-")
	if out := h.must([]string{pass}, "ls"); strings.Contains(out, "Fresh") || strings.Contains(out, "Twice") {
		t.Errorf("a failed import added records:\n%s", out)
	}
}

func TestShowByName(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	h.must([]string{pass}, "r", "a", "note", "--name", "My Bank", "--description", "checking", "--no-prompt")
	id := h.id("my bank")
	for _, ref := range []string{id, id[:6], "my bank", " MY   BANK "} {
		if out := h.must([]string{pass}, "record", "show", ref); !strings.Contains(out, "checking") {
			t.Errorf("show %q:\n%s", ref, out)
		}
	}
	h.mustFail([]string{pass}, `no record with the id or name "my bnak"`, "record", "show", "my bnak")
}

// The same name in two safes isn't merged in twice.
func TestMergeSkipsTakenNames(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	h.must(nil, "safe", "create", "work")
	h.must([]string{pass}, "r", "a", "note", "--name", "GitHub", "--no-prompt")
	h.must([]string{pass}, "--safe", "work", "r", "a", "note", "--name", "github", "--no-prompt") // another id
	out := h.must([]string{pass}, "safe", "merge", "work")
	if !strings.Contains(out, "1 skipped (name taken)") || !strings.Contains(strings.Join(h.ui.Messages, "\n"), "skip") {
		t.Errorf("merge: %q %q", out, h.ui.Messages)
	}
	if out := h.must([]string{pass}, "ls"); strings.Count(strings.ToLower(out), "github") != 1 {
		t.Errorf("after merge:\n%s", out)
	}
}
