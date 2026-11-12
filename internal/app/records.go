package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/urfave/cli/v3"

	"github.com/rhugga/digisafe/internal/record"
	"github.com/rhugga/digisafe/internal/safe"
	"github.com/rhugga/digisafe/internal/totp"
	"github.com/rhugga/digisafe/internal/tui"
)

func sortedStrings(s []string) []string {
	sort.Strings(s)
	return s
}

func (a *App) typesCmd() *cli.Command {
	return &cli.Command{
		Name:  "types",
		Usage: "list the record types and their fields",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			for _, t := range record.TypeNames() {
				s := record.Schemas[t]
				fmt.Fprintf(a.Out, "%s: %s\n", t, s.Description)
				for _, f := range s.Fields {
					extra := []string{}
					if f.Secret {
						extra = append(extra, "secret")
					}
					if f.Hint != "" {
						extra = append(extra, f.Hint)
					}
					note := ""
					if len(extra) > 0 {
						note = " (" + strings.Join(extra, "; ") + ")"
					}
					fmt.Fprintf(a.Out, "  %-20s %s%s\n", f.Name, f.Label, note)
				}
				if s.Address {
					fmt.Fprintf(a.Out, "  %-20s street_1, street_2, city, state, zip_code\n", "address")
				}
				if s.SecretQuestions {
					fmt.Fprintf(a.Out, "  %-20s question / answer (secret) pairs\n", "secret_questions")
				}
			}
			fmt.Fprintln(a.Out, "\nevery record also has: name (required), description, tags, notes, custom fields")
			return nil
		},
	}
}

func (a *App) addCmd() *cli.Command {
	return &cli.Command{
		Name:      "add",
		Usage:     "add a record: a form on a terminal (--plain prompts per field), or from flags or --file",
		ArgsUsage: "<type>",
		// --field values may hold commas ("Suite 4, Floor 2"); repeat --field for several. This is
		// per command in urfave/cli: it isn't inherited from the root.
		DisableSliceFlagSeparator: true,
		Description: "Fields can be given as flags; then only the type's secret fields are prompted for\n" +
			"(--no-prompt skips those too, leaving them empty). Secret fields are never accepted as\n" +
			"flags, since command lines end up in shell history and the process list: type them at the\n" +
			"prompt, or give the whole record as JSON with --file (- reads it from stdin).\n\n" +
			"  digisafe record add login --name GitHub --tags dev,work --field url=https://github.com --field username=me\n" +
			"  digisafe record add bank_account --name Checking --field bank_name=CU --field address.city=Springfield\n" +
			"  some-tool --json | digisafe record add login --file -",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "file", Usage: "read the record from a JSON file instead of prompting (- for stdin)"},
			&cli.StringFlag{Name: "name", Usage: "the record's name"},
			&cli.StringFlag{Name: "description", Usage: "its description"},
			&cli.StringFlag{Name: "tags", Usage: "comma-separated tags"},
			&cli.StringFlag{Name: "notes", Usage: "free-text notes"},
			&cli.StringSliceFlag{Name: "field", Usage: "a non-secret field: name=value, or address.<name>=value (repeatable)"},
			&cli.BoolFlag{Name: "no-prompt", Usage: "with field flags, don't prompt for the secret fields (they stay empty)"},
			plainFlag(),
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if err := needArgs(cmd, 1); err != nil {
				return err
			}
			typ := cmd.Args().First()
			r, err := record.New(typ, a.now())
			if err != nil {
				return err
			}
			byFlags := fieldFlagsSet(cmd)
			file := cmd.String("file")
			switch {
			case file != "" && byFlags:
				return errors.New("--file and the field flags can't be combined: put every field in the file")
			case cmd.Bool("no-prompt") && !byFlags:
				return errors.New("--no-prompt goes with the field flags (--name, --field, ...)")
			}
			if byFlags {
				// before unlocking, so a typo doesn't cost a passphrase prompt
				if err := applyFieldFlags(r, cmd); err != nil {
					return err
				}
			}
			// read under a shared lock, and drop it while the record is filled in: a form left
			// open mustn't block other commands. Reacquire re-reads the safe if it changed.
			s, err := a.open(cmd, false)
			if err != nil {
				return err
			}
			defer s.Close()
			s.Release()
			nameOK := func(name string) error { return s.Doc.CheckName(name, r.ID) }
			if byFlags {
				// before the secret prompts: a taken name would waste them
				if err := nameOK(r.Name); err != nil {
					return err
				}
			}
			if file != "" {
				recs, err := readRecords(a.In, file)
				if err != nil {
					return err
				}
				if len(recs) != 1 {
					return fmt.Errorf("%s holds %d records; add takes one (use import for several)", file, len(recs))
				}
				in := recs[0]
				if in.Type != "" && in.Type != r.Type {
					return fmt.Errorf("%s is a %s record, not %s", file, in.Type, r.Type)
				}
				in.ID, in.Type, in.CreatedAt, in.UpdatedAt = r.ID, r.Type, r.CreatedAt, r.UpdatedAt
				if in.Fields == nil {
					in.Fields = map[string]string{}
				}
				r = in
			} else if byFlags {
				if !cmd.Bool("no-prompt") {
					if err := a.fillSecrets(r); err != nil {
						return err
					}
				}
			} else if f := a.form(cmd); f != nil {
				if err := f.Edit(r, "New "+r.Type, nameOK); err != nil {
					return formErr(err)
				}
			} else if err := a.fill(r, nameOK); err != nil {
				return err
			}
			if _, err := s.Reacquire(); err != nil {
				return err
			}
			if err := a.store(s, r); err != nil {
				return err
			}
			if file != "" && file != "-" {
				a.UI.Message("note: %s still holds the record unencrypted; delete it", file)
			}
			fmt.Fprintf(a.Out, "added %s %s (%s)\n", r.Type, shortID(r.ID), r.Name)
			return nil
		},
	}
}

