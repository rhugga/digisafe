package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/rhugga/digisafe/internal/prompt"
	"github.com/rhugga/digisafe/internal/record"
	"github.com/rhugga/digisafe/internal/safe"
)

// Hardware keys and the key pair belong to the directory: adding, removing and rekeying apply
// to every safe in it, and their backups. Files that aren't encrypted to the key are skipped.
func TestKeysCoverEverySafe(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	h.must(nil, "safe", "create", "work") // shares the key
	h.must([]string{pass}, "r", "a", "note", "--name", "Home", "--no-prompt")
	h.must([]string{pass}, "--safe", "work", "r", "a", "note", "--name", "Work", "--no-prompt")

	// an unrelated age file in the directory
	other, _ := age.GenerateX25519Identity()
	var buf bytes.Buffer
	w, _ := age.Encrypt(&buf, other.Recipient())
	w.Write([]byte("not a safe"))
	w.Close()
	foreign := filepath.Join(h.dir, "foreign.age")
	os.WriteFile(foreign, buf.Bytes(), 0o600)

	key := pluginKeyFile(t)
	if out := h.must([]string{pass}, "keys", "add", key); !strings.Contains(out, "every safe in") {
		t.Errorf("keys add: %q", out)
	}
	if !strings.Contains(strings.Join(h.ui.Messages, "\n"), "skipped "+foreign) {
		t.Errorf("the foreign file wasn't skipped: %q", h.ui.Messages)
	}
	// the hardware key opens both safes: no passphrase asked
	for _, safeName := range []string{"mysafe", "work"} {
		h.ui.Messages = nil
		h.must(nil, "--safe", safeName, "ls")
		if strings.Contains(strings.Join(h.ui.Messages, "\n"), "did not unlock") {
			t.Errorf("%s: the new key didn't open it: %q", safeName, h.ui.Messages)
		}
	}

	// removed from mysafe: neither safe opens with it any more
	h.must([]string{pass}, "keys", "remove", "1")
	for _, safeName := range []string{"mysafe", "work"} {
		if out := h.must([]string{pass}, "--safe", safeName, "ls"); !strings.Contains(out, "note") {
			t.Errorf("%s after remove:\n%s", safeName, out)
		}
	}

	// rekey re-encrypts both safes and their backups; both open with the new passphrase
	h.must([]string{pass}, "keys", "add", key)
	np := "a second long passphrase"
	out := h.must([]string{pass, np, np}, "keys", "rekey")
	if !strings.Contains(out, "file(s) re-encrypted") {
		t.Errorf("rekey: %q", out)
	}
	for _, safeName := range []string{"mysafe", "work"} {
		if out := h.must([]string{np}, "--passphrase", "--safe", safeName, "ls"); !strings.Contains(out, "note") {
			t.Errorf("%s after rekey:\n%s", safeName, out)
		}
	}
	if b, _ := os.ReadFile(foreign); !bytes.Equal(b, buf.Bytes()) {
		t.Error("the foreign file was changed")
	}
}

// While a record is being filled in or edited, the safe isn't locked: another digisafe can save
// it. Then an add still lands in the latest version, and an edit applies unless that other save
// changed or deleted the same record.
func TestConcurrentSaveWhileEditing(t *testing.T) {
	old := safe.LockTimeout
	safe.LockTimeout = 300 * time.Millisecond // a lock held during the form would fail fast
	t.Cleanup(func() { safe.LockTimeout = old })

	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	h.must([]string{pass}, "r", "a", "note", "--name", "A", "--no-prompt")
	h.must([]string{pass}, "r", "a", "note", "--name", "B", "--no-prompt")
	a, b := h.id("a"), h.id("b")
	other := func(args ...string) {
		t.Helper()
		o := &App{UI: prompt.NewScript(pass), Out: &bytes.Buffer{}, Now: h.app.Now, Clipboard: &fakeClipboard{}, Home: h.app.Home}
		if err := o.Run(context.Background(), append([]string{"digisafe", "--dir", h.dir}, args...)); err != nil {
			t.Fatalf("the other digisafe: %v %v", args, err)
		}
	}
	form := &fakeForm{}
	h.app.Form = form

	// add: the other digisafe adds a record meanwhile; both are kept
	form.edit = func(r *record.Record) error {
		other("r", "a", "note", "--name", "Other", "--no-prompt")
		r.Name = "Mine"
		return nil
	}
	h.must([]string{pass}, "r", "a", "note")
	out := h.must([]string{pass}, "ls")
	if !strings.Contains(out, "Other") || !strings.Contains(out, "Mine") {
		t.Errorf("after a concurrent add:\n%s", out)
	}

	// edit A while the other edits B: both changes kept
	form.edit = func(r *record.Record) error {
		other("r", "a", "note", "--name", "Other 2", "--no-prompt")
		r.Description = "edited A"
		return nil
	}
	h.must([]string{pass}, "r", "e", a[:6])
	if out := h.must([]string{pass}, "q", "A"); !strings.Contains(out, "edited A") {
		t.Errorf("edit A:\n%s", out)
	}

	// edit B while the other deletes it, or changes it: nothing saved
	form.edit = func(r *record.Record) error {
		other("r", "d", "--force", b[:6])
		r.Description = "lost"
		return nil
	}
	h.mustFail([]string{pass}, "deleted note "+b[:8], "r", "e", b[:6])
	form.edit = func(r *record.Record) error {
		other("r", "a", "note", "--name", "C", "--no-prompt") // so the safe changes, but not A
		return nil
	}
	h.must([]string{pass}, "r", "e", a[:6]) // unchanged A: fine
	h.app.Form = nil
	h.must([]string{pass}, "safe", "export", "--yes")
	if strings.Contains(h.out.String(), "lost") {
		t.Error("the edit of a deleted record was saved")
	}
}

