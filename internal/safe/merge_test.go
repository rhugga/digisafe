package safe

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rhugga/digisafe/internal/prompt"
	"github.com/rhugga/digisafe/internal/record"
)

func login(t *testing.T, id, name, password string, updated time.Time) *record.Record {
	t.Helper()
	r, err := record.New("login", updated)
	if err != nil {
		t.Fatal(err)
	}
	r.ID, r.Name, r.Fields["password"] = id, name, password
	r.CreatedAt, r.UpdatedAt = updated, updated
	return r
}

func TestMerge(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	const (
		idA = "0b6f4f7e-6d1c-4a4e-9a51-6c2f4b1e8a10"
		idB = "1b6f4f7e-6d1c-4a4e-9a51-6c2f4b1e8a10"
		idC = "2b6f4f7e-6d1c-4a4e-9a51-6c2f4b1e8a10"
		idD = "3b6f4f7e-6d1c-4a4e-9a51-6c2f4b1e8a10"
		idE = "4b6f4f7e-6d1c-4a4e-9a51-6c2f4b1e8a10"
	)
	ours := NewDocument()
	for _, r := range []*record.Record{
		login(t, idA, "A", "a-old", t0),                  // theirs is newer
		login(t, idB, "B", "b-new", t0.Add(2*time.Hour)), // ours is newer
		login(t, idC, "C", "c-ours", t0),                 // same time, different: conflict
		login(t, idD, "D", "same", t0),                   // identical
	} {
		ours.Add(r)
	}
	theirs := []*record.Record{
		login(t, idA, "A", "a-new", t0.Add(time.Hour)),
		login(t, idB, "B", "b-old", t0),
		login(t, idC, "C", "c-theirs", t0),
		login(t, idD, "D", "same", t0),
		login(t, idE, "E", "e", t0), // only theirs
	}
	res, err := ours.Merge(theirs)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 1 || len(res.Updated) != 1 || len(res.Kept) != 1 || len(res.Conflicts) != 1 || res.Same != 1 || !res.Changed {
		t.Fatalf("result: %d added, %d updated, %d kept, %d conflicts, %d same, changed %v",
			len(res.Added), len(res.Updated), len(res.Kept), len(res.Conflicts), res.Same, res.Changed)
	}
	a, b, c := ours.Get(idA), ours.Get(idB), ours.Get(idC)
	if a.Fields["password"] != "a-new" || !a.UpdatedAt.Equal(t0.Add(time.Hour)) || a.History["password"][0].Value != "a-old" {
		t.Errorf("A (theirs newer): %+v", a)
	}
	if b.Fields["password"] != "b-new" || b.History["password"][0].Value != "b-old" {
		t.Errorf("B (ours newer): %+v", b)
	}
	if c.Fields["password"] != "c-ours" || c.History["password"][0].Value != "c-theirs" {
		t.Errorf("C (conflict): %+v", c)
	}
	if ours.Get(idE) == nil {
		t.Error("E wasn't added")
	}

	// merging the same records again changes nothing
	again := []*record.Record{
		login(t, idA, "A", "a-new", t0.Add(time.Hour)),
		login(t, idB, "B", "b-old", t0),
		login(t, idC, "C", "c-theirs", t0),
		login(t, idE, "E", "e", t0),
	}
	if res, err := ours.Merge(again); err != nil || res.Changed {
		t.Errorf("second merge: changed=%v, %v", res.Changed, err)
	}

	// non-login types keep no history, and a bad incoming record is an error
	n, _ := record.New("note", t0)
	n.Name = ""
	if _, err := NewDocument().Merge([]*record.Record{n}); err == nil {
		t.Error("merging a record with no name succeeded")
	}
}

// The combined history is newest first and capped.
func TestMergeHistoryLimit(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	const id = "0b6f4f7e-6d1c-4a4e-9a51-6c2f4b1e8a10"
	ours := login(t, id, "A", "p9", t0.Add(10*time.Hour))
	theirs := login(t, id, "A", "p8", t0)
	ours.History, theirs.History = map[string][]record.PastValue{}, map[string][]record.PastValue{}
	// p0..p7 split between the two histories, oldest first
	for i := 0; i < 8; i++ {
		h := record.PastValue{Value: fmt.Sprintf("p%d", i), ReplacedAt: t0.Add(time.Duration(i) * time.Minute)}
		side := ours
		if i%2 == 0 {
			side = theirs
		}
		side.History["password"] = append([]record.PastValue{h}, side.History["password"]...)
	}
	d := NewDocument()
	d.Add(ours)
	if _, err := d.Merge([]*record.Record{theirs}); err != nil {
		t.Fatal(err)
	}
	// p8 (theirs' current password, replaced when ours was updated), then p7, p6, p5, p4
	var got []string
	for _, h := range d.Get(id).History["password"] {
		got = append(got, h.Value)
	}
	if want := "p8 p7 p6 p5 p4"; strings.Join(got, " ") != want {
		t.Errorf("history = %v, want %s", got, want)
	}
}

// A sibling safe is read with this safe's keys: no second passphrase.
func TestReadSibling(t *testing.T) {
	p := newSafe(t)
	work := NewPaths(p.Dir, "work")
	if _, err := Create(work, prompt.NewScript(), nil); err != nil {
		t.Fatal(err)
	}
	ws := openSafe(t, work, prompt.NewScript(pass), true)
	addNote(t, ws, "in work", "x")
	ws.Close()
	ui := prompt.NewScript(pass)
	s := openSafe(t, p, ui, false)
	d, err := s.ReadSibling(work.Safe)
	if err != nil || len(d.Records) != 1 || d.Records[0].Name != "in work" {
		t.Fatalf("ReadSibling = %v, %v", d, err)
	}
	if len(ui.Prompts) != 1 {
		t.Errorf("prompts = %q, want one", ui.Prompts)
	}
	if _, err := s.ReadSibling(filepath.Join(p.Dir, "missing.age")); err == nil {
		t.Error("reading a missing sibling succeeded")
	}
}
