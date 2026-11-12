package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"filippo.io/age"
	"filippo.io/age/plugin"
)

// TestMain builds the fake age plugin (internal/safe/testdata/age-plugin-dstest) and puts it on
// PATH, so `keys add` and hardware-key unlocking run as they would with a real plugin.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "digisafe-plugin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	exe := filepath.Join(dir, "age-plugin-dstest")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", exe, "../safe/testdata/age-plugin-dstest").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building the fake age plugin: %v\n%s", err, out)
		os.Exit(1)
	}
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// pluginKeyFile writes an identity file like `age-plugin-yubikey --generate` makes.
func pluginKeyFile(t *testing.T) string {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "key.txt")
	body := "# created: now\n# public key: whatever\n" + plugin.EncodeIdentity("dstest", []byte(id.String())) + "\n"
	if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func (h *harness) mustFail(answers []string, wantErr string, args ...string) {
	h.t.Helper()
	if _, err := h.run(answers, args...); err == nil || !strings.Contains(err.Error(), wantErr) {
		h.t.Errorf("%v: err = %v, want it to contain %q", args, err, wantErr)
	}
	h.ui.Answers = nil
}

func TestKeysCommands(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	key := pluginKeyFile(t)

	h.mustFail(nil, "usage", "keys", "add")
	h.mustFail(nil, "no such file", "keys", "add", filepath.Join(t.TempDir(), "missing.txt"))
	if out := h.must([]string{pass}, "keys", "add", key); !strings.Contains(out, "added 1 key(s)") {
		t.Errorf("keys add: %q", out)
	}
	out := h.must(nil, "keys", "list")
	for _, want := range []string{"passphrase", "age1", "dstest", "AGE-PLUGIN-DSTEST-"} {
		if !strings.Contains(out, want) {
			t.Errorf("keys list is missing %q:\n%s", want, out)
		}
	}

	// the plugin key now unlocks with no passphrase
	h.must(nil, "record", "list")
	// adding it again fails (keys add always asks for the passphrase: it re-encrypts the
	// directory's other safes too)
	h.mustFail([]string{pass}, "already added", "keys", "add", key)
	// --passphrase skips it
	h.must([]string{pass}, "--passphrase", "record", "list")

	h.mustFail(nil, "usage", "keys", "remove")
	h.mustFail(nil, "must be a number", "keys", "remove", "one")
	h.mustFail([]string{pass}, "no plugin key 5", "keys", "remove", "5")
	if out := h.must([]string{pass}, "keys", "remove", "1"); !strings.Contains(out, "removed key 1") {
		t.Errorf("keys remove: %q", out)
	}
	// back to the passphrase only
	h.must([]string{pass}, "record", "list")
}

func TestTypes(t *testing.T) {
	h := newHarness(t)
	out := h.must(nil, "record", "types")
	for _, want := range []string{"bank_account:", "bank_card:", "card_network", "token", "username", "login:", "note:", "card_no", "(secret)", "MMYY",
		"street_1, street_2", "secret_questions", "custom fields"} {
		if !strings.Contains(out, want) {
			t.Errorf("types is missing %q", want)
		}
	}
}

func TestBankAccountAddressAndCustomFields(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	// name, description, tags, the 6 bank fields, the 5 address fields, a secret question, one
	// custom field, notes
	h.must([]string{pass,
		"Credit Union", "checking", "money",
		"123456789", "checking", "021000021", "CU", "555-0100", "https://cu.example.com",
		"1 Main St", "", "Springfield", "IL", "62701",
		"mother's maiden name?", "smith", "",
		"branch", "downtown", "",
		"multi\nline notes"},
		"record", "add", "bank_account")
	id := h.id("credit")
	out := h.must([]string{pass}, "record", "show", id[:6])
	for _, want := range []string{"address.city:", "Springfield", "question 1:", "mother's maiden name?", "answer 1:", "smith",
		"custom.branch:", "downtown", "notes:", "  multi", "  line notes"} {
		if !strings.Contains(out, want) {
			t.Errorf("show is missing %q:\n%s", want, out)
		}
	}

	// edit: clear the tags, keep the fields, change the city, remove one custom field, add another
	h.must([]string{pass,
		"", "", "-",
		"", "", "", "", "", "",
		"", "", "Shelbyville", "", "",
		"",
		"branch", "-", "vault", "B12", "",
		""},
		"record", "edit", id[:6])
	out = h.must([]string{pass}, "record", "show", id[:6])
	if !strings.Contains(out, "Shelbyville") || strings.Contains(out, "downtown") || !strings.Contains(out, "custom.vault:") ||
		strings.Contains(out, "tags:") || !strings.Contains(out, "123456789") {
		t.Errorf("after edit:\n%s", out)
	}
	h.must([]string{pass}, "record", "copy", id[:6], "custom.vault")
	if c := h.app.Clipboard.(*fakeClipboard); c.value != "B12" {
		t.Errorf("copy custom.vault = %q", c.value)
	}
	h.must([]string{pass}, "record", "copy", id[:6], "address.city")
	if c := h.app.Clipboard.(*fakeClipboard); c.value != "Shelbyville" {
		t.Errorf("copy address.city = %q", c.value)
	}
}

