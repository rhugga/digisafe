// Package safe holds the safe: the records document, its encryption and keys, and how it is
// stored on disk.
package safe

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/rhugga/digisafe/internal/record"
)

// FormatVersion is the version of the document format this build writes. 2 added records'
// history; this build reads 1 and 2, and writes 2.
const FormatVersion = 2

// Document is the plaintext inside the encrypted safe.
type Document struct {
	Version int              `json:"version"`
	Records []*record.Record `json:"records"`
}

// ErrNotFound is returned when no record matches an id.
var ErrNotFound = errors.New("no record with that id")

// NewDocument returns an empty document.
func NewDocument() *Document { return &Document{Version: FormatVersion, Records: []*record.Record{}} }

// Decode parses a document. Unknown fields are errors, so a typo or a newer format is not
// silently dropped on the next save.
func Decode(b []byte) (*Document, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var d Document
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("reading the safe: %w", err)
	}
	switch {
	case d.Version == 0:
		return nil, errors.New("reading the safe: no format version")
	case d.Version > FormatVersion:
		return nil, fmt.Errorf("the safe is format version %d; this digisafe reads up to %d, upgrade it", d.Version, FormatVersion)
	}
	if d.Records == nil {
		d.Records = []*record.Record{}
	}
	for _, r := range d.Records {
		r.Upgrade() // renamed types and fields (credit_card, login's login); saved under the new names
	}
	d.Version = FormatVersion // saved in this build's format: an older safe is upgraded on save
	return &d, nil
}

// Encode serializes the document, records sorted by creation time.
func (d *Document) Encode() ([]byte, error) {
	d.sort()
	return json.MarshalIndent(d, "", "  ")
}

func (d *Document) sort() {
	sort.SliceStable(d.Records, func(i, j int) bool {
		a, b := d.Records[i], d.Records[j]
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.ID < b.ID
	})
}

// Find returns the record whose id is id or starts with it (case-insensitive). A prefix that
// matches more than one record is an error naming them.
func (d *Document) Find(id string) (*record.Record, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return nil, ErrNotFound
	}
	var matches []*record.Record
	for _, r := range d.Records {
		if r.ID == id {
			return r, nil
		}
		if strings.HasPrefix(r.ID, id) {
			matches = append(matches, r)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("%w %q", ErrNotFound, id)
	case 1:
		return matches[0], nil
	}
	var names []string
	for _, m := range matches {
		names = append(names, fmt.Sprintf("%s (%s)", m.ID, record.Printable(m.Name)))
	}
	return nil, fmt.Errorf("id %q matches %d records, give more of it: %s", id, len(matches), strings.Join(names, ", "))
}

// Named returns the records named name (by record.NameKey). There's at most one, unless the
// safe was written before names had to be unique.
func (d *Document) Named(name string) []*record.Record {
	key := record.NameKey(name)
	var out []*record.Record
	for _, r := range d.Records {
		if key != "" && record.NameKey(r.Name) == key {
			out = append(out, r)
		}
	}
	return out
}

// NameTaken returns another record (not id) already called name, or nil.
func (d *Document) NameTaken(name, id string) *record.Record {
	for _, r := range d.Named(name) {
		if r.ID != id {
			return r
		}
	}
	return nil
}

// ErrNameTaken is returned when a record would take a name another record has.
var ErrNameTaken = errors.New("names must be unique")

// CheckName reports, as an error for the user, whether record id can be called name.
func (d *Document) CheckName(name, id string) error {
	if o := d.NameTaken(name, id); o != nil {
		return fmt.Errorf("%w: %s %s is already called %q", ErrNameTaken, o.Type, o.ID[:min(len(o.ID), 8)], record.Printable(o.Name))
	}
	return nil
}

// Lookup finds a record by its id, a unique start of its id, or its name (case and spacing
// don't matter). A full id wins; a name and an id prefix that point at different records is an
// error asking for more of the id.
func (d *Document) Lookup(ref string) (*record.Record, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, ErrNotFound
	}
	if r := d.Get(strings.ToLower(ref)); r != nil {
		return r, nil
	}
	named := d.Named(ref)
	if len(named) > 1 {
		var ids []string
		for _, r := range named {
			ids = append(ids, r.ID)
		}
		return nil, fmt.Errorf("%d records are called %q (from before names had to be unique): use the id, one of %s",
			len(named), record.Printable(ref), strings.Join(ids, ", "))
	}
	byID, idErr := d.Find(ref)
	switch {
	case len(named) == 1 && (idErr != nil || byID == named[0]):
		return named[0], nil
	case len(named) == 1:
		return nil, fmt.Errorf("%q is the name of %s and the start of %s's id: give more of the id",
			record.Printable(ref), named[0].ID[:8], byID.ID)
	case errors.Is(idErr, ErrNotFound):
		return nil, fmt.Errorf("no record with the id or name %q", record.Printable(ref))
	}
	return byID, idErr
}

// Get returns the record with exactly this id, or nil.
func (d *Document) Get(id string) *record.Record {
	for _, r := range d.Records {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// Add appends a record. Its id must be new, and its name not another record's.
func (d *Document) Add(r *record.Record) error {
	if d.Get(r.ID) != nil {
		return fmt.Errorf("a record with id %s already exists", r.ID)
	}
	if err := d.CheckName(r.Name, r.ID); err != nil {
		return err
	}
	d.Records = append(d.Records, r)
	return nil
}

// Delete removes the record with exactly this id.
func (d *Document) Delete(id string) bool {
	for i, r := range d.Records {
		if r.ID == id {
			d.Records = append(d.Records[:i], d.Records[i+1:]...)
			return true
		}
	}
	return false
}

// Search returns the records matching term (see record.Matches), optionally of one type.
func (d *Document) Search(term, typ string) []*record.Record {
	typ = record.CanonicalType(typ)
	d.sort()
	var out []*record.Record
	for _, r := range d.Records {
		if (typ == "" || r.Type == typ) && r.Matches(term) {
			out = append(out, r)
		}
	}
	return out
}
