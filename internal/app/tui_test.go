package app

import (
	"strings"
	"testing"

	"github.com/rhugga/digisafe/internal/record"
	"github.com/rhugga/digisafe/internal/tui"
)

// fakeForm stands in for the interactive form: it records what it was shown and applies edit.
type fakeForm struct {
	titles []string
	edit   func(r *record.Record) error
	pick   func(rs []*record.Record) (*record.Record, error)
}

func (f *fakeForm) Pick(rs []*record.Record, title string) (*record.Record, error) {
	f.titles = append(f.titles, title)
	return f.pick(rs)
}

func (f *fakeForm) Edit(r *record.Record, title string) error {
	f.titles = append(f.titles, title)
	return f.edit(r)
}

func TestAddAndEditUseTheForm(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	form := &fakeForm{edit: func(r *record.Record) error {
		r.Name, r.Fields["password"] = "  GitHub ", "pw"
		return nil
	}}
	h.app.Form = form
	// the form replaces the field prompts: only the passphrase is asked
	if out := h.must([]string{pass}, "record", "add", "login"); !strings.Contains(out, "added login") {
		t.Errorf("add: %q", out)
	}
	id := h.id("GitHub") // the name was normalised before saving

	form.edit = func(r *record.Record) error {
		if r.Fields["password"] != "pw" {
			t.Errorf("the form got %+v", r)
		}
		r.Description = "edited"
		return nil
	}
	h.must([]string{pass}, "record", "edit", id)
	if want := []string{"New login", "Edit login " + shortID(id)}; strings.Join(form.titles, "|") != strings.Join(want, "|") {
		t.Errorf("titles = %q", form.titles)
	}
	if out := h.must([]string{pass}, "record", "show", id); !strings.Contains(out, "edited") {
		t.Errorf("show: %s", out)
	}

	// cancelling saves nothing
	form.edit = func(r *record.Record) error { r.Name = "nope"; return tui.ErrCancelled }
	h.mustFail([]string{pass}, "aborted", "record", "add", "note")
	h.mustFail([]string{pass}, "aborted", "record", "edit", id)
	if out := h.must([]string{pass}, "ls"); strings.Contains(out, "nope") || strings.Count(out, "\n") != 2 {
		t.Errorf("after cancelling:\n%s", out)
	}

	// --plain, and the flags and --file paths, don't use the form
	n := len(form.titles)
	h.must([]string{pass, "Plain", "", "", "", "", "", "", "", "", "", "", "", ""}, "r", "a", "login", "--plain")
	h.must([]string{pass}, "r", "a", "note", "--name", "Flags", "--no-prompt")
	t.Setenv("DIGISAFE_PLAIN", "1")
	h.must([]string{pass, "", "", "", "", "", "", "", "", "", "", "", "", ""}, "r", "e", id)
	if len(form.titles) != n {
		t.Errorf("the form was used: %q", form.titles[n:])
	}
}

func TestStyledShow(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	h.must([]string{pass}, "r", "a", "note", "--name", "Card", "--description", "a note", "--no-prompt")
	id := h.id("Card")
	h.app.Styled = true
	out := h.must([]string{pass}, "record", "show", id)
	if !strings.Contains(out, "╭") || !strings.Contains(out, "a note") {
		t.Errorf("styled show:\n%s", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Errorf("colour codes written to a non-terminal:\n%q", out)
	}
	if out := h.must([]string{pass}, "record", "show", id, "--plain"); strings.Contains(out, "╭") || !strings.Contains(out, "description:") {
		t.Errorf("--plain show:\n%s", out)
	}
}

func TestEditPicksARecord(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	for _, a := range [][]string{
		{"r", "a", "note", "--name", "N1", "--no-prompt"},
		{"r", "a", "login", "--name", "L1", "--no-prompt"},
		{"r", "a", "login", "--name", "L2", "--no-prompt"},
	} {
		h.must([]string{pass}, a...)
	}
	var offered []string
	form := &fakeForm{
		pick: func(rs []*record.Record) (*record.Record, error) {
			offered = nil
			for _, r := range rs {
				offered = append(offered, r.Name)
			}
			return rs[len(rs)-1], nil
		},
		edit: func(r *record.Record) error { r.Description = "picked"; return nil },
	}
	h.app.Form = form

	// every record, then just the logins
	if out := h.must([]string{pass}, "record", "edit"); !strings.Contains(out, "updated") {
		t.Errorf("edit: %q", out)
	}
	if len(offered) != 3 {
		t.Errorf("offered %q", offered)
	}
	h.must([]string{pass}, "r", "e", "--type", "login")
	if strings.Join(sortedStrings(offered), ",") != "L1,L2" {
		t.Errorf("--type login offered %q", offered)
	}
	if len(form.titles) != 4 || form.titles[0] != "Edit which record?" || form.titles[2] != "Edit which login?" {
		t.Errorf("titles %q", form.titles)
	}
	if out := h.must([]string{pass}, "q", "", "--type", "login"); strings.Count(out, "picked") != 1 {
		t.Errorf("one login should be edited:\n%s", out)
	}

	// cancelling the list saves nothing
	form.pick = func([]*record.Record) (*record.Record, error) { return nil, tui.ErrCancelled }
	h.mustFail([]string{pass}, "aborted", "record", "edit")

	// the old type name still works as a filter
	h.mustFail([]string{pass}, "no bank_card records", "record", "edit", "--type", "credit_card")
	h.mustFail(nil, "unknown record type", "record", "edit", "--type", "car")
	h.mustFail(nil, "--type only goes with no id", "record", "edit", "--type", "login", "abc")
	h.mustFail(nil, "usage", "record", "edit", "a", "b")
	// no list without a terminal (or with --plain): the id is needed
	h.mustFail(nil, "needs a terminal", "record", "edit", "--plain")
	// --plain belongs to record add, edit and show only
	h.mustFail(nil, "flag provided but not defined: -plain", "--plain", "ls")
	h.mustFail(nil, "flag provided but not defined: -plain", "keys", "list", "--plain")
	h.app.Form = nil
	h.mustFail(nil, "needs a terminal", "record", "edit")
}

func TestEditPickWithNoRecords(t *testing.T) {
	h := newHarness(t)
	h.must([]string{pass, pass}, "safe", "create")
	h.app.Form = &fakeForm{}
	h.mustFail([]string{pass}, "no records in", "record", "edit")
}