// historySummary lists the fields with previous values: "password (2), token (1)".
func historySummary(r *record.Record) string {
	var parts []string
	for _, f := range r.Schema().Fields {
		if n := len(r.History[f.Name]); n > 0 {
			parts = append(parts, fmt.Sprintf("%s (%d)", f.Name, n))
		}
	}
	return strings.Join(parts, ", ")
}

// reacquireFor takes the safe's lock back after editing r. If another digisafe saved the safe
// meanwhile, the edit still applies as long as that save left r alone (orig is r as it was
// read); then r replaces its copy in the re-read safe.
func (a *App) reacquireFor(s *safe.Safe, r *record.Record, orig []byte) error {
	changed, err := s.Reacquire()
	if err != nil || !changed {
		return err
	}
	cur := s.Doc.Get(r.ID)
	if cur == nil {
		return fmt.Errorf("another digisafe deleted %s %s while you were editing it; nothing was saved", r.Type, shortID(r.ID))
	}
	if now, err := json.Marshal(cur); err != nil || !bytes.Equal(now, orig) {
		return fmt.Errorf("another digisafe changed %s %s while you were editing it; nothing was saved (edit it again)", r.Type, shortID(r.ID))
	}
	s.Doc.Delete(r.ID)
	return s.Doc.Add(r)
}

// formErr maps the form's cancel to errAborted, so nothing is saved and the exit says so.
func formErr(err error) error {
	if errors.Is(err, tui.ErrCancelled) {
		return errAborted
	}
	return err
}

// store validates r, adds it and saves the safe.
func (a *App) store(s *safe.Safe, r *record.Record) error {
	r.Normalize()
	warnings, err := r.Validate()
	if err != nil {
		return err
	}
	for _, w := range warnings {
		a.UI.Message("warning: %s", w)
	}
	if err := s.Doc.Add(r); err != nil {
		return err
	}
	return s.Save()
}

