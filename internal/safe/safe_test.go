package safe

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rhugga/digisafe/internal/prompt"
	"github.com/rhugga/digisafe/internal/record"
)

const pass = "correct horse battery"

func init() { ScryptWorkFactor = 10 } // fast tests

func newSafe(t *testing.T) Paths {
	t.Helper()
	p := NewPaths(filepath.Join(t.TempDir(), "ds"), "")
	created, err := Create(p, prompt.NewScript(), func() (string, error) { return pass, nil })
	if err != nil || !created {
		t.Fatalf("Create = %v, %v", created, err)
	}
	return p
}

func openSafe(t *testing.T, p Paths, ui *prompt.Script, write bool) *Safe {
	t.Helper()
	s, err := Open(p, ui, Options{Write: write})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func addNote(t *testing.T, s *Safe, name, content string) *record.Record {
	t.Helper()
	r, err := record.New("note", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	r.Name, r.Fields["content"] = name, content
	if err := s.Doc.Add(r); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCreateOpenSave(t *testing.T) {
	p := newSafe(t)
	for _, f := range []string{p.Safe, p.Identity, p.IdentityPub} {
		st, err := os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
		// Windows has no Unix modes (Go reports 0666); the profile directory's ACL protects it
		if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v, want 0600", f, st.Mode().Perm())
		}
	}
	if st, _ := os.Stat(p.Dir); runtime.GOOS != "windows" && st.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v, want 0700", st.Mode().Perm())
	}
	if _, err := Create(p, prompt.NewScript(), nil); err == nil {
		t.Error("creating over an existing safe should fail")
	}

	s := openSafe(t, p, prompt.NewScript(pass), true)
	r := addNote(t, s, "first", "top secret")
	s.Close()

	enc, _ := os.ReadFile(p.Safe)
	if bytes.Contains(enc, []byte("top secret")) || bytes.Contains(enc, []byte("first")) {
		t.Fatal("the safe file holds plaintext")
	}
	s2 := openSafe(t, p, prompt.NewScript(pass), false)
	got, err := s2.Doc.Find(r.ID[:6])
	if err != nil || got.Fields["content"] != "top secret" {
		t.Fatalf("Find = %+v, %v", got, err)
	}
}

func TestWrongPassphrase(t *testing.T) {
	p := newSafe(t)
	ui := prompt.NewScript("nope", "still wrong", "wrong again")
	if _, err := Open(p, ui, Options{}); err == nil || !strings.Contains(err.Error(), "wrong passphrase") {
		t.Fatalf("Open with a wrong passphrase: %v", err)
	}
	// a typo followed by the right passphrase works
	s := openSafe(t, p, prompt.NewScript("typo", pass), false)
	if s.Doc.Version != FormatVersion {
		t.Errorf("version %d", s.Doc.Version)
	}
}

func TestSecondSafeSharesTheKey(t *testing.T) {
	p := newSafe(t)
	p2 := NewPaths(p.Dir, "other.age")
	created, err := Create(p2, prompt.NewScript(), func() (string, error) {
		t.Fatal("no new passphrase should be asked for")
		return "", nil
	})
	if err != nil || created {
		t.Fatalf("Create second safe = %v, %v", created, err)
	}
	openSafe(t, p2, prompt.NewScript(pass), false)
}

func TestBackups(t *testing.T) {
	p := newSafe(t)
	s := openSafe(t, p, prompt.NewScript(pass), true)
	for i := range Backups + 2 {
		addNote(t, s, "n"+string(rune('a'+i)), "x")
	}
	if got := ExistingBackups(p.Safe); len(got) != Backups {
		t.Fatalf("backups = %v, want %d", got, Backups)
	}
	// backup 1 is the version before the last save: one record fewer
	enc, _ := os.ReadFile(BackupPath(p.Safe, 1))
	plain, err := s.Keyring.Decrypt(enc)
	if err != nil {
		t.Fatal(err)
	}
	d, err := Decode(plain)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Records) != len(s.Doc.Records)-1 {
		t.Errorf("backup 1 has %d records, want %d", len(d.Records), len(s.Doc.Records)-1)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(p.Dir, ".*tmp-*")); len(leftovers) > 0 {
		t.Errorf("temporary files left behind: %v", leftovers)
	}
}

