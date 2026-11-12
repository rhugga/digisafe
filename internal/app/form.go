package app

import (
	"fmt"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/rhugga/digisafe/internal/record"
)

// clearValue typed at an edit prompt empties the field; Enter keeps it.
const clearValue = "-"

// label renders a field prompt: "card number (MMYY)".
func label(name, hint string) string {
	if hint != "" {
		return fmt.Sprintf("%s (%s)", name, hint)
	}
	return name
}

// fill prompts for every field of a new record. Empty answers leave fields empty. nameOK checks
// the name (unique in the safe); a taken one is asked again.
func (a *App) fill(r *record.Record, nameOK func(string) error) error {
	s := r.Schema()
	a.UI.Message("new %s: press Enter to leave a field empty; secret fields are not echoed", r.Type)
	for r.Name == "" {
		v, err := a.UI.Line("name: ")
		if err != nil {
			return err
		}
		r.Name = strings.TrimSpace(v)
		switch err := nameOK(r.Name); {
		case r.Name == "":
			a.UI.Message("a record needs a name")
		case err != nil:
			a.UI.Message("%v; pick another", err)
			r.Name = ""
		}
	}
	var err error
	if r.Description, err = a.UI.Line("description: "); err != nil {
		return err
	}
	tags, err := a.UI.Line("tags (comma-separated): ")
	if err != nil {
		return err
	}
	r.Tags = splitTags(tags)
	for _, f := range s.Fields {
		v, err := a.askField(f, "", false)
		if err != nil {
			return err
		}
		if v != "" {
			r.Fields[f.Name] = v
		}
	}
	if s.Address {
		r.Address = &record.Address{}
		for _, f := range record.AddressFields {
			v, err := a.UI.Line("address " + f.Label + ": ")
			if err != nil {
				return err
			}
			f.Set(r.Address, strings.TrimSpace(v))
		}
	}
	if s.SecretQuestions {
		if r.SecretQuestions, err = a.askQuestions(); err != nil {
			return err
		}
	}
	if err := a.askCustom(r); err != nil {
		return err
	}
	if r.Notes, err = a.UI.Line("notes: "); err != nil {
		return err
	}
	return nil
}

// askField prompts for one schema field, re-asking when a strict check fails and warning when a
// loose one does. With editing, Enter keeps current and "-" clears it.
func (a *App) askField(f record.FieldSpec, current string, editing bool) (string, error) {
	p := label(f.Label, f.Hint)
	switch {
	case editing && f.Secret && current != "":
		p += " [set]"
	case editing && current != "":
		p += " [" + current + "]"
	}
	for attempt := 1; ; attempt++ {
		var v string
		var err error
		if f.Secret {
			v, err = a.UI.Secret(p + ": ")
		} else {
			v, err = a.UI.Line(p + ": ")
		}
		if err != nil {
			return "", err
		}
		if !f.Secret {
			v = strings.TrimSpace(v)
		}
		if editing {
			switch v {
			case "":
				return current, nil
			case clearValue:
				return "", nil
			}
		}
		if v == "" || f.Check == nil {
			return v, nil
		}
		msg := f.Check(v)
		if msg == "" {
			return v, nil
		}
		if !f.Strict {
			a.UI.Message("warning: %s: %s", f.Name, msg)
			return v, nil
		}
		if attempt == 3 {
			return "", fmt.Errorf("%s: %s", f.Name, msg)
		}
		a.UI.Message("%s: %s, try again", f.Name, msg)
	}
}

func (a *App) askQuestions() ([]record.SecretQuestion, error) {
	var qs []record.SecretQuestion
	for {
		q, err := a.UI.Line(fmt.Sprintf("secret question %d (Enter to finish): ", len(qs)+1))
		if err != nil {
			return nil, err
		}
		if q = strings.TrimSpace(q); q == "" {
			return qs, nil
		}
		ans, err := a.UI.Secret("answer: ")
		if err != nil {
			return nil, err
		}
		qs = append(qs, record.SecretQuestion{Question: q, Answer: ans})
	}
}

// askCustom adds, changes or removes custom fields: a new key adds it, an existing key's value
// is replaced ("-" removes it). Values are read like secrets.
func (a *App) askCustom(r *record.Record) error {
	for {
		k, err := a.UI.Line("custom field name (Enter to finish): ")
		if err != nil {
			return err
		}
		if k = strings.TrimSpace(k); k == "" {
			return nil
		}
		p := "value: "
		if _, ok := r.Custom[k]; ok {
			p = "value [set, " + clearValue + " removes]: "
		}
		v, err := a.UI.Secret(p)
		if err != nil {
			return err
		}
		if r.Custom == nil {
			r.Custom = map[string]string{}
		}
		switch {
		case v == clearValue:
			delete(r.Custom, k)
		case v != "":
			r.Custom[k] = v
		}
	}
}

