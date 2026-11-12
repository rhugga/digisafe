// Package record defines the records a safe holds: their types, their fields, validation and
// search.
package record

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Record is one entry in the safe. The common fields are on every record; the type-specific
// scalar fields are in Fields, keyed by the names in the type's Schema.
type Record struct {
	ID              string            `json:"id"`
	Type            string            `json:"type"`
	Name            string            `json:"name"`
	Description     string            `json:"description,omitempty"`
	Tags            []string          `json:"tags,omitempty"`
	Notes           string            `json:"notes,omitempty"`
	Fields          map[string]string `json:"fields,omitempty"`
	Address         *Address          `json:"address,omitempty"`
	SecretQuestions []SecretQuestion  `json:"secret_questions,omitempty"`
	Custom          map[string]string `json:"custom,omitempty"`
	// History holds the previous values of secret fields, newest first (types with
	// Schema.History: logins). See NoteChanges.
	History   map[string][]PastValue `json:"history,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// Address is a postal address (bank accounts).
type Address struct {
	Street1 string `json:"street_1,omitempty"`
	Street2 string `json:"street_2,omitempty"`
	City    string `json:"city,omitempty"`
	State   string `json:"state,omitempty"`
	ZipCode string `json:"zip_code,omitempty"`
}

// IsZero reports whether every address field is empty.
func (a *Address) IsZero() bool {
	return a == nil || *a == Address{}
}

// AddressFields lists the address fields in display order, with getters and setters.
var AddressFields = []struct {
	Name, Label string
	Get         func(*Address) string
	Set         func(*Address, string)
}{
	{"street_1", "street 1", func(a *Address) string { return a.Street1 }, func(a *Address, v string) { a.Street1 = v }},
	{"street_2", "street 2", func(a *Address) string { return a.Street2 }, func(a *Address, v string) { a.Street2 = v }},
	{"city", "city", func(a *Address) string { return a.City }, func(a *Address, v string) { a.City = v }},
	{"state", "state", func(a *Address) string { return a.State }, func(a *Address, v string) { a.State = v }},
	{"zip_code", "zip code", func(a *Address) string { return a.ZipCode }, func(a *Address, v string) { a.ZipCode = v }},
}

// Upgrade renames an old record's type and fields to their current names (see renamedTypes and
// renamedFields). A renamed field's value moves unless the new field already has one.
func (r *Record) Upgrade() {
	r.Type = CanonicalType(r.Type)
	for old, cur := range renamedFields[r.Type] {
		v, ok := r.Fields[old]
		if !ok {
			continue
		}
		if _, taken := r.Fields[cur]; !taken {
			r.Fields[cur] = v
			delete(r.Fields, old)
		}
	}
}

// unsafeRune reports whether printing r to a terminal could do more than show a character:
// control characters (escape sequences start with one) and the Unicode bidirectional overrides,
// which can make text read differently from what it is. Newline and tab are fine.
func unsafeRune(r rune) bool {
	switch {
	case r == '\n' || r == '\t':
		return false
	case r < 0x20, r == 0x7f, r >= 0x80 && r < 0xa0:
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

// Printable makes s safe to print to a terminal: the characters unsafeRune finds are shown
// escaped (\x1b, \u202e) instead. Records can come from imported files, so their text is
// printed through this, never as is.
func Printable(s string) string {
	if !strings.ContainsFunc(s, unsafeRune) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case !unsafeRune(r):
			b.WriteRune(r)
		case r < 0x100:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
	}
	return b.String()
}

// oneLine cleans a single-line field (name, description, a tag): control characters, newlines
// and bidirectional overrides become spaces, and the ends are trimmed.
func oneLine(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unsafeRune(r) || r == '\n' || r == '\t' {
			return ' '
		}
		return r
	}, s))
}

// HistoryLimit is how many previous values a record keeps per secret field.
const HistoryLimit = 5

// PastValue is a secret field's earlier value and when it was replaced.
type PastValue struct {
	Value      string    `json:"value"`
	ReplacedAt time.Time `json:"replaced_at"`
}

// NoteChanges keeps the previous value of each secret field that changed since before (the
// record's fields before an edit): newest first, up to HistoryLimit per field. A cleared field
// keeps its old value too; a field that was empty has nothing to keep. It returns the fields
// whose value was kept. Only types with Schema.History keep a history.
func (r *Record) NoteChanges(before map[string]string, now time.Time) []string {
	s := r.Schema()
	if !s.History {
		return nil
	}
	var kept []string
	for _, f := range s.Fields {
		old := before[f.Name]
		if !f.Secret || old == "" || old == r.Fields[f.Name] {
			continue
		}
		if r.History == nil {
			r.History = map[string][]PastValue{}
		}
		h := append([]PastValue{{Value: old, ReplacedAt: now.UTC().Truncate(time.Second)}}, r.History[f.Name]...)
		r.History[f.Name] = h[:min(len(h), HistoryLimit)]
		kept = append(kept, f.Name)
	}
	return kept
}

// SecretQuestion is a security question and its answer; the answer is secret.
type SecretQuestion struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// New returns an empty record of the given type with a fresh ID and timestamps. Old type names
// (credit_card) are accepted.
func New(typ string, now time.Time) (*Record, error) {
	typ = CanonicalType(typ)
	if _, ok := Schemas[typ]; !ok {
		return nil, fmt.Errorf("unknown record type %q (want one of %s)", typ, strings.Join(TypeNames(), ", "))
	}
	// v4 (random), not v7: v7 starts with a timestamp, so records created close together share
	// a long prefix and the short ids the CLI shows would collide.
	id, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("generating an id: %w", err)
	}
	now = now.UTC().Truncate(time.Second)
	return &Record{ID: id.String(), Type: typ, Fields: map[string]string{}, CreatedAt: now, UpdatedAt: now}, nil
}

// Schema returns the record's type schema. The record must have been validated.
func (r *Record) Schema() Schema { return Schemas[r.Type] }

// Normalize drops empty values so a record has one representation: empty fields, custom
// fields, tags, questions and a blank address are removed; tags are trimmed and de-duplicated.
func (r *Record) Normalize() {
	r.Upgrade()
	r.Name, r.Description = oneLine(r.Name), oneLine(r.Description)
	for i, t := range r.Tags {
		r.Tags[i] = oneLine(t)
	}
	if id, err := uuid.Parse(r.ID); err == nil {
		r.ID = id.String() // the canonical form: Parse also takes upper case, {...} and urn:uuid:
	}
	for k, v := range r.Fields {
		if strings.TrimSpace(v) == "" {
			delete(r.Fields, k)
		}
	}
	for k, v := range r.Custom {
		if strings.TrimSpace(k) == "" || v == "" {
			delete(r.Custom, k)
		}
	}
	if len(r.Custom) == 0 {
		r.Custom = nil
	}
	seen := map[string]bool{}
	var tags []string
	for _, t := range r.Tags {
		t = strings.TrimSpace(t)
		if t != "" && !seen[t] {
			seen[t] = true
			tags = append(tags, t)
		}
	}
	r.Tags = tags
	var qs []SecretQuestion
	for _, q := range r.SecretQuestions {
		if strings.TrimSpace(q.Question) != "" || q.Answer != "" {
			qs = append(qs, q)
		}
	}
	r.SecretQuestions = qs
	if r.Address.IsZero() {
		r.Address = nil
	}
	for k, h := range r.History {
		var vs []PastValue
		for _, p := range h {
			if p.Value != "" {
				vs = append(vs, p)
			}
		}
		if len(vs) == 0 {
			delete(r.History, k)
			continue
		}
		r.History[k] = vs[:min(len(vs), HistoryLimit)]
	}
	if len(r.History) == 0 {
		r.History = nil
	}
	for _, f := range Schemas[r.Type].Fields {
		for _, sug := range f.Suggest {
			if v := strings.TrimSpace(r.Fields[f.Name]); strings.EqualFold(v, sug) {
				r.Fields[f.Name] = sug
			}
		}
	}
}

// Validate checks the record against its schema. Errors make the record unusable (unknown type
// or field, missing name, bad TOTP secret); warnings are suspicious values worth a second look
// (a card number failing the Luhn check, a malformed expiry date or CVV).
func (r *Record) Validate() (warnings []string, err error) {
	s, ok := Schemas[r.Type]
	if !ok {
		return nil, fmt.Errorf("unknown record type %q (want one of %s)", r.Type, strings.Join(TypeNames(), ", "))
	}
	if _, err := uuid.Parse(r.ID); err != nil {
		return nil, fmt.Errorf("record id %q is not a UUID", r.ID)
	}
	if strings.TrimSpace(r.Name) == "" {
		return nil, fmt.Errorf("a record needs a name")
	}
	var unknown []string
	for k := range r.Fields {
		if _, ok := s.Field(k); !ok {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("%s records have no field %s (fields: %s)", r.Type,
			strings.Join(unknown, ", "), strings.Join(s.FieldNames(), ", "))
	}
	if !r.Address.IsZero() && !s.Address {
		return nil, fmt.Errorf("%s records have no address", r.Type)
	}
	if len(r.SecretQuestions) > 0 && !s.SecretQuestions {
		return nil, fmt.Errorf("%s records have no secret questions", r.Type)
	}
	for k := range r.History {
		if f, ok := s.Field(k); !s.History || !ok || !f.Secret {
			return nil, fmt.Errorf("%s records keep no history for %s", r.Type, k)
		}
	}
	for _, f := range s.Fields {
		v, ok := r.Fields[f.Name]
		if !ok || v == "" || f.Check == nil {
			continue
		}
		if msg := f.Check(v); msg != "" {
			if f.Strict {
				return nil, fmt.Errorf("%s: %s", f.Name, msg)
			}
			warnings = append(warnings, fmt.Sprintf("%s: %s", f.Name, msg))
		}
	}
	return warnings, nil
}

// Matches reports whether term (case-insensitive) is in the record's name, description, tags,
// or a field that isn't secret: url, username, bank_name, the address, and so on. Secrets are
// never searched, so a search can't be used to probe them. An empty term matches everything.
func (r *Record) Matches(term string) bool {
	term = strings.ToLower(strings.TrimSpace(term))
	if term == "" {
		return true
	}
	has := func(s string) bool { return strings.Contains(strings.ToLower(s), term) }
	if has(r.Name) || has(r.Description) {
		return true
	}
	for _, t := range r.Tags {
		if has(t) {
			return true
		}
	}
	for _, f := range r.Schema().Fields {
		if !f.Secret && has(r.Fields[f.Name]) {
			return true
		}
	}
	if r.Address != nil {
		for _, f := range AddressFields {
			if has(f.Get(r.Address)) {
				return true
			}
		}
	}
	return false
}

// Get returns a field's value by name: a schema field, a common field (name, description,
// notes), an address field ("address.city") or a custom field ("custom.<key>").
func (r *Record) Get(name string) (string, bool) {
	switch name {
	case "name":
		return r.Name, true
	case "description":
		return r.Description, true
	case "notes":
		return r.Notes, r.Notes != ""
	}
	if k, ok := strings.CutPrefix(name, "history."); ok {
		// history.<field>.<n>: the field's nth previous value, 1 the most recent
		f, n, _ := strings.Cut(k, ".")
		var i int
		if _, err := fmt.Sscanf(n, "%d", &i); err != nil || i < 1 || i > len(r.History[f]) {
			return "", false
		}
		return r.History[f][i-1].Value, true
	}
	if k, ok := strings.CutPrefix(name, "custom."); ok {
		v, ok := r.Custom[k]
		return v, ok
	}
	if k, ok := strings.CutPrefix(name, "address."); ok && r.Address != nil {
		for _, f := range AddressFields {
			if f.Name == k {
				v := f.Get(r.Address)
				return v, v != ""
			}
		}
	}
	v, ok := r.Fields[CanonicalField(r.Type, name)]
	return v, ok
}