func TestLoginQuestionsAndStrictFields(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	// a bad TOTP secret is asked again; the second one is good
	h.must([]string{pass,
		"Mail", "", "",
		"", "me", "pw", "", "", "not base32!", totpSecret, "",
		"q1?", "a1", "",
		"",
		""},
		"record", "add", "login")
	if !strings.Contains(strings.Join(h.ui.Messages, "\n"), "try again") {
		t.Errorf("no retry message: %q", h.ui.Messages)
	}
	id := h.id("mail")

	// replace the secret questions
	h.must([]string{pass,
		"", "", "",
		"", "", "", "", "", "", "",
		"y", "q2?", "a2", "q3?", "a3", "",
		"",
		""},
		"record", "edit", id[:6])
	out := h.must([]string{pass}, "record", "show", id[:6])
	if strings.Contains(out, "q1?") || !strings.Contains(out, "q3?") {
		t.Errorf("questions not replaced:\n%s", out)
	}

	// three bad TOTP secrets give up
	h.mustFail([]string{pass, "Bad", "", "", "", "", "", "", "", "!!", "!!", "!!"}, "totp_secret", "record", "add", "login")
}

func TestCopyOTPAndExportImport(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	h.must([]string{pass, "Site", "", "", "", "me", "pw", "", "", totpSecret, "", "", "", ""}, "record", "add", "login")
	h.must([]string{pass, "Plain", "", "", "", "", "", "", "", "", "", "", "", ""}, "record", "add", "login")
	id, plain := h.id("site"), h.id("plain")
	clip := h.app.Clipboard.(*fakeClipboard)

	h.must([]string{pass}, "record", "copy", id[:6], "password", "--timeout", "0")
	if clip.scheduled || !strings.Contains(h.ui.Messages[len(h.ui.Messages)-1], "copied the password") {
		t.Errorf("--timeout 0 should not schedule clearing: %+v", clip)
	}
	clip.failSchedule = true
	h.must([]string{pass}, "record", "copy", id[:6], "password")
	if !strings.Contains(h.ui.Messages[len(h.ui.Messages)-1], "clear the clipboard yourself") {
		t.Errorf("schedule failure message: %q", h.ui.Messages)
	}
	clip.failSchedule = false
	h.mustFail([]string{pass}, "has no pin", "record", "copy", id[:6], "pin")
	h.mustFail([]string{pass}, "has no answer.9", "record", "copy", id[:6], "answer.9")
	h.mustFail(nil, "usage", "record", "copy", id[:6])

	h.must([]string{pass}, "record", "otp", id[:6], "--copy")
	if len(clip.value) != 6 {
		t.Errorf("otp --copy put %q on the clipboard", clip.value)
	}
	h.mustFail([]string{pass}, "has no totp_secret", "record", "otp", plain[:6])

	// export to stdout, and declined
	if out := h.must([]string{pass}, "safe", "export", "--yes"); !strings.Contains(out, `"name": "Site"`) {
		t.Errorf("export to stdout: %q", out)
	}
	h.mustFail([]string{"n"}, "aborted", "safe", "export")
	exp := filepath.Join(t.TempDir(), "e.json")
	h.must([]string{pass}, "safe", "export", "--yes", "--out", exp)
	h.must([]string{pass}, "safe", "export", "--yes", "--force", "--out", exp)

	// import: an export replaces with --replace; a single record and a list are added
	if out := h.must([]string{pass}, "safe", "import", "--replace", exp); !strings.Contains(out, "2 replaced") {
		t.Errorf("import --replace: %q", out)
	}
	one := filepath.Join(t.TempDir(), "one.json")
	os.WriteFile(one, []byte(`{"type":"note","name":"n1","fields":{"content":"c"}}`), 0o600)
	h.must([]string{pass}, "safe", "import", one)
	list := filepath.Join(t.TempDir(), "list.json")
	os.WriteFile(list, []byte(`[{"type":"note","name":"n2"},{"type":"note","name":"n3"}]`), 0o600)
	if out := h.must([]string{pass}, "safe", "import", list); !strings.Contains(out, "2 added") {
		t.Errorf("import list: %q", out)
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte(`[{"type":"bogus","name":"x"}]`), 0o600)
	h.mustFail([]string{pass}, "record 1", "safe", "import", bad)
	os.WriteFile(bad, []byte(`[{"type":"note","name":"x","extra":1}]`), 0o600)
	h.mustFail(nil, "unknown field", "safe", "import", bad)
	h.mustFail(nil, "no such file", "safe", "import", filepath.Join(t.TempDir(), "nope.json"))
	h.mustFail(nil, "usage", "safe", "import")

	// add --file takes exactly one record of the right type
	h.mustFail([]string{pass}, "holds 2 records", "record", "add", "note", "--file", list)
	h.mustFail([]string{pass}, "not login", "record", "add", "login", "--file", one)
}

