package app

import (
	"fmt"
	"github.com/rhugga/digisafe/internal/record"
	"strings"
	"testing"
)

func TestAddWithFieldFlags(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")

	// only the secret fields are prompted for: password, PIN, TOTP secret, token
	h.must([]string{pass, "hunter2", "", "", "tok-123"},
		"record", "add", "login", "--name", "GitHub", "--description", "work", "--tags", "dev, work",
		"--notes", "2FA on phone", "--field", "url=https://github.com", "--field", "login=me")
	var asked []string
	for _, p := range h.ui.Prompts[3:] { // after create's two and the unlock
		asked = append(asked, strings.TrimSuffix(p, ": "))
	}
	if strings.Join(asked, "|") != "password|PIN|TOTP secret (base32 seed or otpauth:// URI)|token (an API or access token)" {
		t.Errorf("prompts = %q", asked)
	}
	out := h.must([]string{pass}, "record", "show", h.id("github")[:6])
	// --field login=me: the old name of username still works
	for _, want := range []string{"https://github.com", "username:", "me", "hunter2", "dev, work", "2FA on phone", "token:", "tok-123"} {
		if !strings.Contains(out, want) {
			t.Errorf("show is missing %q:\n%s", want, out)
		}
	}

	// --no-prompt: no secret prompts at all; commas in a value survive
	h.must([]string{pass}, "record", "add", "bank_account", "--name", "Checking", "--no-prompt",
		"--field", "bank_name=Credit Union, Main", "--field", "address.street_1=Suite 4, Floor 2", "--field", "address.city=Springfield")
	out = h.must([]string{pass}, "record", "show", h.id("checking")[:6])
	for _, want := range []string{"Credit Union, Main", "Suite 4, Floor 2", "Springfield"} {
		if !strings.Contains(out, want) {
			t.Errorf("show is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "account_no") {
		t.Errorf("--no-prompt should leave the secret fields empty:\n%s", out)
	}

	// a card's expiry is checked like a prompted one (a warning, not an error)
	h.must([]string{pass, "4111111111111111", "123", "1234"}, "record", "add", "bank_card", "--name", "Visa",
		"--field", "card_network=mastercard", "--field", "exp_date=13/28")
	if !strings.Contains(strings.Join(h.ui.Messages, "\n"), "exp_date") {
		t.Errorf("no exp_date warning: %q", h.ui.Messages)
	}
}

// Mistakes in the flags are caught before the passphrase is asked for.
func TestFieldFlagMistakes(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	for _, tc := range []struct {
		args    []string
		wantErr string
	}{
		{[]string{"record", "add", "login", "--name", "x", "--field", "password=hunter2"}, "password is secret"},
		{[]string{"record", "add", "bank_card", "--name", "x", "--field", "card_no=4111111111111111"}, "card_no is secret"},
		{[]string{"record", "add", "bank_card", "--name", "x", "--field", "pin=1234"}, "pin is secret"},
		{[]string{"record", "add", "login", "--name", "x", "--field", "token=t"}, "token is secret"},
		{[]string{"record", "add", "login", "--name", "x", "--field", "colour=blue"}, "have no field colour"},
		{[]string{"record", "add", "login", "--name", "x", "--field", "noequals"}, "want name=value"},
		{[]string{"record", "add", "login", "--name", "x", "--field", "address.city=x"}, "have no address"},
		{[]string{"record", "add", "bank_account", "--name", "x", "--field", "address.planet=x"}, "no such address field"},
		{[]string{"record", "add", "login", "--field", "url=x"}, "--name is required"},
		{[]string{"record", "add", "login", "--name", "x", "--file", "f.json"}, "can't be combined"},
		{[]string{"record", "add", "login", "--no-prompt"}, "--no-prompt goes with"},
	} {
		h.mustFail(nil, tc.wantErr, tc.args...) // no answers: a passphrase prompt would fail differently
	}
	if strings.Contains(strings.Join(h.ui.Prompts[2:], ""), "passphrase") {
		t.Errorf("a flag mistake asked for the passphrase: %q", h.ui.Prompts[2:])
	}
}

func TestFileFromStdin(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	h.app.In = strings.NewReader(`{"name":"wifi","fields":{"content":"pa55word"}}`)
	h.must([]string{pass}, "record", "add", "note", "--file", "-")
	if strings.Contains(strings.Join(h.ui.Messages, "\n"), "still holds") {
		t.Errorf("stdin has no file to delete: %q", h.ui.Messages)
	}
	if out := h.must([]string{pass}, "record", "show", h.id("wifi")[:6]); !strings.Contains(out, "pa55word") {
		t.Errorf("show:\n%s", out)
	}

	h.app.In = strings.NewReader(`[{"type":"note","name":"one"},{"type":"login","name":"two","fields":{"password":"p"}}]`)
	if out := h.must([]string{pass}, "safe", "import", "-"); !strings.Contains(out, "2 added") {
		t.Errorf("import -: %q", out)
	}
	h.app.In = strings.NewReader(`{"name":"bad","fields":{"nope":"x"}}`)
	h.mustFail([]string{pass}, "no field nope", "record", "add", "note", "--file", "-")
	h.app.In = strings.NewReader(`not json`)
	h.mustFail([]string{pass}, "stdin", "record", "add", "note", "--file", "-")
}

// Files and exports from before the rename import, as bank_card.
func TestImportOldCardType(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	h.app.In = strings.NewReader(`[{"type":"credit_card","name":"Old","fields":{"card_network":"Amex"}}]`)
	h.must([]string{pass}, "safe", "import", "-")
	h.app.In = strings.NewReader(`{"type":"credit_card","name":"Single"}`)
	h.must([]string{pass}, "record", "add", "bank_card", "--file", "-")
	h.app.In = strings.NewReader(`{"type":"credit_card","name":"Alias"}`)
	h.must([]string{pass}, "record", "add", "credit_card", "--file", "-")
	out := h.must([]string{pass}, "ls", "--type", "credit_card")
	if strings.Count(out, "bank_card") != 3 {
		t.Errorf("ls:\n%s", out)
	}
	if out := h.must([]string{pass}, "r", "s", h.id("old")[:6]); !strings.Contains(out, "card_network:  amex") {
		t.Errorf("show:\n%s", out)
	}
}

func TestSecretHistory(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	h.must([]string{pass}, "r", "a", "login", "--name", "Site", "--field", "username=me", "--no-prompt")
	id := h.id("site")
	h.must([]string{pass}, "r", "a", "login", "--name", "Other", "--no-prompt")

	// edit (plain): name, description, tags, url, login, password, email, pin, totp, token,
	// replace questions?, custom, notes
	edit := func(password string) {
		h.t.Helper()
		h.must([]string{pass, "", "", "", "", "", password, "", "", "", "", "", "", ""}, "r", "e", id[:6], "--plain")
	}
	h.app.Form = &fakeForm{edit: func(r *record.Record) error { r.Fields["password"] = "pw-1"; return nil }}
	h.must([]string{pass}, "r", "e", id[:6]) // the form path: sets the first password
	h.app.Form = nil
	for i := 2; i <= 7; i++ {
		edit(fmt.Sprintf("pw-%d", i))
	}
	if msgs := strings.Join(h.ui.Messages, "\n"); !strings.Contains(msgs, "kept the previous password as history.password.1 (5 kept") {
		t.Errorf("messages: %q", h.ui.Messages)
	}
	h.ui.Messages = nil
	edit("") // Enter keeps the password: nothing new kept
	if strings.Contains(strings.Join(h.ui.Messages, "\n"), "kept the previous") {
		t.Errorf("an unchanged password was kept: %q", h.ui.Messages)
	}

	h.ui.Messages = nil
	out := h.must([]string{pass}, "r", "s", id[:6])
	if strings.Contains(out, "pw-6") || !strings.Contains(strings.Join(h.ui.Messages, "\n"), "previous values kept: password (5)") {
		t.Errorf("show without --history:\n%s\n%q", out, h.ui.Messages)
	}
	out = h.must([]string{pass}, "r", "s", id[:6], "--history")
	for _, want := range []string{"password:", "pw-7", "history.password.1:", "pw-6  (replaced ", "history.password.5:", "pw-2"} {
		if !strings.Contains(out, want) {
			t.Errorf("show --history lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "pw-1") {
		t.Errorf("more than 5 kept:\n%s", out)
	}
	h.must([]string{pass}, "r", "c", id[:6], "history.password.2")
	if c := h.app.Clipboard.(*fakeClipboard); c.value != "pw-5" {
		t.Errorf("copy history.password.2 = %q", c.value)
	}
	h.mustFail([]string{pass}, "has no history.password.6", "r", "c", id[:6], "history.password.6")

	// the styled card shows them too, masked nowhere (show reveals)
	h.app.Styled = true
	if out := h.must([]string{pass}, "r", "s", id[:6], "--history"); !strings.Contains(out, "password, previous 1") || !strings.Contains(out, "pw-6") {
		t.Errorf("styled --history:\n%s", out)
	}
	// export carries the history; import puts it back
	h.app.Styled = false
	exported := h.must([]string{pass}, "safe", "export", "--yes")
	if !strings.Contains(exported, `"history"`) || !strings.Contains(exported, `"replaced_at"`) {
		t.Errorf("export:\n%s", exported)
	}
}

// Exports from before the rename import with username; copy accepts the old name.
func TestImportOldLoginField(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	h.app.In = strings.NewReader(`[{"type":"login","name":"Old","fields":{"login":"me"}}]`)
	h.must([]string{pass}, "safe", "import", "-")
	id := h.id("old")
	if out := h.must([]string{pass}, "r", "s", id[:6]); !strings.Contains(out, "username:") {
		t.Errorf("show:\n%s", out)
	}
	h.must([]string{pass}, "r", "c", id[:6], "login")
	if c := h.app.Clipboard.(*fakeClipboard); c.value != "me" {
		t.Errorf("copy login = %q", c.value)
	}
}
