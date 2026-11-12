package safe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"

	"github.com/rhugga/digisafe/internal/record"
)

// MergeResult says what Merge did with each incoming record.
type MergeResult struct {
	Added     []*record.Record // only in the incoming safe
	Updated   []*record.Record // in both; the incoming version was newer and replaced ours
	Kept      []*record.Record // in both; ours was newer (its history may have gained secrets)
	Conflicts []*record.Record // in both, updated at the same time but different: ours kept
	// NameClashes are incoming records not merged because another record here (a different id)
	// has their name: the same thing added to each safe separately, say. They stay in the other
	// safe; rename or delete one and merge again.
	NameClashes []*record.Record
	Same        int  // identical in both
	Changed     bool // the document changed and needs saving
}

// Merge adds the incoming records to d (except those whose name another record here has: see
// NameClashes). A record in both (same id) keeps whichever version was
// updated most recently, with that version's updated_at; on a tie with different contents, ours
// stays and the record is reported as a conflict. The losing version's secrets aren't lost: a
// login's history gets the ones that differ, and both histories are combined. Merging the same
// records again changes nothing.
func (d *Document) Merge(in []*record.Record) (MergeResult, error) {
	var res MergeResult
	for _, r := range in {
		r.Normalize()
		if _, err := r.Validate(); err != nil {
			return MergeResult{}, fmt.Errorf("%s (%s): %w", r.ID, record.Printable(r.Name), err)
		}
		cur := d.Get(r.ID)
		takesName := cur == nil || r.UpdatedAt.After(cur.UpdatedAt) // the incoming version would be ours
		if takesName && d.NameTaken(r.Name, r.ID) != nil {
			res.NameClashes = append(res.NameClashes, r)
			continue
		}
		switch {
		case cur == nil:
			if err := d.Add(r); err != nil {
				return MergeResult{}, err
			}
			res.Added = append(res.Added, r)
			res.Changed = true
		case sameRecord(cur, r):
			res.Same++
			if keepLosersSecrets(cur, r) { // the same but for their histories: combine them
				res.Changed = true
			}
		case r.UpdatedAt.After(cur.UpdatedAt):
			keepLosersSecrets(r, cur)
			d.Delete(cur.ID)
			if err := d.Add(r); err != nil {
				return MergeResult{}, err
			}
			res.Updated = append(res.Updated, r)
			res.Changed = true
		default:
			if r.UpdatedAt.Equal(cur.UpdatedAt) {
				res.Conflicts = append(res.Conflicts, cur)
			} else {
				res.Kept = append(res.Kept, cur)
			}
			if keepLosersSecrets(cur, r) {
				res.Changed = true
			}
		}
	}
	return res, nil
}

// sameRecord reports whether a and b are the same record but for their histories (after one
// merge, ours has theirs' secrets in its history).
func sameRecord(a, b *record.Record) bool {
	ca, cb := *a, *b
	ca.History, cb.History = nil, nil
	ja, errA := json.Marshal(ca)
	jb, errB := json.Marshal(cb)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

// keepLosersSecrets puts into winner's history the loser's secret values that differ from the
// winner's and aren't in its history yet (replaced, as far as we know, when the winner was
// updated), and the loser's own history. Only types that keep a history (logins) do. It
// reports whether winner changed.
func keepLosersSecrets(winner, loser *record.Record) bool {
	s := winner.Schema()
	if !s.History {
		return false
	}
	before, _ := json.Marshal(winner.History)
	add := func(field string, p record.PastValue) {
		if p.Value == "" || p.Value == winner.Fields[field] {
			return
		}
		for _, h := range winner.History[field] {
			if h.Value == p.Value {
				return
			}
		}
		if winner.History == nil {
			winner.History = map[string][]record.PastValue{}
		}
		winner.History[field] = append(winner.History[field], p)
	}
	for _, f := range s.Fields {
		if !f.Secret {
			continue
		}
		add(f.Name, record.PastValue{Value: loser.Fields[f.Name], ReplacedAt: winner.UpdatedAt})
		for _, p := range loser.History[f.Name] {
			add(f.Name, p)
		}
		h := winner.History[f.Name]
		slices.SortStableFunc(h, func(a, b record.PastValue) int { return b.ReplacedAt.Compare(a.ReplacedAt) })
		if len(h) > record.HistoryLimit {
			winner.History[f.Name] = h[:record.HistoryLimit]
		}
	}
	after, _ := json.Marshal(winner.History)
	return !bytes.Equal(before, after) // a value added, then trimmed off as the oldest, is no change
}

// ReadSibling reads another safe in this safe's directory with the same keys (no second
// passphrase prompt; the directory's lock is already held).
func (s *Safe) ReadSibling(path string) (*Document, error) {
	enc, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	plain, err := s.Keyring.Decrypt(enc)
	if err != nil {
		return nil, fmt.Errorf("unlocking %s: %w", path, err)
	}
	return Decode(plain)
}
