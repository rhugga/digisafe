package app

import (
	"bytes"
	"context"
	"encoding/base32"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rhugga/digisafe/internal/prompt"
	"github.com/rhugga/digisafe/internal/safe"
	"github.com/rhugga/digisafe/internal/totp"
)

const pass = "correct horse battery"

func init() { safe.ScryptWorkFactor = 10 }

type fakeClipboard struct {
	value        string
	clearIn      time.Duration
	scheduled    bool
	failSchedule bool
	clearedSum   string
}

func (c *fakeClipboard) Copy(v string) error { c.value = v; return nil }
func (c *fakeClipboard) ScheduleClear(v string, d time.Duration) error {
	if c.failSchedule {
		return errors.New("no background process")
	}
	c.scheduled, c.clearIn = true, d
	return nil
}
func (c *fakeClipboard) ClearIfUnchanged(sum string, d time.Duration) error {
	c.clearedSum, c.clearIn = sum, d
	return nil
}

type harness struct {
	t   *testing.T
	dir string
	ui  *prompt.Script
	out *bytes.Buffer
	app *App
	now time.Time
}

func newHarness(t *testing.T) *harness {
	h := &harness{t: t, dir: filepath.Join(t.TempDir(), "ds"), ui: prompt.NewScript(), out: &bytes.Buffer{},
		now: time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)}
	h.app = &App{UI: h.ui, Out: h.out, Now: func() time.Time { return h.now }, Clipboard: &fakeClipboard{}, Home: t.TempDir()}
	return h
}

// run runs a command with the scripted answers and returns its stdout.
func (h *harness) run(answers []string, args ...string) (string, error) {
	h.t.Helper()
	h.out.Reset()
	h.ui.Push(answers...)
	err := h.app.Run(context.Background(), append([]string{"digisafe", "--dir", h.dir}, args...))
	if len(h.ui.Answers) > 0 && err == nil {
		h.t.Errorf("%v: %d answers unused: %q", args, len(h.ui.Answers), h.ui.Answers)
		h.ui.Answers = nil
	}
	return h.out.String(), err
}

func (h *harness) must(answers []string, args ...string) string {
	h.t.Helper()
	out, err := h.run(answers, args...)
	if err != nil {
		h.t.Fatalf("%v: %v (prompts %q, messages %q)", args, err, h.ui.Prompts, h.ui.Messages)
	}
	return out
}

func (h *harness) id(name string) string {
	h.t.Helper()
	s, err := safe.Open(safe.NewPaths(h.dir, ""), prompt.NewScript(pass), safe.Options{PassphraseOnly: true})
	if err != nil {
		h.t.Fatal(err)
	}
	defer s.Close()
	rs := s.Doc.Search(name, "")
	if len(rs) != 1 {
		h.t.Fatalf("%d records match %q", len(rs), name)
	}
	return rs[0].ID
}

var totpSecret = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))