func TestEditOfARecordChangedMeanwhile(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	h.must([]string{pass}, "r", "a", "note", "--name", "A", "--no-prompt")
	a := h.id("a")
	h.app.Form = &fakeForm{edit: func(r *record.Record) error {
		o := &App{UI: prompt.NewScript(pass), Out: &bytes.Buffer{}, Now: h.app.Now, Clipboard: &fakeClipboard{}, Home: h.app.Home}
		h.now = h.now.Add(time.Minute)
		if err := o.Run(context.Background(), []string{"digisafe", "--dir", h.dir, "r", "e", a[:6], "--plain"}); err == nil {
			t.Error("the plain edit had no answers but succeeded")
		}
		o.UI = prompt.NewScript(pass, "", "theirs", "", "", "", "")
		if err := o.Run(context.Background(), []string{"digisafe", "--dir", h.dir, "r", "e", a[:6], "--plain"}); err != nil {
			t.Fatal(err)
		}
		r.Description = "mine"
		return nil
	}}
	h.mustFail([]string{pass}, "another digisafe changed note "+a[:8], "r", "e", a[:6])
	if out := h.must([]string{pass}, "q", "A"); !strings.Contains(out, "theirs") || strings.Contains(out, "mine") {
		t.Errorf("after:\n%s", out)
	}
}

// A record with a custom field called "records" is a record, not an export.
func TestRecordMentioningRecords(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	h.app.In = strings.NewReader(`{"type":"note","name":"Odd","custom":{"records":"x"},"notes":"see \"records\""}`)
	h.must([]string{pass}, "record", "add", "note", "--file", "-")
	h.app.In = strings.NewReader(`[{"type":"note","name":"Odd2","custom":{"records":"y"}}]`)
	h.must([]string{pass}, "safe", "import", "-")
	if out := h.must([]string{pass}, "ls"); !strings.Contains(out, "Odd") || !strings.Contains(out, "Odd2") {
		t.Errorf("ls:\n%s", out)
	}
}

// Imported ids are stored in canonical form; the same id twice in one file is refused; and
// --replace keeps the replaced login's secrets in its history.
func TestImportIDsAndReplace(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	const id = "0B6F4F7E-6D1C-4A4E-9A51-6C2F4B1E8A10"
	h.app.In = strings.NewReader(`[{"id":"{` + id + `}","type":"login","name":"Site","fields":{"password":"old"}}]`)
	h.must([]string{pass}, "safe", "import", "-")
	if got := h.id("site"); got != strings.ToLower(id) {
		t.Errorf("id stored as %q", got)
	}
	h.app.In = strings.NewReader(`[{"id":"urn:uuid:` + strings.ToLower(id) + `","type":"login","name":"Site","fields":{"password":"new"}}]`)
	out := h.must([]string{pass}, "safe", "import", "-", "--replace")
	if !strings.Contains(out, "1 replaced") {
		t.Errorf("replace: %q", out)
	}
	h.must([]string{pass}, "r", "c", id[:6], "history.password.1")
	if c := h.app.Clipboard.(*fakeClipboard); c.value != "old" {
		t.Errorf("history.password.1 = %q, want the replaced password", c.value)
	}
	h.app.In = strings.NewReader(`[{"id":"` + id + `","type":"note","name":"A"},{"id":"` + strings.ToLower(id) + `","type":"note","name":"B"}]`)
	h.mustFail([]string{pass}, "records 1 and 2", "safe", "import", "-")
}

// Escape sequences in imported text are shown escaped, never sent to the terminal.
func TestEscapesAreNotPrinted(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	h.app.In = strings.NewReader(`{"type":"login","name":"Bank","fields":{"url":"https://x\u001b]0;pwned\u0007"},` +
		`"custom":{"k\u001b[2J":"v"},"notes":"n\u001b[31m"}`)
	h.must([]string{pass}, "record", "add", "login", "--file", "-")
	id := h.id("bank")
	for _, styled := range []bool{false, true} {
		h.app.Styled = styled
		out := h.must([]string{pass}, "r", "s", id[:6])
		if strings.Contains(out, "]0;pwned\x07") || strings.Contains(out, "\x1b[2J") || strings.Contains(out, "n\x1b[31m") {
			t.Errorf("styled=%v: raw escapes printed:\n%q", styled, out)
		}
		if !strings.Contains(out, `\x1b]0;pwned\x07`) {
			t.Errorf("styled=%v: escaped form missing:\n%q", styled, out)
		}
	}
}