// edit prompts for every field of r: Enter keeps a value, "-" clears it. nameOK checks a new name
// (unique in the safe); a taken one is asked again.
func (a *App) edit(r *record.Record, nameOK func(string) error) error {
	s := r.Schema()
	a.UI.Message("editing %s %s: Enter keeps a value, %q clears it", r.Type, shortID(r.ID), clearValue)
	keep := func(p, cur string) (string, error) {
		if cur != "" {
			p += " [" + oneLine(cur, 60) + "]"
		}
		v, err := a.UI.Line(p + ": ")
		if err != nil {
			return "", err
		}
		switch v = strings.TrimSpace(v); v {
		case "":
			return cur, nil
		case clearValue:
			return "", nil
		}
		return v, nil
	}
	var err error
	for name := r.Name; ; {
		if name, err = keep("name", name); err != nil {
			return err
		}
		if name == "" {
			a.UI.Message("a record needs a name")
			name = r.Name
			continue
		}
		if err := nameOK(name); err != nil {
			a.UI.Message("%v; pick another", err)
			name = r.Name
			continue
		}
		r.Name = name
		break
	}
	if r.Description, err = keep("description", r.Description); err != nil {
		return err
	}
	tags, err := keep("tags (comma-separated)", strings.Join(r.Tags, ", "))
	if err != nil {
		return err
	}
	r.Tags = splitTags(tags)
	for _, f := range s.Fields {
		v, err := a.askField(f, r.Fields[f.Name], true)
		if err != nil {
			return err
		}
		r.Fields[f.Name] = v
	}
	if s.Address {
		if r.Address == nil {
			r.Address = &record.Address{}
		}
		for _, f := range record.AddressFields {
			v, err := keep("address "+f.Label, f.Get(r.Address))
			if err != nil {
				return err
			}
			f.Set(r.Address, v)
		}
	}
	if s.SecretQuestions {
		replace, err := a.UI.Confirm(fmt.Sprintf("%d secret question(s); replace them?", len(r.SecretQuestions)), false)
		if err != nil {
			return err
		}
		if replace {
			if r.SecretQuestions, err = a.askQuestions(); err != nil {
				return err
			}
		}
	}
	if len(r.Custom) > 0 {
		var keys []string
		for k := range r.Custom {
			keys = append(keys, k)
		}
		a.UI.Message("custom fields: %s", strings.Join(sortedStrings(keys), ", "))
	}
	if err := a.askCustom(r); err != nil {
		return err
	}
	if r.Notes, err = keep("notes", r.Notes); err != nil {
		return err
	}
	return nil
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

// fieldFlagsSet reports whether any of add's field flags were given.
func fieldFlagsSet(cmd *cli.Command) bool {
	for _, f := range []string{"name", "description", "tags", "notes", "field"} {
		if cmd.IsSet(f) {
			return true
		}
	}
	return false
}

// applyFieldFlags sets r's fields from add's flags. Only non-secret fields are accepted: a
// secret on the command line would land in shell history and the process list.
func applyFieldFlags(r *record.Record, cmd *cli.Command) error {
	s := r.Schema()
	if r.Name = strings.TrimSpace(cmd.String("name")); r.Name == "" {
		return fmt.Errorf("--name is required with the field flags")
	}
	r.Description = cmd.String("description")
	r.Tags = splitTags(cmd.String("tags"))
	r.Notes = cmd.String("notes")
	for _, kv := range cmd.StringSlice("field") {
		k, v, ok := strings.Cut(kv, "=")
		if k = strings.TrimSpace(k); !ok || k == "" {
			return fmt.Errorf("--field %q: want name=value", kv)
		}
		if a, isAddr := strings.CutPrefix(k, "address."); isAddr {
			if !s.Address {
				return fmt.Errorf("--field %s: %s records have no address", k, r.Type)
			}
			if err := setAddress(r, a, v); err != nil {
				return err
			}
			continue
		}
		k = record.CanonicalField(r.Type, k)
		f, known := s.Field(k)
		switch {
		case !known:
			return fmt.Errorf("--field %s: %s records have no field %s (fields: %s)", k, r.Type, k, strings.Join(s.FieldNames(), ", "))
		case f.Secret:
			return fmt.Errorf("--field %s: %s is secret, so it can't be given on the command line "+
				"(it would end up in shell history); you'll be prompted for it, or use --file -", k, k)
		}
		r.Fields[k] = strings.TrimSpace(v)
	}
	return nil
}

func setAddress(r *record.Record, name, v string) error {
	if r.Address == nil {
		r.Address = &record.Address{}
	}
	var names []string
	for _, f := range record.AddressFields {
		if f.Name == name {
			f.Set(r.Address, strings.TrimSpace(v))
			return nil
		}
		names = append(names, f.Name)
	}
	return fmt.Errorf("--field address.%s: no such address field (fields: %s)", name, strings.Join(names, ", "))
}

// fillSecrets prompts for just the type's secret fields (the rest came from flags).
func (a *App) fillSecrets(r *record.Record) error {
	var secrets []record.FieldSpec
	for _, f := range r.Schema().Fields {
		if f.Secret {
			secrets = append(secrets, f)
		}
	}
	if len(secrets) == 0 {
		return nil
	}
	a.UI.Message("%s %q: enter the secret fields (Enter leaves one empty; they are not echoed)", r.Type, r.Name)
	for _, f := range secrets {
		v, err := a.askField(f, "", false)
		if err != nil {
			return err
		}
		if v != "" {
			r.Fields[f.Name] = v
		}
	}
	return nil
}