// isExport reports whether b is an export (a document with "records" at the top level), not a
// single record: records have no "records" field, but one may mention the word anywhere else.
func isExport(b []byte) bool {
	var top map[string]json.RawMessage
	if json.Unmarshal(b, &top) != nil {
		return false
	}
	_, ok := top["records"]
	return ok
}

// readRecords reads a JSON file holding one record, a list of records, or an export. path "-"
// reads stdin, so records can be piped in without a plaintext file on disk.
func readRecords(stdin io.Reader, path string) ([]*record.Record, error) {
	var b []byte
	var err error
	if path == "-" {
		b, err = io.ReadAll(stdin)
		path = "stdin"
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	b = bytes.TrimSpace(b)
	strict := func(v any) error {
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if err := dec.Decode(v); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		return nil
	}
	switch {
	case bytes.HasPrefix(b, []byte("[")):
		var rs []*record.Record
		if err := strict(&rs); err != nil {
			return nil, err
		}
		for _, r := range rs {
			r.Upgrade()
		}
		return rs, nil
	case isExport(b):
		d, err := safe.Decode(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		return d.Records, nil
	}
	var r record.Record
	if err := strict(&r); err != nil {
		return nil, err
	}
	r.Upgrade()
	return []*record.Record{&r}, nil
}

func (a *App) listCmd() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "list the records (no secrets)",
		Flags: []cli.Flag{&cli.StringFlag{Name: "type", Usage: "only this record type"}},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return a.search(cmd, "")
		},
	}
}

func (a *App) queryCmd() *cli.Command {
	return &cli.Command{
		Name:      "query",
		Aliases:   []string{"q"},
		Usage:     "search names, descriptions, tags and the fields that aren't secret (url, username, ...)",
		ArgsUsage: "<term>",
		Flags:     []cli.Flag{&cli.StringFlag{Name: "type", Usage: "only this record type"}},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if err := needArgs(cmd, 1); err != nil {
				return err
			}
			return a.search(cmd, cmd.Args().First())
		},
	}
}

func (a *App) search(cmd *cli.Command, term string) error {
	typ := record.CanonicalType(cmd.String("type"))
	if _, ok := record.Schemas[typ]; typ != "" && !ok {
		return fmt.Errorf("unknown record type %q (want one of %s)", typ, strings.Join(record.TypeNames(), ", "))
	}
	s, err := a.open(cmd, false)
	if err != nil {
		return err
	}
	defer s.Close()
	// which safe, on stderr: easy to lose track of with a context set; stdout stays the table
	a.UI.Message("safe: %s", s.Paths.Safe)
	rs := s.Doc.Search(term, typ)
	if len(rs) == 0 {
		a.UI.Message("no records found")
		return nil
	}
	table(a.Out, rs)
	return nil
}

func (a *App) showCmd() *cli.Command {
	return &cli.Command{
		Name:      "show",
		Usage:     "show a record in full, secrets included",
		ArgsUsage: "<id|name>",
		Description: "The record is named by its id, any unique start of it, or its name (case and spacing\n" +
			"don't matter; quote a name with spaces).",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "history", Usage: "also show the previous values of its secret fields"},
			plainFlag(),
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if err := needArgs(cmd, 1); err != nil {
				return err
			}
			s, err := a.open(cmd, false)
			if err != nil {
				return err
			}
			defer s.Close()
			r, err := s.Doc.Lookup(cmd.Args().First())
			if err != nil {
				return err
			}
			history := cmd.Bool("history")
			if a.styled(cmd) {
				lipgloss.Fprintln(a.Out, tui.Card(r, true, history))
			} else {
				show(a.Out, r, history)
			}
			if !history && len(r.History) > 0 {
				a.UI.Message("previous values kept: %s; `record show --history` shows them", historySummary(r))
			}
			return nil
		},
	}
}

