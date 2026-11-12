package tui

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/rhugga/digisafe/internal/record"
)

// Keys for the scripts; anything else is typed as text.
const (
	tab      = "<tab>"
	backtab  = "<shift+tab>"
	enter    = "<enter>"
	down     = "<down>"
	ctrlC    = "<ctrl+c>"
	ctrlU    = "<ctrl+u>" // clears an input
	ctrlE    = "<ctrl+e>" // accepts a suggestion
	finished = "<done>"   // marks the end of a script step (readability only)
)

func keyMsg(k string) []tea.Msg {
	switch k {
	case tab:
		return []tea.Msg{tea.KeyPressMsg{Code: tea.KeyTab}}
	case backtab:
		return []tea.Msg{tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}}
	case enter:
		return []tea.Msg{tea.KeyPressMsg{Code: tea.KeyEnter}}
	case down:
		return []tea.Msg{tea.KeyPressMsg{Code: tea.KeyDown}}
	case ctrlC:
		return []tea.Msg{tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}}
	case ctrlU:
		return []tea.Msg{tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}}
	case ctrlE:
		return []tea.Msg{tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl}}
	case finished:
		return nil
	}
	var msgs []tea.Msg
	for _, r := range k {
		msgs = append(msgs, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return msgs
}

// driver runs each form the Form shows with the next script: its key presses are sent to the
// form's Update and the resulting commands run, without a terminal or a tea.Program.
type driver struct {
	t       *testing.T
	scripts [][]string
	forms   int
}

func (d *driver) run(form *huh.Form) error {
	d.t.Helper()
	if d.forms >= len(d.scripts) {
		d.t.Fatalf("form %d shown, but there are only %d scripts", d.forms+1, len(d.scripts))
	}
	keys := d.scripts[d.forms]
	d.forms++
	d.exec(form, form.Init())
	d.send(form, tea.WindowSizeMsg{Width: 100, Height: 50})
	for _, k := range keys {
		for _, m := range keyMsg(k) {
			d.send(form, m)
		}
	}
	switch form.State {
	case huh.StateCompleted:
		return nil
	case huh.StateAborted:
		return huh.ErrUserAborted
	}
	d.t.Fatalf("form %d not finished by its script %q:\n%s", d.forms, keys, form.View())
	return nil
}

func (d *driver) send(form *huh.Form, m tea.Msg) {
	_, cmd := form.Update(m)
	d.exec(form, cmd)
}

var cmdType = reflect.TypeOf(tea.Cmd(nil))

// exec runs cmd and feeds its message back to the form. Commands that don't return at once are
// timers (cursor blinks) and are dropped.
func (d *driver) exec(form *huh.Form, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(5 * time.Millisecond):
		return
	}
	if msg == nil {
		return
	}
	// tea.Batch and tea.Sequence: lists of commands
	if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice && v.Type().Elem() == cmdType {
		for i := 0; i < v.Len(); i++ {
			d.exec(form, v.Index(i).Interface().(tea.Cmd))
		}
		return
	}
	switch msg.(type) {
	case tea.QuitMsg, tea.InterruptMsg:
		return
	}
	d.send(form, msg)
}

func newForm(t *testing.T, scripts ...[]string) (Form, *bytes.Buffer, *driver) {
	var out bytes.Buffer
	d := &driver{t: t, scripts: scripts}
	return Form{Out: &out, runForm: d.run}, &out, d
}

func TestAddALogin(t *testing.T) {
	r, _ := record.New("login", time.Now())
	f, out, d := newForm(t,
		// the fields: name, description, tags, url, login, password, email, pin, totp, token; then notes
		[]string{"GitHub", enter, "  my account  ", enter, "dev, work,dev", enter, "https://github.com", enter,
			"me", enter, "hunter2 ", enter, enter, enter, enter, "tok", enter, "line one", enter},
		// review: show the secrets, then save
		[]string{down, down, down, down, enter},
		[]string{enter},
	)
	if err := f.Edit(r, "New login"); err != nil {
		t.Fatal(err)
	}
	if d.forms != 3 {
		t.Errorf("%d forms shown, want 3", d.forms)
	}
	want := map[string]string{"url": "https://github.com", "username": "me", "password": "hunter2 ", "token": "tok"}
	if r.Name != "GitHub" || r.Description != "my account" || strings.Join(r.Tags, "|") != "dev|work|dev" ||
		!reflect.DeepEqual(r.Fields, want) || r.Notes != "line one" {
		t.Errorf("record = %+v", r)
	}
	// the review shows the record twice: masked, then revealed
	cards := out.String()
	if strings.Count(cards, "GitHub") != 2 || !strings.Contains(cards, masked) || !strings.Contains(cards, "hunter2") {
		t.Errorf("review output:\n%s", cards)
	}
	if i, j := strings.Index(cards, masked), strings.Index(cards, "hunter2"); i > j {
		t.Error("secrets shown before the reveal")
	}
}