func TestChangePassphrase(t *testing.T) {
	p := newSafe(t)
	k, err := LoadKeyring(p, prompt.NewScript(pass), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.ChangePassphrase("a brand new passphrase"); err != nil {
		t.Fatal(err)
	}
	openSafe(t, p, prompt.NewScript("a brand new passphrase"), false)
	if _, err := Open(p, prompt.NewScript(pass, pass, pass), Options{}); err == nil {
		t.Error("the old passphrase still works")
	}
	if err := k.ChangePassphrase("short"); err == nil {
		t.Error("a short passphrase was accepted")
	}
}

func TestRekey(t *testing.T) {
	p := newSafe(t)
	s := openSafe(t, p, prompt.NewScript(pass), true)
	addNote(t, s, "a", "1")
	addNote(t, s, "b", "2")
	oldPub := s.Keyring.PublicKey()
	if _, _, err := s.Rekey("the second passphrase"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if s.Keyring.PublicKey() == oldPub {
		t.Fatal("rekey kept the old key")
	}
	if _, err := os.Stat(p.Identity + ".new"); err == nil {
		t.Error("identity.age.new left behind")
	}
	s2 := openSafe(t, p, prompt.NewScript("the second passphrase"), false)
	if len(s2.Doc.Records) != 2 {
		t.Errorf("%d records after rekey", len(s2.Doc.Records))
	}
	// every backup opens with the new key
	for _, b := range ExistingBackups(p.Safe) {
		enc, _ := os.ReadFile(b)
		if _, err := s2.Keyring.Decrypt(enc); err != nil {
			t.Errorf("%s: %v", b, err)
		}
	}
}

func TestLock(t *testing.T) {
	p := newSafe(t)
	old := LockTimeout
	LockTimeout = 200 * time.Millisecond
	defer func() { LockTimeout = old }()
	l, err := Acquire(p.Lock, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(p.Lock, false); err == nil || !strings.Contains(err.Error(), "in use") {
		t.Errorf("second lock: %v", err)
	}
	l.Release()
	r1, err := Acquire(p.Lock, false)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Acquire(p.Lock, false) // shared locks coexist
	if err != nil {
		t.Fatal(err)
	}
	r1.Release()
	r2.Release()
}

func TestDocument(t *testing.T) {
	if _, err := Decode([]byte(`{"version":1,"records":[],"extra":1}`)); err == nil {
		t.Error("unknown field accepted")
	}
	if _, err := Decode([]byte(`{"version":99,"records":[]}`)); err == nil || !strings.Contains(err.Error(), "upgrade") {
		t.Errorf("newer version: %v", err)
	}
	d := NewDocument()
	a := &record.Record{ID: "aaaa1111-0000-0000-0000-000000000000", Name: "one"}
	b := &record.Record{ID: "aaaa2222-0000-0000-0000-000000000000", Name: "two"}
	d.Add(a)
	d.Add(b)
	if err := d.Add(a); err == nil {
		t.Error("duplicate id accepted")
	}
	if _, err := d.Find("aaaa"); err == nil || !strings.Contains(err.Error(), "matches 2") {
		t.Errorf("ambiguous prefix: %v", err)
	}
	if r, err := d.Find("AAAA2"); err != nil || r != b {
		t.Errorf("Find(AAAA2) = %v, %v", r, err)
	}
	if _, err := d.Find("bbbb"); err == nil {
		t.Error("Find(bbbb) found something")
	}
}

func TestNewPaths(t *testing.T) {
	dir := filepath.Join("home", ".digisafe")
	for name, want := range map[string]string{
		"":              filepath.Join(dir, "mysafe.age"),
		"work.age":      filepath.Join(dir, "work.age"),
		"/tmp/work.age": "/tmp/work.age",
		"sub/work.age":  "sub/work.age",
		`C:\s\work.age`: `C:\s\work.age`, // a Windows path is never joined onto dir
		"C:/s/work.age": "C:/s/work.age",
	} {
		if got := NewPaths(dir, name).Safe; got != want {
			t.Errorf("NewPaths(%q).Safe = %q, want %q", name, got, want)
		}
	}
}

// A safe written before credit_card was renamed reads its cards as bank_card, and saves them so.
func TestDecodeRenamedType(t *testing.T) {
	doc := `{"version":1,"records":[{"id":"0b6f4f7e-6d1c-4a4e-9a51-6c2f4b1e8a10","type":"credit_card","name":"Old card",
		"fields":{"card_no":"4111111111111111"},"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}]}`
	d, err := Decode([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	r := d.Records[0]
	if r.Type != "bank_card" || r.Schema().Type != "bank_card" {
		t.Errorf("type %q", r.Type)
	}
	if got := d.Search("", "credit_card"); len(got) != 1 {
		t.Errorf("Search by the old name: %d", len(got))
	}
	b, _ := d.Encode()
	if !strings.Contains(string(b), `"type": "bank_card"`) || strings.Contains(string(b), "credit_card") {
		t.Errorf("encoded:\n%s", b)
	}
}

// A version 1 safe (before records' history) reads, and is saved as version 2; a newer one
// asks for an upgrade.
func TestFormatUpgrade(t *testing.T) {
	d, err := Decode([]byte(`{"version":1,"records":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := d.Encode(); !strings.Contains(string(b), `"version": 2`) {
		t.Errorf("encoded:\n%s", b)
	}
	if _, err := Decode([]byte(`{"version":3,"records":[]}`)); err == nil || !strings.Contains(err.Error(), "upgrade it") {
		t.Errorf("version 3: %v", err)
	}
}

// A login saved with the old "login" field reads it as username, and saves it so.
func TestDecodeRenamedField(t *testing.T) {
	doc := `{"version":2,"records":[{"id":"0b6f4f7e-6d1c-4a4e-9a51-6c2f4b1e8a10","type":"login","name":"Old",
		"fields":{"login":"me"},"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}]}`
	d, err := Decode([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if f := d.Records[0].Fields; f["username"] != "me" || len(f) != 1 {
		t.Errorf("fields = %v", f)
	}
	if b, _ := d.Encode(); !strings.Contains(string(b), `"username": "me"`) {
		t.Errorf("encoded:\n%s", b)
	}
}
