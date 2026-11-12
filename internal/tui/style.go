package tui

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/rhugga/digisafe/internal/record"
)

// The palette: 256-colour values that read on dark and light backgrounds. Print styled text
// with lipgloss.Fprint*, which drops the colours for terminals that can't show them and when
// NO_COLOR is set.
var (
	accent      = lipgloss.Color("63")
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(accent)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	labelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	secretStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	cardStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(0, 1)
)

// masked stands in for a hidden secret; it's the same length whatever the secret's length.
const masked = "••••••••"

type row struct {
	label, value string
	secret       bool
	note         string // dim text after the value
}

// rows lists r's non-empty values in the order `show` prints them; with history, the previous
// values of its secret fields after them.
func rows(r *record.Record, history bool) []row {
	var out []row
	add := func(label, value string, secret bool) {
		if value != "" {
			out = append(out, row{label: record.Printable(label), value: record.Printable(value), secret: secret})
		}
	}
	add("description", r.Description, false)
	add("tags", strings.Join(r.Tags, ", "), false)
	for _, f := range r.Schema().Fields {
		add(f.Label, r.Fields[f.Name], f.Secret)
	}
	if r.Address != nil {
		for _, f := range record.AddressFields {
			add("address: "+f.Label, f.Get(r.Address), false)
		}
	}
	for i, q := range r.SecretQuestions {
		add(fmt.Sprintf("question %d", i+1), q.Question, false)
		add(fmt.Sprintf("answer %d", i+1), q.Answer, true)
	}
	for _, k := range slices.Sorted(maps.Keys(r.Custom)) {
		add("custom: "+k, r.Custom[k], true)
	}
	add("notes", r.Notes, false)
	if history {
		for _, f := range r.Schema().Fields {
			for i, p := range r.History[f.Name] {
				if p.Value == "" {
					continue
				}
				add(fmt.Sprintf("%s, previous %d", f.Label, i+1), p.Value, true)
				out[len(out)-1].note = "replaced " + p.ReplacedAt.Local().Format("2006-01-02 15:04")
			}
		}
	}
	return out
}

// Card renders r in a box: its name and type as the title, then each field. Secrets are shown
// in their own colour, or masked unless reveal. With history, the secrets' previous values too.
func Card(r *record.Record, reveal, history bool) string {
	rs := rows(r, history)
	width := 0
	for _, x := range rs {
		width = max(width, lipgloss.Width(x.label))
	}
	lines := []string{titleStyle.Render(cmp.Or(record.Printable(r.Name), "(no name)")) + "  " + dimStyle.Render(r.Type)}
	if r.ID != "" {
		lines = append(lines, dimStyle.Render(r.ID))
	}
	lines = append(lines, "")
	for _, x := range rs {
		v := x.value
		style := lipgloss.NewStyle()
		if x.secret {
			style = secretStyle
			if !reveal {
				v = masked
			}
		}
		label := labelStyle.Render(fmt.Sprintf("%-*s", width, x.label))
		if strings.Contains(v, "\n") {
			lines = append(lines, label)
			for _, l := range strings.Split(strings.TrimRight(v, "\n"), "\n") {
				lines = append(lines, "  "+style.Render(l))
			}
			continue
		}
		line := label + "  " + style.Render(v)
		if x.note != "" {
			line += "  " + dimStyle.Render(x.note)
		}
		lines = append(lines, line)
	}
	if !r.CreatedAt.IsZero() {
		lines = append(lines, "", dimStyle.Render(fmt.Sprintf("created %s · updated %s",
			r.CreatedAt.Local().Format("2006-01-02 15:04"), r.UpdatedAt.Local().Format("2006-01-02 15:04"))))
	}
	return cardStyle.Render(strings.Join(lines, "\n"))
}