func TestNameIsRequired(t *testing.T) {
	r, _ := record.New("note", time.Now())
	f, _, _ := newForm(t,
		// enter on an empty name stays put: the text typed after it goes into name
		[]string{enter, "x", enter, enter, enter, enter, enter},
		[]string{enter},
	)
	if err := f.Edit(r, "New note"); err != nil {
		t.Fatal(err)
	}
	if r.Name != "x" {
		t.Errorf("name = %q", r.Name)
	}
}

func TestStrictAndLooseChecks(t *testing.T) {
	r, _ := record.New("bank_card", time.Now())
	// a bad card number only warns; a bad TOTP secret would block, but cards have none: check
	// the value is kept. The network is completed from its suggestions with ctrl+e.
	f, _, _ := newForm(t,
		[]string{"Visa", enter, enter, enter, enter, "mas", ctrlE, enter, "1234", enter, enter, enter, enter, enter, enter, enter, enter},
		[]string{enter},
	)
	if err := f.Edit(r, "New bank_card"); err != nil {
		t.Fatal(err)
	}
	if r.Fields["card_no"] != "1234" || r.Fields["card_network"] != "mastercard" {
		t.Errorf("fields = %v", r.Fields)
	}

	l, _ := record.New("login", time.Now())
	// login's TOTP secret is strict: "!!" is refused, so the enter after it doesn't leave the field
	f, _, _ = newForm(t,
		[]string{"L", enter, enter, enter, enter, enter, enter, enter, enter, "!!", enter, ctrlU, "JBSWY3DPEHPK3PXP", enter, enter, enter},
		[]string{enter},
	)
	if err := f.Edit(l, "New login"); err != nil {
		t.Fatal(err)
	}
	if l.Fields["totp_secret"] != "JBSWY3DPEHPK3PXP" {
		t.Errorf("fields = %v", l.Fields)
	}
}

// The warning for a loose check shows under its field once the value is in (on leaving the
// field); the hint shows until then.
func TestWarningShowsUnderTheField(t *testing.T) {
	var card, exp string
	spec := record.Schemas["bank_card"].Fields
	form := huh.NewForm(huh.NewGroup(fieldInput(spec[2], &card), fieldInput(spec[3], &exp), huh.NewInput()))
	d := &driver{t: t}
	d.exec(form, form.Init())
	d.send(form, tea.WindowSizeMsg{Width: 100, Height: 50})
	if view := form.View(); !strings.Contains(view, "MMYY") || strings.Contains(view, "warning") {
		t.Errorf("before:\n%s", view)
	}
	for _, k := range []string{"1234", tab} {
		for _, m := range keyMsg(k) {
			d.send(form, m)
		}
	}
	if view := form.View(); !strings.Contains(view, "warning: a card number has 12 to 19 digits") {
		t.Errorf("after:\n%s", view)
	}
}

func TestEditAnAccount(t *testing.T) {
	r, _ := record.New("bank_account", time.Now())
	r.Name, r.Tags, r.Fields["bank_name"] = "Checking", []string{"home"}, "CU"
	r.Address = &record.Address{City: "Springfield"}
	r.Custom = map[string]string{"old": "x"}
	orig := *r
	f, _, _ := newForm(t,
		// keep everything on the first page (9 fields), then clear the city
		[]string{enter, enter, enter, enter, enter, enter, enter, enter, enter,
			enter, enter, ctrlU, enter, enter, enter, enter},
		// review: custom fields (save, edit, secret questions, custom = 4th)
		[]string{down, down, down, enter},
		// custom menu: add, remove "old", done; remove old
		[]string{down, enter},
		// add one
		[]string{enter},
		[]string{"pin", enter, "4321", enter},
		// done (add, remove pin, done)
		[]string{down, down, enter},
		// save
		[]string{enter},
	)
	if err := f.Edit(r, "Edit bank_account"); err != nil {
		t.Fatal(err)
	}
	if r.Name != "Checking" || r.Fields["bank_name"] != "CU" || r.Address != nil ||
		!reflect.DeepEqual(r.Custom, map[string]string{"pin": "4321"}) || r.ID != orig.ID {
		t.Errorf("record = %+v", r)
	}
}

func TestSecretQuestions(t *testing.T) {
	r, _ := record.New("login", time.Now())
	r.Name = "Bank"
	f, _, _ := newForm(t,
		[]string{enter, enter, enter, enter, enter, enter, enter, enter, enter, enter, enter},
		// review: secret questions (3rd)
		[]string{down, down, enter},
		[]string{enter},                             // add
		[]string{"first pet?", enter, "rex", enter}, // the pair
		[]string{enter},                             // add another
		[]string{"street?", enter, "elm", enter},
		[]string{down, enter},       // remove the first
		[]string{down, down, enter}, // done
		[]string{enter},             // save
	)
	if err := f.Edit(r, "Edit login"); err != nil {
		t.Fatal(err)
	}
	if want := []record.SecretQuestion{{Question: "street?", Answer: "elm"}}; !reflect.DeepEqual(r.SecretQuestions, want) {
		t.Errorf("questions = %+v", r.SecretQuestions)
	}
}

