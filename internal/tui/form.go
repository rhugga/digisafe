// Package tui is digisafe's interactive terminal UI: the record form (huh, on bubbletea) and
// the styled output (lipgloss). The app only uses it on a terminal; pipes, files and --plain get
// the line-by-line prompts and plain text.
package tui

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/rhugga/digisafe/internal/record"
)

// ErrCancelled is returned when the user cancels the form (ctrl+c, or Cancel at the review).
var ErrCancelled = errors.New("cancelled")

// Form edits records in a form the user can move around in before saving.
type Form struct {
	In  io.Reader // the terminal
	Out io.Writer // where the form draws: stderr, so stdout stays clean

	runForm func(*huh.Form) error // tests drive the forms with key messages; nil runs them on In
}

// draft holds the form's values while the user edits; r only changes on Save.
type draft struct {
	name, description, tags, notes string
	fields                         map[string]*string
	address                        []string
	questions                      []record.SecretQuestion
	custom                         map[string]string
}

func newDraft(r *record.Record) *draft {
	d := &draft{
		name:        r.Name,
		description: r.Description,
		tags:        strings.Join(r.Tags, ", "),
		notes:       r.Notes,
		fields:      map[string]*string{},
		address:     make([]string, len(record.AddressFields)),
		questions:   slices.Clone(r.SecretQuestions),
		custom:      maps.Clone(r.Custom),
	}
	for _, f := range r.Schema().Fields {
		v := r.Fields[f.Name]
		d.fields[f.Name] = &v
	}
	if r.Address != nil {
		for i, f := range record.AddressFields {
			d.address[i] = f.Get(r.Address)
		}
	}
	if d.custom == nil {
		d.custom = map[string]string{}
	}
	return d
}

// apply returns a copy of r with the draft's values; r itself is untouched.
func (d *draft) apply(r *record.Record) *record.Record {
	out := *r
	out.Name = strings.TrimSpace(d.name)
	out.Description = strings.TrimSpace(d.description)
	out.Tags = splitTags(d.tags)
	out.Notes = d.notes
	out.Fields = map[string]string{}
	for _, f := range r.Schema().Fields {
		v := *d.fields[f.Name]
		if !f.Secret {
			v = strings.TrimSpace(v)
		}
		if v != "" {
			out.Fields[f.Name] = v
		}
	}
	out.Address = nil
	if r.Schema().Address {
		a := &record.Address{}
		for i, f := range record.AddressFields {
			f.Set(a, strings.TrimSpace(d.address[i]))
		}
		if !a.IsZero() {
			out.Address = a
		}
	}
	out.SecretQuestions = slices.Clone(d.questions)
	out.Custom = nil
	if len(d.custom) > 0 {
		out.Custom = maps.Clone(d.custom)
	}
	return &out
}

func splitTags(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// Edit shows r in a form: the user moves between fields (tab, shift+tab, arrows), reviews the
// record and saves, goes back to change it, or cancels. On Save, r gets the new values; on
// cancel it's unchanged and Edit returns ErrCancelled.
func (f Form) Edit(r *record.Record, title string) error {
	d := newDraft(r)
	s := r.Schema()
	if err := f.run(d.fieldsForm(s, title)); err != nil {
		return err
	}
	reveal := false
	for {
		preview := d.apply(r)
		lipgloss.Fprintln(f.Out, Card(preview, reveal, false))
		choice, err := f.review(s, d, reveal)
		if err != nil {
			return err
		}
		switch choice {
		case "save":
			*r = *preview
			return nil
		case "cancel":
			return ErrCancelled
		case "fields":
			err = f.run(d.fieldsForm(s, title))
		case "questions":
			err = f.questions(d)
		case "custom":
			err = f.customFields(d)
		case "reveal":
			reveal = !reveal
		}
		if err != nil {
			return err
		}
	}
}

func (f Form) run(form *huh.Form) error {
	form = form.WithTheme(huh.ThemeFunc(huh.ThemeCharm))
	var err error
	if f.runForm != nil {
		err = f.runForm(form)
	} else {
		err = form.WithInput(f.In).WithOutput(f.Out).Run()
	}
	if errors.Is(err, huh.ErrUserAborted) {
		return ErrCancelled
	}
	return err
}

// fieldsForm is the main form: the details and the type's fields on one page, the address (if
// the type has one) on the next, then the notes.
func (d *draft) fieldsForm(s record.Schema, title string) *huh.Form {
	details := []huh.Field{
		huh.NewInput().Title("name").Value(&d.name).Validate(func(v string) error {
			if strings.TrimSpace(v) == "" {
				return errors.New("a record needs a name")
			}
			return nil
		}),
		huh.NewInput().Title("description").Value(&d.description),
		huh.NewInput().Title("tags").Description("comma-separated").Value(&d.tags),
	}
	for _, spec := range s.Fields {
		details = append(details, fieldInput(spec, d.fields[spec.Name]))
	}
	groups := []*huh.Group{huh.NewGroup(details...).Title(title).
		Description("tab / shift+tab move between fields; ctrl+c cancels")}
	var last []huh.Field
	if s.Address {
		for i, f := range record.AddressFields {
			last = append(last, huh.NewInput().Title("address: "+f.Label).Value(&d.address[i]))
		}
	}
	last = append(last, huh.NewText().Title("notes").Value(&d.notes).Lines(4).
		ExternalEditor(false)) // the editor would write the notes to a temporary file
	groups = append(groups, huh.NewGroup(last...))
	return huh.NewForm(groups...)
}

// fieldInput is one schema field: secrets are masked, strict checks stop the form until fixed,
// loose ones show a warning under the field.
func fieldInput(spec record.FieldSpec, v *string) *huh.Input {
	in := huh.NewInput().Title(spec.Label).Value(v)
	if spec.Secret {
		in.EchoMode(huh.EchoModePassword)
	}
	hint := spec.Hint
	if len(spec.Suggest) > 0 {
		in.Suggestions(spec.Suggest)
		hint += "; ctrl+e completes"
	}
	check := func(s string) string {
		if !spec.Secret {
			s = strings.TrimSpace(s)
		}
		if s == "" || spec.Check == nil {
			return ""
		}
		return spec.Check(s)
	}
	if spec.Strict {
		in.Validate(func(s string) error {
			if msg := check(s); msg != "" {
				return errors.New(msg)
			}
			return nil
		})
	}
	in.DescriptionFunc(func() string {
		if msg := check(*v); msg != "" && !spec.Strict {
			return "warning: " + msg
		}
		return hint
	}, v)
	return in
}

// review asks what to do with the record shown above it.
func (f Form) review(s record.Schema, d *draft, reveal bool) (string, error) {
	opts := []huh.Option[string]{
		huh.NewOption("Save", "save"),
		huh.NewOption("Edit the fields", "fields"),
	}
	if s.SecretQuestions {
		opts = append(opts, huh.NewOption(fmt.Sprintf("Secret questions (%d)", len(d.questions)), "questions"))
	}
	opts = append(opts, huh.NewOption(fmt.Sprintf("Custom fields (%d)", len(d.custom)), "custom"))
	if reveal {
		opts = append(opts, huh.NewOption("Hide the secrets", "reveal"))
	} else {
		opts = append(opts, huh.NewOption("Show the secrets", "reveal"))
	}
	opts = append(opts, huh.NewOption("Cancel", "cancel"))
	choice := "save"
	err := f.run(huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().Title("Save this " + s.Type + "?").Options(opts...).Value(&choice))))
	return choice, err
}