func (a *App) editCmd() *cli.Command {
	return &cli.Command{
		Name:        "edit",
		Usage:       "edit a record in a form (with --plain or no terminal: Enter keeps a value, - clears it)",
		ArgsUsage:   "[id]",
		Description: "Without an id, on a terminal, pick the record from a list (--type narrows it).",
		Flags:       []cli.Flag{&cli.StringFlag{Name: "type", Usage: "without an id: list only this record type"}, plainFlag()},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() > 1 || (cmd.NArg() == 1 && cmd.IsSet("type")) {
				return fmt.Errorf("usage: %s %s (--type only goes with no id)", cmd.FullName(), cmd.ArgsUsage)
			}
			form := a.form(cmd)
			typ := record.CanonicalType(cmd.String("type"))
			if cmd.NArg() == 0 {
				if form == nil {
					return fmt.Errorf("usage: %s <id> (picking from a list needs a terminal, without --plain)", cmd.FullName())
				}
				if _, ok := record.Schemas[typ]; typ != "" && !ok {
					return fmt.Errorf("unknown record type %q (want one of %s)", typ, strings.Join(record.TypeNames(), ", "))
				}
			}
			s, err := a.open(cmd, false) // shared, then released while editing (see add)
			if err != nil {
				return err
			}
			defer s.Close()
			var r *record.Record
			if cmd.NArg() == 1 {
				if r, err = s.Doc.Find(cmd.Args().First()); err != nil {
					return err
				}
			} else {
				rs := s.Doc.Search("", typ)
				if len(rs) == 0 {
					if typ != "" {
						return fmt.Errorf("no %s records in %s", typ, s.Paths.Safe)
					}
					return fmt.Errorf("no records in %s", s.Paths.Safe)
				}
				title := "Edit which record?"
				if typ != "" {
					title = "Edit which " + typ + "?"
				}
				if r, err = form.Pick(rs, title); err != nil {
					return formErr(err)
				}
			}
			if r.Fields == nil {
				r.Fields = map[string]string{}
			}
			orig, err := json.Marshal(r)
			if err != nil {
				return err
			}
			s.Release()
			before := maps.Clone(r.Fields)
			nameOK := func(name string) error { return s.Doc.CheckName(name, r.ID) }
			if form != nil {
				if err := form.Edit(r, fmt.Sprintf("Edit %s %s", r.Type, shortID(r.ID)), nameOK); err != nil {
					return formErr(err)
				}
			} else if err := a.edit(r, nameOK); err != nil {
				return err
			}
			r.Normalize()
			kept := r.NoteChanges(before, a.now())
			warnings, err := r.Validate()
			if err != nil {
				return err
			}
			for _, w := range warnings {
				a.UI.Message("warning: %s", w)
			}
			for _, f := range kept {
				a.UI.Message("kept the previous %s as history.%s.1 (%d kept; `record show --history`)", f, f, len(r.History[f]))
			}
			r.UpdatedAt = a.now()
			if err := a.reacquireFor(s, r, orig); err != nil {
				return err
			}
			if err := s.Doc.CheckName(r.Name, r.ID); err != nil { // taken meanwhile, or a duplicate from before
				return fmt.Errorf("%v; nothing was saved", err)
			}
			if err := s.Save(); err != nil {
				return err
			}
			fmt.Fprintf(a.Out, "updated %s %s (%s)\n", r.Type, shortID(r.ID), r.Name)
			return nil
		},
	}
}

func (a *App) delCmd() *cli.Command {
	return &cli.Command{
		Name:      "del",
		Usage:     "delete a record",
		ArgsUsage: "<id>",
		Flags:     []cli.Flag{&cli.BoolFlag{Name: "force", Usage: "don't ask first"}},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if err := needArgs(cmd, 1); err != nil {
				return err
			}
			s, err := a.open(cmd, true)
			if err != nil {
				return err
			}
			defer s.Close()
			r, err := s.Doc.Find(cmd.Args().First())
			if err != nil {
				return err
			}
			if !cmd.Bool("force") {
				ok, err := a.UI.Confirm(fmt.Sprintf("delete %s %s (%s)?", r.Type, shortID(r.ID), r.Name), false)
				if err != nil {
					return err
				}
				if !ok {
					return errAborted
				}
			}
			s.Doc.Delete(r.ID)
			if err := s.Save(); err != nil {
				return err
			}
			fmt.Fprintf(a.Out, "deleted %s %s (%s); the previous version is kept as %s\n",
				r.Type, shortID(r.ID), r.Name, safe.BackupPath(s.Paths.Safe, 1))
			return nil
		},
	}
}