func TestMoreMistakes(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	h.mustFail(nil, "already exists", "safe", "create")
	if out := h.must(nil, "--safe", "work.age", "safe", "create"); !strings.Contains(out, "uses the existing key") {
		t.Errorf("second safe: %q", out)
	}
	h.mustFail([]string{pass}, "unknown record type", "query", "x", "--type", "bogus")
	h.mustFail(nil, "usage", "query")
	h.mustFail(nil, "usage", "record", "edit")
	h.mustFail([]string{pass}, "no record", "record", "edit", "ffff")
	h.mustFail(nil, "usage", "record", "del")
	h.mustFail([]string{pass}, "no record", "record", "del", "ffff")
	h.mustFail(nil, "usage", "record", "otp")
	h.mustFail([]string{"wrong 1", "wrong 2", "wrong 3"}, "wrong passphrase", "keys", "passwd")

	h.must([]string{pass, "Note", "", "", "c", "", ""}, "record", "add", "note")
	id := h.id("note")
	if out := h.must([]string{pass}, "record", "del", "--force", id[:6]); !strings.Contains(out, "deleted note") {
		t.Errorf("del --force: %q", out)
	}
	if New() == nil {
		t.Error("New returned nil")
	}
}

// The hidden clipboard clearer reads the hash from stdin and hands it to the clipboard.
func TestClearClipboardCommand(t *testing.T) {
	h := newHarness(t)
	h.app.In = strings.NewReader("abc123\n")
	h.must(nil, "_clear-clipboard", "--after", "2s")
	clip := h.app.Clipboard.(*fakeClipboard)
	if clip.clearedSum != "abc123" || clip.clearIn.String() != "2s" {
		t.Errorf("clearer got %q after %v", clip.clearedSum, clip.clearIn)
	}
	h.app.In = strings.NewReader("")
	h.mustFail(nil, "EOF", "_clear-clipboard")
}

func TestFormatting(t *testing.T) {
	if shortID("abc") != "abc" || shortID("0123456789") != "01234567" {
		t.Error("shortID")
	}
	if abbrev("short") != "short" || len(abbrev(strings.Repeat("x", 100))) != 35 {
		t.Errorf("abbrev = %q", abbrev(strings.Repeat("x", 100)))
	}
	if oneLine("a\n  b   c", 10) != "a b c" || oneLine("abcdefghijk", 5) != "abcd…" {
		t.Errorf("oneLine")
	}
}

// Without --dir the safe is in $HOME/.digisafe.
func TestDefaultDirectory(t *testing.T) {
	h := newHarness(t)
	h.out.Reset()
	err := h.app.Run(context.Background(), []string{"digisafe", "record", "list"})
	want := filepath.Join(h.app.Home, ".digisafe", "mysafe.age")
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("list with the default dir: %v, want it to mention %s", err, want)
	}
}

// An empty name is asked again, when adding and when editing.
func TestNameIsRequired(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	h.must([]string{pass, "", "  ", "Named", "", "", "c", "", ""}, "record", "add", "note")
	if n := strings.Count(strings.Join(h.ui.Messages, "\n"), "a record needs a name"); n != 2 {
		t.Errorf("name messages = %d", n)
	}
	id := h.id("named")
	h.must([]string{pass, "-", "Renamed", "", "", "", "", ""}, "record", "edit", id[:6])
	if out := h.must([]string{pass}, "record", "show", id[:6]); !strings.Contains(out, "Renamed") {
		t.Errorf("rename:\n%s", out)
	}
	// a bad TOTP secret on edit is an error after three tries, and nothing is saved
	h.must([]string{pass, "Site", "", "", "", "", "", "", "", "", "", "", "", ""}, "record", "add", "login")
	site := h.id("site")
	h.mustFail([]string{pass, "", "", "", "", "", "", "", "", "!!", "!!", "!!"}, "totp_secret", "record", "edit", site[:6])
	// rekey and passwd give up on a new passphrase that's too short
	h.mustFail([]string{pass, "short", "short", "short"}, "at least 12", "keys", "passwd")
}