// questions adds and removes secret question / answer pairs.
func (f Form) questions(d *draft) error {
	for {
		opts := []huh.Option[int]{huh.NewOption("Add a secret question", -1)}
		for i, q := range d.questions {
			opts = append(opts, huh.NewOption("Remove: "+q.Question, i))
		}
		opts = append(opts, huh.NewOption("Done", -2))
		choice := -1
		if err := f.run(huh.NewForm(huh.NewGroup(
			huh.NewSelect[int]().Title("Secret questions").Options(opts...).Value(&choice)))); err != nil {
			return err
		}
		switch {
		case choice == -2:
			return nil
		case choice >= 0:
			d.questions = slices.Delete(d.questions, choice, choice+1)
			continue
		}
		var q record.SecretQuestion
		if err := f.run(huh.NewForm(huh.NewGroup(
			huh.NewInput().Title("question").Value(&q.Question).Validate(nonEmpty("a question")),
			huh.NewInput().Title("answer").EchoMode(huh.EchoModePassword).Value(&q.Answer),
		))); err != nil {
			return err
		}
		q.Question = strings.TrimSpace(q.Question)
		d.questions = append(d.questions, q)
	}
}

// customFields adds, changes and removes custom fields. Their values are treated as secrets.
func (f Form) customFields(d *draft) error {
	for {
		opts := []huh.Option[string]{huh.NewOption("Add or change a custom field", "")}
		for _, k := range slices.Sorted(maps.Keys(d.custom)) {
			opts = append(opts, huh.NewOption("Remove: "+k, k))
		}
		opts = append(opts, huh.NewOption("Done", "\x00done"))
		var choice string
		if err := f.run(huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().Title("Custom fields").Options(opts...).Value(&choice)))); err != nil {
			return err
		}
		switch choice {
		case "\x00done":
			return nil
		case "":
		default:
			delete(d.custom, choice)
			continue
		}
		var k, v string
		if err := f.run(huh.NewForm(huh.NewGroup(
			huh.NewInput().Title("field name").Value(&k).Validate(nonEmpty("a name")),
			huh.NewInput().Title("value").EchoMode(huh.EchoModePassword).Value(&v).Validate(nonEmpty("a value")),
		))); err != nil {
			return err
		}
		d.custom[strings.TrimSpace(k)] = v
	}
}

func nonEmpty(what string) func(string) error {
	return func(v string) error {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("needs %s", what)
		}
		return nil
	}
}

// Pick shows the records in a list to choose from: up and down move, / filters, Enter picks.
// It shows what `record list` shows, never a secret.
func (f Form) Pick(rs []*record.Record, title string) (*record.Record, error) {
	opts := make([]huh.Option[*record.Record], len(rs))
	for i, r := range rs {
		label := shortID(r.ID) + "  " + r.Type + "  " + record.Printable(r.Name)
		if d := strings.Join(strings.Fields(record.Printable(r.Description)), " "); d != "" {
			label += "  · " + d
		}
		opts[i] = huh.NewOption(label, r)
	}
	var picked *record.Record
	err := f.run(huh.NewForm(huh.NewGroup(
		huh.NewSelect[*record.Record]().
			Title(title).
			Description("↑/↓ move, / filters, enter picks, ctrl+c cancels").
			Options(opts...).
			Height(min(len(rs), 12) + 3).
			Value(&picked))))
	if err != nil {
		return nil, err
	}
	return picked, nil
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