func (a *App) copyCmd() *cli.Command {
	return &cli.Command{
		Name:      "copy",
		Usage:     "copy a field to the clipboard, cleared after --timeout",
		ArgsUsage: "<id> <field>",
		Description: "field is a field name (see `digisafe record types`), name, description, notes,\n" +
			"address.<field>, custom.<key>, answer.<n> for secret question n, or history.<field>.<n>\n" +
			"for a field's nth previous value (1 = the most recent; logins keep 5)",
		Flags: []cli.Flag{
			&cli.DurationFlag{Name: "timeout", Value: 45 * time.Second, Usage: "clear the clipboard after this long (0 = never)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if err := needArgs(cmd, 2); err != nil {
				return err
			}
			s, err := a.open(cmd, false)
			if err != nil {
				return err
			}
			defer s.Close()
			r, err := s.Doc.Find(cmd.Args().Get(0))
			if err != nil {
				return err
			}
			field := cmd.Args().Get(1)
			v, ok := fieldValue(r, field)
			if !ok || v == "" {
				return fmt.Errorf("%s %s has no %s", r.Type, shortID(r.ID), field)
			}
			return a.copy(v, fmt.Sprintf("%s of %s", field, r.Name), cmd.Duration("timeout"))
		},
	}
}

// fieldValue is record.Get plus answer.<n> for secret question answers.
func fieldValue(r *record.Record, field string) (string, bool) {
	if n, ok := strings.CutPrefix(field, "answer."); ok {
		var i int
		if _, err := fmt.Sscanf(n, "%d", &i); err != nil || i < 1 || i > len(r.SecretQuestions) {
			return "", false
		}
		return r.SecretQuestions[i-1].Answer, true
	}
	return r.Get(field)
}

func (a *App) copy(value, what string, timeout time.Duration) error {
	if err := a.Clipboard.Copy(value); err != nil {
		return err
	}
	if timeout <= 0 {
		a.UI.Message("copied the %s to the clipboard", what)
		return nil
	}
	if err := a.Clipboard.ScheduleClear(value, timeout); err != nil {
		a.UI.Message("copied the %s, but could not schedule clearing it (%v): clear the clipboard yourself", what, err)
		return nil
	}
	a.UI.Message("copied the %s to the clipboard; it is cleared in %s", what, timeout)
	return nil
}

func (a *App) otpCmd() *cli.Command {
	return &cli.Command{
		Name:      "otp",
		Usage:     "show a login's current TOTP code",
		ArgsUsage: "<id>",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "copy", Usage: "copy the code to the clipboard instead of printing it"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if err := needArgs(cmd, 1); err != nil {
				return err
			}
			s, err := a.open(cmd, false)
			if err != nil {
				return err
			}
			defer s.Close()
			r, err := s.Doc.Find(cmd.Args().First())
			if err != nil {
				return err
			}
			secret := r.Fields["totp_secret"]
			if secret == "" {
				return fmt.Errorf("%s %s has no totp_secret", r.Type, shortID(r.ID))
			}
			k, err := totp.Parse(secret)
			if err != nil {
				return err
			}
			code, left := k.Code(a.Now())
			if cmd.Bool("copy") {
				return a.copy(code, "code for "+r.Name, left)
			}
			fmt.Fprintln(a.Out, code)
			a.UI.Message("valid for %ds", int(left.Round(time.Second)/time.Second))
			return nil
		},
	}
}

