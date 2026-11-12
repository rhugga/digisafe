package safe

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rhugga/digisafe/internal/record"
)

func named(t *testing.T, id, name string) *record.Record {
	t.Helper()
	r, err := record.New("note", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	r.ID, r.Name = id, name
	return r
}

func TestNamesAreUnique(t *testing.T) {
	d := NewDocument()
	if err := d.Add(named(t, "0b6f4f7e-6d1c-4a4e-9a51-6c2f4b1e8a10", "My  GitHub")); err != nil {
		t.Fatal(err)
	}
	for _, clash := range []string{"my github", " MY GITHUB ", "My GitHub"} {
		err := d.Add(named(t, "1b6f4f7e-6d1c-4a4e-9a51-6c2f4b1e8a10", clash))
		if !errors.Is(err, ErrNameTaken) || !strings.Contains(err.Error(), `already called "My  GitHub"`) {
			t.Errorf("Add(%q) = %v", clash, err)
		}
	}
	// a record keeps its own name; another may not take it
	if err := d.CheckName("my github", "0b6f4f7e-6d1c-4a4e-9a51-6c2f4b1e8a10"); err != nil {
		t.Errorf("own name: %v", err)
	}
	if err := d.Add(named(t, "1b6f4f7e-6d1c-4a4e-9a51-6c2f4b1e8a10", "My GitHub 2")); err != nil {
		t.Error(err)
	}
}

func TestLookup(t *testing.T) {
	d := NewDocument()
	gh := named(t, "0b6f4f7e-6d1c-4a4e-9a51-6c2f4b1e8a10", "GitHub")
	cafe := named(t, "cafe0000-6d1c-4a4e-9a51-6c2f4b1e8a10", "Coffee")
	bead := named(t, "beef0000-6d1c-4a4e-9a51-6c2f4b1e8a10", "cafe") // named like cafe0000's id
	for _, r := range []*record.Record{gh, cafe, bead} {
		if err := d.Add(r); err != nil {
			t.Fatal(err)
		}
	}
	for ref, want := range map[string]*record.Record{
		gh.ID:      gh,   // the full id
		"0B6F":     gh,   // a prefix, any case
		"github":   gh,   // the name, any case
		" GitHub ": gh,   // and spacing
		"coffee":   cafe, //
		"beef":     bead, // a prefix that's no one's name
	} {
		if got, err := d.Lookup(ref); err != nil || got != want {
			t.Errorf("Lookup(%q) = %v, %v; want %s", ref, got, err, want.Name)
		}
	}
	// "cafe" is bead's name and the start of cafe's id
	if _, err := d.Lookup("cafe"); err == nil || !strings.Contains(err.Error(), "give more of the id") {
		t.Errorf("ambiguous: %v", err)
	}
	if got, _ := d.Lookup(cafe.ID[:9]); got != cafe {
		t.Errorf("more of the id: %v", got)
	}
	if _, err := d.Lookup("nothing"); err == nil || !strings.Contains(err.Error(), `no record with the id or name "nothing"`) {
		t.Errorf("not found: %v", err)
	}
	if _, err := d.Lookup(" "); !errors.Is(err, ErrNotFound) {
		t.Errorf("empty: %v", err)
	}
	// a safe from before names were unique: the name lists the ids
	d.Records = append(d.Records, named(t, "dd000000-6d1c-4a4e-9a51-6c2f4b1e8a10", "github"))
	if _, err := d.Lookup("GitHub"); err == nil || !strings.Contains(err.Error(), "2 records are called") ||
		!strings.Contains(err.Error(), gh.ID) {
		t.Errorf("duplicates: %v", err)
	}
}

// Merging doesn't bring in a second record with a name ours already has.
func TestMergeNameClash(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	ours := NewDocument()
	ours.Add(login(t, "0b6f4f7e-6d1c-4a4e-9a51-6c2f4b1e8a10", "GitHub", "ours", t0))
	ours.Add(login(t, "1b6f4f7e-6d1c-4a4e-9a51-6c2f4b1e8a10", "Bank", "b", t0))
	res, err := ours.Merge([]*record.Record{
		login(t, "2b6f4f7e-6d1c-4a4e-9a51-6c2f4b1e8a10", "github", "theirs", t0),            // same name, another id
		login(t, "1b6f4f7e-6d1c-4a4e-9a51-6c2f4b1e8a10", "GitHub", "b2", t0.Add(time.Hour)), // newer Bank, renamed onto GitHub
		login(t, "3b6f4f7e-6d1c-4a4e-9a51-6c2f4b1e8a10", "New", "n", t0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.NameClashes) != 2 || len(res.Added) != 1 || len(res.Updated) != 0 {
		t.Errorf("%d clashes, %d added, %d updated", len(res.NameClashes), len(res.Added), len(res.Updated))
	}
	if b := ours.Get("1b6f4f7e-6d1c-4a4e-9a51-6c2f4b1e8a10"); b.Name != "Bank" {
		t.Errorf("Bank was renamed onto a taken name: %+v", b)
	}
}
