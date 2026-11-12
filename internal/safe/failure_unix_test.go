//go:build !windows

package safe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rhugga/digisafe/internal/prompt"
)

// readOnly makes dir unwritable for the rest of the test (and writable again for cleanup).
func readOnly(t *testing.T, dir string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
}

// A save that can't write leaves the old safe in place and opening.
func TestSaveFailsSafely(t *testing.T) {
	p := newSafe(t)
	s := openSafe(t, p, prompt.NewScript(pass), true)
	addNote(t, s, "kept", "x")
	before, _ := os.ReadFile(p.Safe)

	readOnly(t, p.Dir)
	r := addNoteNoSave(t, s, "lost")
	if err := s.Save(); err == nil {
		t.Fatal("Save into a read-only directory succeeded")
	}
	s.Doc.Delete(r.ID)
	if _, err := s.ReencryptKeyed(); err == nil {
		t.Error("re-encrypting backups into a read-only directory succeeded")
	}
	if _, _, err := s.Rekey("a brand new passphrase"); err == nil {
		t.Error("rekey into a read-only directory succeeded")
	}
	if err := s.Keyring.AddPluginKeys([]string{fakeKey(t, false)}); err == nil {
		t.Error("adding a key into a read-only directory succeeded")
	}
	if err := s.Keyring.ChangePassphrase("a brand new passphrase"); err == nil {
		t.Error("changing the passphrase in a read-only directory succeeded")
	}
	after, _ := os.ReadFile(p.Safe)
	if string(before) != string(after) {
		t.Error("a failed save changed the safe")
	}
	if left, _ := filepath.Glob(filepath.Join(p.Dir, ".*tmp-*")); len(left) > 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}

func TestCreateInAReadOnlyDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ds")
	if _, err := EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	readOnly(t, dir)
	if _, err := Create(NewPaths(dir, ""), prompt.NewScript(), func() (string, error) { return pass, nil }); err == nil {
		t.Error("Create in a read-only directory succeeded")
	}
}

func TestCorruptFiles(t *testing.T) {
	// a damaged safe file
	p := newSafe(t)
	os.WriteFile(p.Safe, []byte("not age at all"), 0o600)
	if _, err := Open(p, prompt.NewScript(pass), Options{}); err == nil || !strings.Contains(err.Error(), "unlocking") {
		t.Errorf("corrupt safe: %v", err)
	}

	// a damaged key file
	p = newSafe(t)
	os.WriteFile(p.Identity, []byte("not age at all"), 0o600)
	if _, err := Open(p, prompt.NewScript(pass), Options{}); err == nil || !strings.Contains(err.Error(), "decrypting the safe key") {
		t.Errorf("corrupt key: %v", err)
	}
	os.Remove(p.Identity)
	if _, err := Open(p, prompt.NewScript(pass), Options{}); err == nil || !strings.Contains(err.Error(), "reading the safe key") {
		t.Errorf("missing key: %v", err)
	}

	// a damaged backup stops rekey and backup re-encryption before anything changes
	p = newSafe(t)
	s := openSafe(t, p, prompt.NewScript(pass), true)
	addNote(t, s, "a", "1")
	addNote(t, s, "b", "2")
	os.WriteFile(BackupPath(p.Safe, 1), []byte("damaged"), 0o600)
	pub := s.Keyring.PublicKey()
	if _, _, err := s.Rekey("a brand new passphrase"); err == nil {
		t.Error("rekey with a damaged backup succeeded")
	}
	if s.Keyring.PublicKey() != pub {
		t.Error("a failed rekey changed the key")
	}
	if _, err := s.ReencryptKeyed(); err == nil {
		t.Error("re-encrypting a damaged backup succeeded")
	}

	// a key file holding something other than an X25519 key
	if _, err := parseX25519([]byte("# just a comment\n")); err == nil {
		t.Error("parseX25519 of no key succeeded")
	}
	if _, err := parseX25519([]byte("AGE-SECRET-KEY-1NOTVALID\n")); err == nil {
		t.Error("parseX25519 of a bad key succeeded")
	}
}

// EnsureDir creates missing directories 0700 and leaves existing ones alone, reporting whether
// they're open to other users.
func TestEnsureDirLeavesExistingDirectories(t *testing.T) {
	fresh := filepath.Join(t.TempDir(), "a", "b")
	if open, err := EnsureDir(fresh); err != nil || open {
		t.Fatalf("EnsureDir(new) = %v, %v", open, err)
	}
	if st, _ := os.Stat(fresh); st.Mode().Perm() != 0o700 {
		t.Errorf("a new directory is %v, want 0700", st.Mode().Perm())
	}
	dir := filepath.Join(t.TempDir(), "d")
	os.Mkdir(dir, 0o755)
	os.Chmod(dir, 0o755)
	if open, err := EnsureDir(dir); err != nil || !open {
		t.Errorf("EnsureDir(0755) = %v, %v; want open", open, err)
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o755 {
		t.Errorf("an existing 0755 directory became %v", st.Mode().Perm())
	}
	// creating a safe there says so, and leaves the directory as it was
	ui := prompt.NewScript()
	if _, err := Create(NewPaths(dir, ""), ui, func() (string, error) { return pass, nil }); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o755 || !strings.Contains(strings.Join(ui.Messages, "\n"), "chmod 700") {
		t.Errorf("after Create: %v, %q", st.Mode().Perm(), ui.Messages)
	}
	f := filepath.Join(t.TempDir(), "file")
	os.WriteFile(f, nil, 0o600)
	if _, err := EnsureDir(f); err == nil {
		t.Error("EnsureDir of a file succeeded")
	}
}

// A safe in a directory that can't be written (read-only media) still opens for reading;
// writing says why it can't.
func TestReadOnlyDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	p := newSafe(t)
	os.Remove(p.Lock)
	os.Chmod(p.Dir, 0o500)
	t.Cleanup(func() { os.Chmod(p.Dir, 0o700) })
	s, err := Open(p, prompt.NewScript(pass), Options{})
	if err != nil {
		t.Fatalf("reading without a lock file: %v", err)
	}
	s.Close()
	if _, err := Open(p, prompt.NewScript(pass), Options{Write: true}); err == nil {
		t.Error("opening for writing succeeded")
	}
	// with a lock file (made before the directory went read-only), it's locked read-only
	os.Chmod(p.Dir, 0o700)
	l, _ := Acquire(p.Lock, false)
	l.Release()
	os.Chmod(p.Lock, 0o400)
	os.Chmod(p.Dir, 0o500)
	s, err = Open(p, prompt.NewScript(pass), Options{})
	if err != nil {
		t.Fatalf("reading with a read-only lock file: %v", err)
	}
	if s.lock == nil || s.lock.f == nil {
		t.Error("the existing lock file wasn't locked")
	}
	s.Close()
}