func TestCancel(t *testing.T) {
	r, _ := record.New("note", time.Now())
	r.Name = "keep"
	before := *r
	// ctrl+c in the fields
	f, _, _ := newForm(t, []string{ctrlU, "changed", ctrlC})
	if err := f.Edit(r, "Edit note"); !errors.Is(err, ErrCancelled) {
		t.Errorf("err = %v", err)
	}
	// Cancel at the review: the last option (save, edit, custom, show, cancel)
	f, _, _ = newForm(t,
		[]string{ctrlU, "changed", enter, enter, enter, enter, enter},
		[]string{down, down, down, down, enter},
	)
	if err := f.Edit(r, "Edit note"); !errors.Is(err, ErrCancelled) {
		t.Errorf("err = %v", err)
	}
	if !reflect.DeepEqual(*r, before) {
		t.Errorf("record changed: %+v", r)
	}
}

func TestGoBackToTheFields(t *testing.T) {
	r, _ := record.New("note", time.Now())
	f, _, d := newForm(t,
		[]string{"first", enter, enter, enter, enter, enter},
		[]string{down, enter}, // edit the fields
		[]string{ctrlU, "second", enter, enter, enter, enter, enter},
		[]string{enter},
	)
	if err := f.Edit(r, "New note"); err != nil {
		t.Fatal(err)
	}
	if r.Name != "second" || d.forms != 4 {
		t.Errorf("name %q after %d forms", r.Name, d.forms)
	}
}

func TestCard(t *testing.T) {
	r, _ := record.New("login", time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local))
	r.Name, r.Notes = "GitHub", "two\nlines"
	r.Fields["password"], r.Fields["url"] = "s3cret", "https://x"
	r.SecretQuestions = []record.SecretQuestion{{Question: "pet?", Answer: "rex"}}
	r.Custom = map[string]string{"k": "v"}
	masked1 := Card(r, false, false)
	for _, s := range []string{"GitHub", "login", r.ID, "https://x", "pet?", "custom: k", "two", "lines", "created 2026-01-02"} {
		if !strings.Contains(masked1, s) {
			t.Errorf("card lacks %q:\n%s", s, masked1)
		}
	}
	for _, s := range []string{"s3cret", "rex"} {
		if strings.Contains(masked1, s) {
			t.Errorf("masked card shows %q", s)
		}
	}
	if shown := Card(r, true, false); !strings.Contains(shown, "s3cret") || !strings.Contains(shown, "rex") {
		t.Errorf("revealed card:\n%s", shown)
	}
	if empty := Card(&record.Record{Type: "note"}, false, false); !strings.Contains(empty, "(no name)") {
		t.Errorf("empty card:\n%s", empty)
	}
}

func TestPick(t *testing.T) {
	var rs []*record.Record
	for _, n := range []string{"alpha", "bravo", "charlie"} {
		r, _ := record.New("note", time.Now())
		r.Name, r.Description, r.Fields["content"] = n, "the "+n+"\nnote", "secret-"+n
		rs = append(rs, r)
	}
	f, _, _ := newForm(t, []string{down, enter})
	if got, err := f.Pick(rs, "Edit which record?"); err != nil || got != rs[1] {
		t.Errorf("Pick = %v, %v; want bravo", got, err)
	}
	// / filters the list
	f, _, _ = newForm(t, []string{"/", "char", enter, enter})
	if got, err := f.Pick(rs, "Edit which record?"); err != nil || got != rs[2] {
		t.Errorf("filtered Pick = %v, %v; want charlie", got, err)
	}
	f, _, _ = newForm(t, []string{ctrlC})
	if _, err := f.Pick(rs, "Edit which record?"); !errors.Is(err, ErrCancelled) {
		t.Errorf("cancelled Pick: %v", err)
	}
}

// The list shows the id, type, name and description, never a secret.
func TestPickShowsNoSecrets(t *testing.T) {
	r, _ := record.New("login", time.Now())
	r.Name, r.Description, r.Fields["password"] = "GitHub", "work\naccount", "hunter2"
	var view string
	d := &driver{t: t, scripts: [][]string{{enter}}}
	f := Form{runForm: func(form *huh.Form) error {
		d.exec(form, form.Init())
		d.send(form, tea.WindowSizeMsg{Width: 100, Height: 50})
		view = form.View()
		return d.run(form)
	}}
	if _, err := f.Pick([]*record.Record{r}, "pick"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(view, shortID(r.ID)+"  login  GitHub  · work account") || strings.Contains(view, "hunter2") {
		t.Errorf("list:\n%s", view)
	}
}