func (a *App) exportCmd() *cli.Command {
	return &cli.Command{
		Name:  "export",
		Usage: "write every record as plaintext JSON",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "out", Usage: "write to this file (mode 0600) instead of stdout"},
			&cli.BoolFlag{Name: "force", Usage: "overwrite --out if it exists"},
			&cli.BoolFlag{Name: "yes", Usage: "don't ask first"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			out := cmd.String("out")
			if out != "" && !cmd.Bool("force") {
				if _, err := os.Stat(out); err == nil {
					return fmt.Errorf("%s exists (use --force to overwrite)", out)
				}
			}
			if !cmd.Bool("yes") {
				ok, err := a.UI.Confirm("this writes every secret unencrypted; continue?", false)
				if err != nil {
					return err
				}
				if !ok {
					return errAborted
				}
			}
			s, err := a.open(cmd, false)
			if err != nil {
				return err
			}
			defer s.Close()
			b, err := s.Doc.Encode()
			if err != nil {
				return err
			}
			b = append(b, '\n')
			if out == "" {
				_, err := a.Out.Write(b)
				return err
			}
			if err := safe.WriteAtomic(out, b, false); err != nil {
				return err
			}
			a.UI.Message("wrote %d record(s) to %s; it is unencrypted, delete it when done", len(s.Doc.Records), out)
			return nil
		},
	}
}

func (a *App) importCmd() *cli.Command {
	return &cli.Command{
		Name:      "import",
		Usage:     "add records from a JSON file (- for stdin): one record, a list, or an export",
		ArgsUsage: "<file>",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "replace", Usage: "replace records whose id is already in the safe (default: skip them)"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if err := needArgs(cmd, 1); err != nil {
				return err
			}
			file := cmd.Args().First()
			recs, err := readRecords(a.In, file)
			if err != nil {
				return err
			}
			s, err := a.open(cmd, true)
			if err != nil {
				return err
			}
			defer s.Close()
			added, replaced, skipped := 0, 0, 0
			now := a.now()
			seen := map[string]int{} // id -> record number in the file
			for i, in := range recs {
				fresh, err := record.New(in.Type, now)
				if err != nil {
					return fmt.Errorf("record %d: %w", i+1, err)
				}
				if in.ID == "" {
					in.ID = fresh.ID
				}
				if in.CreatedAt.IsZero() {
					in.CreatedAt = now
				}
				if in.UpdatedAt.IsZero() {
					in.UpdatedAt = in.CreatedAt
				}
				if in.Fields == nil {
					in.Fields = map[string]string{}
				}
				in.Normalize() // the id too: lower case, hyphens, no braces or urn:uuid:
				warnings, err := in.Validate()
				if err != nil {
					return fmt.Errorf("record %d (%s): %w", i+1, in.Name, err)
				}
				if j, dup := seen[in.ID]; dup {
					return fmt.Errorf("records %d and %d in %s have the same id %s; nothing was imported", j, i+1, file, in.ID)
				}
				seen[in.ID] = i + 1
				for _, w := range warnings {
					a.UI.Message("warning: %s: %s", in.Name, w)
				}
				if old := s.Doc.Get(in.ID); old != nil {
					if !cmd.Bool("replace") {
						skipped++
						continue
					}
					// keep the replaced version's secrets in the history, as an edit would
					if in.History == nil {
						in.History = old.History
					}
					in.NoteChanges(old.Fields, now)
					s.Doc.Delete(in.ID)
					replaced++
				} else {
					added++
				}
				if err := s.Doc.Add(in); err != nil {
					return fmt.Errorf("record %d (%s): %w; nothing was imported", i+1, record.Printable(in.Name), err)
				}
			}
			if added+replaced > 0 {
				if err := s.Save(); err != nil {
					return err
				}
			}
			fmt.Fprintf(a.Out, "imported %d record(s): %d added, %d replaced, %d skipped (id already in the safe)\n",
				len(recs), added, replaced, skipped)
			if file != "-" {
				a.UI.Message("note: %s still holds the records unencrypted; delete it", file)
			}
			return nil
		},
	}
}