func TestEndToEnd(t *testing.T) {
	h := newHarness(t)
	out := h.must([]string{pass, pass}, "safe", "create")
	if !strings.Contains(out, "created") {
		t.Fatalf("create: %q", out)
	}

	// add a login by prompts: passphrase, then name, description, tags, the 7 login fields,
	// one secret question, no custom fields, notes
	out = h.must([]string{pass,
		"GitHub", "work account", "dev, work",
		"https://github.com", "me", "hunter2-secret", "me@example.com", "", totpSecret, "",
		"first pet?", "rex", "",
		"",
		"a note"},
		"record", "add", "login")
	if !strings.Contains(out, "added login") {
		t.Fatalf("add: %q", out)
	}
	id := h.id("github")

	// list and query never show secrets
	for _, args := range [][]string{{"record", "list"}, {"query", "work"}, {"record", "list", "--type", "login"}, {"ls"}, {"q", "work"}} {
		out = h.must([]string{pass}, args...)
		if !strings.Contains(out, "GitHub") || strings.Contains(out, "hunter2-secret") || strings.Contains(out, "rex") {
			t.Errorf("%v output: %q", args, out)
		}
	}
	h.must([]string{pass}, "query", "zzz")
	if last := h.ui.Messages[len(h.ui.Messages)-1]; last != "no records found" {
		t.Errorf("query zzz message %q", last)
	}

	// show reveals everything
	out = h.must([]string{pass}, "record", "show", id[:6])
	for _, want := range []string{"hunter2-secret", "first pet?", "rex", "a note", "dev, work"} {
		if !strings.Contains(out, want) {
			t.Errorf("show is missing %q:\n%s", want, out)
		}
	}

	// copy goes to the clipboard, not stdout, and is cleared later
	out = h.must([]string{pass}, "record", "copy", id[:6], "password")
	clip := h.app.Clipboard.(*fakeClipboard)
	if clip.value != "hunter2-secret" || !clip.scheduled || clip.clearIn != 45*time.Second || out != "" {
		t.Errorf("copy: clipboard %+v, stdout %q", clip, out)
	}
	h.must([]string{pass}, "record", "copy", id[:6], "answer.1")
	if clip.value != "rex" {
		t.Errorf("copy answer.1 = %q", clip.value)
	}

	// otp prints the current code
	k, _ := totp.Parse(totpSecret)
	want, _ := k.Code(h.now)
	if out = h.must([]string{pass}, "record", "otp", id[:6]); strings.TrimSpace(out) != want {
		t.Errorf("otp = %q, want %s", out, want)
	}

	// edit: keep the name, change the description, clear the password and the pin stays empty
	h.now = h.now.Add(time.Hour)
	h.must([]string{pass,
		"", "personal account", "",
		"", "", "-", "", "", "", "",
		"n",
		"",
		""},
		"record", "edit", id[:6])
	out = h.must([]string{pass}, "record", "show", id[:6])
	if strings.Contains(out, "hunter2-secret") || !strings.Contains(out, "personal account") || !strings.Contains(out, totpSecret) {
		t.Errorf("after edit:\n%s", out)
	}

	// a card with a bad number, expiry and PIN is stored, with warnings; the network's case is
	// normalised
	h.must([]string{pass,
		"Visa", "", "",
		"ME", "VISA", "4111111111111112", "13/28", "123", "12", "", "", "",
		"",
		""},
		"record", "add", "bank_card")
	var warned int
	for _, m := range h.ui.Messages {
		if strings.HasPrefix(m, "warning:") {
			warned++
		}
	}
	if warned < 3 {
		t.Errorf("expected warnings for the card number, expiry and PIN, got %q", h.ui.Messages)
	}
	if out := h.must([]string{pass}, "record", "show", h.id("visa")[:6]); !strings.Contains(out, "card_network:  visa") {
		t.Errorf("network not normalised:\n%s", out)
	}

	// add from a file
	file := filepath.Join(t.TempDir(), "note.json")
	os.WriteFile(file, []byte(`{"name":"wifi","fields":{"content":"pa55word"}}`), 0o600)
	h.must([]string{pass}, "record", "add", "note", "--file", file)

	// a typo in a file field is rejected
	os.WriteFile(file, []byte(`{"name":"wifi2","fields":{"contnet":"x"}}`), 0o600)
	if _, err := h.run([]string{pass}, "record", "add", "note", "--file", file); err == nil || !strings.Contains(err.Error(), "no field contnet") {
		t.Errorf("bad field: %v", err)
	}

	// export, then import into a new safe
	exp := filepath.Join(t.TempDir(), "export.json")
	h.must([]string{pass}, "safe", "export", "--yes", "--out", exp)
	if st, _ := os.Stat(exp); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("export mode %v", st.Mode().Perm())
	}
	if _, err := h.run(nil, "safe", "export", "--yes", "--out", exp); err == nil {
		t.Error("export overwrote a file without --force")
	}

	h2 := newHarness(t)
	h2.must([]string{"another long passphrase", "another long passphrase"}, "safe", "create")
	out = h2.must([]string{"another long passphrase"}, "safe", "import", exp)
	if !strings.Contains(out, "3 added") {
		t.Errorf("import: %q", out)
	}
	out = h2.must([]string{"another long passphrase"}, "safe", "import", exp)
	if !strings.Contains(out, "3 skipped") {
		t.Errorf("re-import: %q", out)
	}

	// delete, declining first
	if _, err := h.run([]string{pass, "n"}, "record", "del", id[:6]); err != errAborted {
		t.Errorf("declined delete: %v", err)
	}
	h.must([]string{pass, "y"}, "record", "del", id[:6])
	if out = h.must([]string{pass}, "record", "list"); strings.Contains(out, "GitHub") {
		t.Errorf("deleted record still listed:\n%s", out)
	}
}

func TestPasswdAndRekey(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	h.must([]string{pass, "a different passphrase", "a different passphrase"}, "keys", "passwd")
	h.must([]string{"a different passphrase"}, "record", "list")

	out := h.must([]string{"a different passphrase", "third passphrase here", "third passphrase here"}, "keys", "rekey")
	if !strings.Contains(out, "new key age1") {
		t.Errorf("rekey: %q", out)
	}
	h.must([]string{"third passphrase here"}, "record", "list")

	out = h.must(nil, "keys", "list")
	if !strings.Contains(out, "passphrase") || !strings.Contains(out, "age1") {
		t.Errorf("keys list: %q", out)
	}
}

func TestMistakes(t *testing.T) {
	h := newHarness(t)
	if _, err := h.run(nil, "record", "list"); err == nil || !strings.Contains(err.Error(), "digisafe safe create") {
		t.Errorf("list before create: %v", err)
	}
	h.must([]string{"short", "a long enough passphrase", "a long enough passphrase"}, "safe", "create")
	if _, err := h.run(nil, "record", "add", "bogus"); err == nil || !strings.Contains(err.Error(), "unknown record type") {
		t.Errorf("add bogus: %v", err)
	}
	if _, err := h.run(nil, "record", "show"); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("show without id: %v", err)
	}
	if _, err := h.run([]string{"a long enough passphrase"}, "record", "show", "ffff"); err == nil {
		t.Error("show of a missing id succeeded")
	}
}
