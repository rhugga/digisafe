package app

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/rhugga/digisafe/internal/record"
)

// shortID is the id prefix list shows; any unique prefix works as an id.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// table prints records without any secret: id, type, name, description, last update.
func table(w io.Writer, rs []*record.Record) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTYPE\tNAME\tDESCRIPTION\tUPDATED")
	for _, r := range rs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", shortID(r.ID), r.Type, oneLine(r.Name, 40),
			oneLine(r.Description, 50), r.UpdatedAt.Local().Format("2006-01-02"))
	}
	tw.Flush()
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(record.Printable(s)), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

// show prints a record in full, secrets included; with history, the previous values of its
// secret fields too.
func show(w io.Writer, r *record.Record, history bool) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	var long [][2]string // multi-line values, printed after the table
	row := func(k, v string) {
		k, v = record.Printable(k), record.Printable(v)
		switch {
		case v == "":
		case strings.Contains(v, "\n"):
			long = append(long, [2]string{k, v})
		default:
			fmt.Fprintf(tw, "%s:\t%s\n", k, v)
		}
	}
	row("id", r.ID)
	row("type", r.Type)
	row("name", r.Name)
	row("description", r.Description)
	row("tags", strings.Join(r.Tags, ", "))
	for _, f := range r.Schema().Fields {
		row(f.Name, r.Fields[f.Name])
	}
	if r.Address != nil {
		for _, f := range record.AddressFields {
			row("address."+f.Name, f.Get(r.Address))
		}
	}
	for i, q := range r.SecretQuestions {
		row(fmt.Sprintf("question %d", i+1), q.Question)
		row(fmt.Sprintf("answer %d", i+1), q.Answer)
	}
	keys := make([]string, 0, len(r.Custom))
	for k := range r.Custom {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		row("custom."+k, r.Custom[k])
	}
	if history {
		for _, f := range r.Schema().Fields {
			for i, p := range r.History[f.Name] {
				row(fmt.Sprintf("history.%s.%d", f.Name, i+1), p.Value+"  (replaced "+p.ReplacedAt.Local().Format("2006-01-02 15:04")+")")
			}
		}
	}
	row("created", r.CreatedAt.Local().Format("2006-01-02 15:04:05"))
	row("updated", r.UpdatedAt.Local().Format("2006-01-02 15:04:05"))
	tw.Flush()
	for _, kv := range long {
		fmt.Fprintf(w, "%s:\n%s\n", kv[0], indent(kv[1]))
	}
	if r.Notes != "" {
		fmt.Fprintf(w, "notes:\n%s\n", indent(record.Printable(r.Notes)))
	}
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}
