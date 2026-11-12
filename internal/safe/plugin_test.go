package safe

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"filippo.io/age/plugin"

	"github.com/rhugga/digisafe/internal/prompt"
	"github.com/rhugga/digisafe/internal/record"
)

// TestMain builds the fake age plugin (testdata/age-plugin-dstest) and puts it on PATH, so
// the plugin-key code runs as it would with age-plugin-se or age-plugin-yubikey.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "digisafe-plugin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	exe := filepath.Join(dir, "age-plugin-dstest")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", exe, "./testdata/age-plugin-dstest").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building the fake age plugin: %v\n%s", err, out)
		os.Exit(1)
	}
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// fakeKey returns a dstest plugin identity. With fail, it encrypts but never decrypts.
func fakeKey(t *testing.T, fail bool) string {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	data := id.String()
	if fail {
		data = "fail:" + data
	}
	return plugin.EncodeIdentity("dstest", []byte(data))
}

// addPluginKeys opens the safe with the passphrase, adds keys and saves.
func addPluginKeys(t *testing.T, p Paths, keys ...string) {
	t.Helper()
	s, err := Open(p, prompt.NewScript(pass), Options{Write: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Keyring.AddPluginKeys(keys); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
}

func TestPluginKeyUnlocksWithoutThePassphrase(t *testing.T) {
	p := newSafe(t)
	key := fakeKey(t, false)
	addPluginKeys(t, p, key)

	// no scripted answers: any passphrase prompt would fail the open
	ui := prompt.NewScript()
	s := openSafe(t, p, ui, false)
	if len(ui.Prompts) != 0 {
		t.Errorf("the plugin key should unlock without prompts, got %q", ui.Prompts)
	}
	if got := s.Keyring.PluginKeys(); len(got) != 1 || got[0].Name() != "dstest" {
		t.Errorf("PluginKeys = %v", got)
	}
	s.Close()

	// --passphrase skips the plugin key
	ui = prompt.NewScript(pass)
	s2, err := Open(p, ui, Options{PassphraseOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()
	if len(ui.Prompts) != 1 || ui.Prompts[0] != "passphrase for mysafe.age: " {
		t.Errorf("--passphrase prompts = %q", ui.Prompts)
	}

	// the same key can't be added twice
	s3 := openSafe(t, p, prompt.NewScript(), true)
	if err := s3.Keyring.AddPluginKeys([]string{key}); err == nil || !strings.Contains(err.Error(), "already added") {
		t.Errorf("duplicate key: %v", err)
	}
}

func TestPluginKeyFallsBackToThePassphrase(t *testing.T) {
	p := newSafe(t)
	addPluginKeys(t, p, fakeKey(t, true))
	ui := prompt.NewScript(pass)
	openSafe(t, p, ui, false)
	if len(ui.Messages) == 0 || !strings.Contains(ui.Messages[0], "hardware key did not unlock the safe") {
		t.Errorf("messages = %q", ui.Messages)
	}
	if len(ui.Prompts) != 1 {
		t.Errorf("want one passphrase prompt, got %q", ui.Prompts)
	}
}

func TestRemovePluginKey(t *testing.T) {
	p := newSafe(t)
	key := fakeKey(t, false)
	addPluginKeys(t, p, key)

	s := openSafe(t, p, prompt.NewScript(pass), true)
	if err := s.Keyring.RemovePluginKey(5); err == nil {
		t.Error("removing a key that isn't there succeeded")
	}
	if err := s.Keyring.RemovePluginKey(0); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReencryptKeyed(); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// the removed key opens neither the safe nor any backup
	id, err := plugin.NewIdentity(key, &plugin.ClientUI{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range append([]string{p.Safe}, ExistingBackups(p.Safe)...) {
		enc, _ := os.ReadFile(f)
		if _, err := decrypt(enc, id); err == nil {
			t.Errorf("the removed key still opens %s", filepath.Base(f))
		}
	}
	if b, _ := os.ReadFile(p.PluginKeys); strings.Contains(string(b), "AGE-PLUGIN-") {
		t.Errorf("plugin-keys.txt still lists a key:\n%s", b)
	}
}

func TestReadPluginIdentityFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		f := filepath.Join(dir, name)
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return f
	}
	key := fakeKey(t, false)
	ids, err := ReadPluginIdentityFile(write("ok.txt", "# created by keygen\n# public key: x\n\n"+key+"\n"))
	if err != nil || len(ids) != 1 || ids[0] != key {
		t.Fatalf("ReadPluginIdentityFile = %v, %v", ids, err)
	}
	if _, err := ReadPluginIdentityFile(write("empty.txt", "# nothing\n")); err == nil || !strings.Contains(err.Error(), "holds no age plugin identity") {
		t.Errorf("empty file: %v", err)
	}
	if _, err := ReadPluginIdentityFile(write("secret.txt", "AGE-SECRET-KEY-1ABC\n")); err == nil || !strings.Contains(err.Error(), "not an age plugin identity") {
		t.Errorf("non-plugin line: %v", err)
	}
	if _, err := ReadPluginIdentityFile(filepath.Join(dir, "missing.txt")); err == nil {
		t.Error("missing file succeeded")
	}
}

func TestKeyringErrors(t *testing.T) {
	p := NewPaths(t.TempDir(), "")
	if _, err := LoadKeyring(p, prompt.NewScript(), false); err == nil || !strings.Contains(err.Error(), "digisafe safe create") {
		t.Errorf("no key: %v", err)
	}
	if _, err := Open(p, prompt.NewScript(), Options{}); err == nil || !strings.Contains(err.Error(), "digisafe safe create") {
		t.Errorf("no safe: %v", err)
	}

	p = newSafe(t)
	os.WriteFile(p.IdentityPub, []byte("not a key\n"), 0o600)
	if _, err := LoadKeyring(p, prompt.NewScript(), false); err == nil {
		t.Error("a corrupt identity.pub loaded")
	}

	p = newSafe(t)
	os.WriteFile(p.PluginKeys, []byte("garbage\n"), 0o600)
	if _, err := LoadKeyring(p, prompt.NewScript(), false); err == nil {
		t.Error("a corrupt plugin-keys.txt loaded")
	}
	os.WriteFile(p.PluginKeys, []byte("AGE-PLUGIN-NOPE-1QQQQ\n"), 0o600)
	if _, err := LoadKeyring(p, prompt.NewScript(), false); err == nil {
		t.Error("an undecodable plugin key loaded")
	}

	// identity.age without identity.pub: create refuses rather than overwriting the key
	p = newSafe(t)
	os.Remove(p.IdentityPub)
	p2 := NewPaths(p.Dir, "second.age")
	if _, err := Create(p2, prompt.NewScript(), func() (string, error) { return pass, nil }); err == nil || !strings.Contains(err.Error(), "exists without") {
		t.Errorf("create with an orphaned key: %v", err)
	}

	// a key file that doesn't match identity.pub
	a, b := newSafe(t), newSafe(t)
	ka, _ := os.ReadFile(a.Identity)
	os.WriteFile(b.Identity, ka, 0o600)
	if _, err := Open(b, prompt.NewScript(pass), Options{}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("mismatched key: %v", err)
	}
}

func TestPassphrasePrompts(t *testing.T) {
	if err := CheckPassphrase("short"); err == nil {
		t.Error("a short passphrase passed")
	}
	// too short, then a mismatch, then right
	ui := prompt.NewScript("short", "long enough passphrase", "different", "long enough passphrase", "long enough passphrase")
	got, err := AskNewPassphrase(ui, "new passphrase")
	if err != nil || got != "long enough passphrase" {
		t.Fatalf("AskNewPassphrase = %q, %v", got, err)
	}
	if _, err := AskNewPassphrase(prompt.NewScript("a", "b", "c"), "new"); err == nil {
		t.Error("three short passphrases accepted")
	}
	if _, err := AskNewPassphrase(prompt.NewScript("long enough 1", "x", "long enough 2", "y", "long enough 3", "z"), "new"); err == nil || !strings.Contains(err.Error(), "don't match") {
		t.Errorf("three mismatches: %v", err)
	}
	if _, err := AskNewPassphrase(prompt.NewScript(), "new"); err == nil {
		t.Error("no input accepted")
	}
}

// The plugin UI callbacks route the plugin's prompts and messages to the user.
func TestClientUI(t *testing.T) {
	ui := prompt.NewScript("1234", "visible", "y")
	k := &Keyring{ui: ui}
	c := k.clientUI()
	if err := c.DisplayMessage("yubikey", "insert your key"); err != nil {
		t.Fatal(err)
	}
	if v, _ := c.RequestValue("yubikey", "PIN:", true); v != "1234" {
		t.Errorf("secret value = %q", v)
	}
	if v, _ := c.RequestValue("yubikey", "slot:", false); v != "visible" {
		t.Errorf("value = %q", v)
	}
	if ok, _ := c.Confirm("yubikey", "use slot 1?", "yes", "no"); !ok {
		t.Error("confirm = false")
	}
	if ok, _ := c.Confirm("yubikey", "touch the key", "ok", ""); !ok {
		t.Error("a confirm with only one choice should just show the prompt")
	}
	c.WaitTimer("yubikey")
	all := strings.Join(ui.Messages, "\n")
	for _, want := range []string{"yubikey: insert your key", "touch the key", "waiting on the yubikey key"} {
		if !strings.Contains(all, want) {
			t.Errorf("messages missing %q:\n%s", want, all)
		}
	}
	if pluginName("AGE-PLUGIN-YUBIKEY-1QQ") != "yubikey" || pluginName("garbage") != "plugin" {
		t.Error("pluginName")
	}
}

func TestDecodeErrors(t *testing.T) {
	for _, b := range []string{`{"records":[]}`, `not json`} {
		if _, err := Decode([]byte(b)); err == nil {
			t.Errorf("Decode(%s) succeeded", b)
		}
	}
	d, err := Decode([]byte(`{"version":1}`))
	if err != nil || d.Records == nil {
		t.Errorf("Decode with no records = %+v, %v", d, err)
	}
}

func TestSearchDeleteAndOtherSafes(t *testing.T) {
	p := newSafe(t)
	s := openSafe(t, p, prompt.NewScript(pass), true)
	a := addNote(t, s, "wifi home", "x")
	addNote(t, s, "wifi work", "y")
	addNote(t, s, "bank", "z")
	if got := s.Doc.Search("wifi", ""); len(got) != 2 {
		t.Errorf("Search(wifi) = %d records", len(got))
	}
	if got := s.Doc.Search("", "login"); len(got) != 0 {
		t.Errorf("Search by type login = %d records", len(got))
	}
	if !s.Doc.Delete(a.ID) || s.Doc.Delete(a.ID) {
		t.Error("Delete should succeed once")
	}
	if others, err := s.OtherSafes(); err != nil || len(others) != 0 {
		t.Errorf("OtherSafes = %v, %v", others, err)
	}
	if _, _, err := s.Rekey("short"); err == nil {
		t.Error("rekey accepted a short passphrase")
	}
	s.Close() // Create takes the same lock

	if _, err := Create(NewPaths(p.Dir, "second.age"), prompt.NewScript(), nil); err != nil {
		t.Fatal(err)
	}
	s = openSafe(t, p, prompt.NewScript(pass), false)
	if others, _ := s.OtherSafes(); len(others) != 1 || filepath.Base(others[0]) != "second.age" {
		t.Errorf("OtherSafes = %v", others)
	}
}

func TestStoreEdges(t *testing.T) {
	dir := t.TempDir()
	if err := WriteAtomic(filepath.Join(dir, "missing", "f"), []byte("x"), true); err == nil {
		t.Error("writing into a missing directory succeeded")
	}
	// backup of a file that doesn't exist yet is a no-op
	f := filepath.Join(dir, "f")
	if err := WriteAtomic(f, []byte("one"), true); err != nil {
		t.Fatal(err)
	}
	if len(ExistingBackups(f)) != 0 {
		t.Error("a backup was made of nothing")
	}
	if err := copyFile(f, filepath.Join(dir, "copy")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "copy")); string(b) != "one" {
		t.Errorf("copy = %q", b)
	}
	if err := copyFile(filepath.Join(dir, "nope"), filepath.Join(dir, "copy2")); err == nil {
		t.Error("copying a missing file succeeded")
	}
	if _, err := EnsureDir(filepath.Join(f, "sub")); err == nil {
		t.Error("EnsureDir under a file succeeded")
	}
}

// addNoteNoSave adds a note to the document without saving.
func addNoteNoSave(t *testing.T, s *Safe, name string) *record.Record {
	t.Helper()
	r, err := record.New("note", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	r.Name = name
	if err := s.Doc.Add(r); err != nil {
		t.Fatal(err)
	}
	return r
}

// A plugin key whose plugin is no longer installed: unlocking falls back to the passphrase, and
// saving fails (it can't encrypt to the key) rather than writing a safe that key can't open.
func TestUninstalledPlugin(t *testing.T) {
	p := newSafe(t)
	key := fakeKey(t, false)
	addPluginKeys(t, p, key)
	// the same key under a plugin name that isn't on PATH
	_, data, err := plugin.ParseIdentity(key)
	if err != nil {
		t.Fatal(err)
	}
	gone := plugin.EncodeIdentity("notinstalled", data)
	os.WriteFile(p.PluginKeys, []byte(gone+"\n"), 0o600)

	ui := prompt.NewScript(pass)
	s := openSafe(t, p, ui, true)
	if len(ui.Messages) == 0 || !strings.Contains(ui.Messages[0], "hardware key did not unlock") {
		t.Errorf("messages = %q", ui.Messages)
	}
	before, _ := os.ReadFile(p.Safe)
	if err := s.Save(); err == nil {
		t.Error("saving with an uninstalled plugin key succeeded")
	}
	if after, _ := os.ReadFile(p.Safe); string(after) != string(before) {
		t.Error("a failed save changed the safe")
	}
}

func TestSafePathAndContextFile(t *testing.T) {
	dir := filepath.Join("base")
	for name, want := range map[string]string{
		"":         filepath.Join(dir, "mysafe.age"),
		"work":     filepath.Join(dir, "work.age"),
		"work.age": filepath.Join(dir, "work.age"),
		"x.safe":   filepath.Join(dir, "x.safe"),
		"/u/f.age": "/u/f.age",
	} {
		if got := SafePath(dir, name); got != want {
			t.Errorf("SafePath(%q) = %q, want %q", name, got, want)
		}
	}
	// a path's keys are beside it
	if p := NewPaths(dir, "/u/f.age"); p.Dir != "/u" || p.Identity != filepath.Join("/u", "identity.age") {
		t.Errorf("NewPaths(/u/f.age) = %+v", p)
	}

	base := t.TempDir()
	if _, ok, err := ReadContext(base); ok || err != nil {
		t.Errorf("ReadContext with none = %v, %v", ok, err)
	}
	if _, err := SetContext(base, filepath.Join(base, "missing.age")); err == nil {
		t.Error("a context to a missing safe was set")
	}
	if _, err := SetContext(base, base); err == nil {
		t.Error("a context to a directory was set")
	}
	f := filepath.Join(base, "a.age")
	os.WriteFile(f, []byte("x"), 0o600)
	if _, err := SetContext(base, f); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := ReadContext(base); !ok || got != f {
		t.Errorf("ReadContext = %q, %v", got, ok)
	}
	if had, err := ClearContext(base); !had || err != nil {
		t.Errorf("ClearContext = %v, %v", had, err)
	}
	os.Mkdir(filepath.Join(base, ContextFile), 0o700) // unreadable as a file
	if _, _, err := ReadContext(base); err == nil {
		t.Error("ReadContext of a directory succeeded")
	}
	safes, _ := ListSafes(base)
	if len(safes) != 1 || filepath.Base(safes[0]) != "a.age" {
		t.Errorf("ListSafes = %v", safes)
	}
}

// Keys belong to the directory: after ReencryptKeyed a removed key opens no other safe there,
// and an added one opens them all.
func TestReencryptKeyedCoversOtherSafes(t *testing.T) {
	p := newSafe(t)
	key := fakeKey(t, false)
	addPluginKeys(t, p, key)
	work := NewPaths(p.Dir, "work")
	if _, err := Create(work, prompt.NewScript(), nil); err != nil {
		t.Fatal(err)
	}
	opens := func(key, file string) bool {
		id, _ := plugin.NewIdentity(key, &plugin.ClientUI{})
		enc, _ := os.ReadFile(file)
		_, err := decrypt(enc, id)
		return err == nil
	}
	s := openSafe(t, p, prompt.NewScript(pass), true)
	if err := s.Keyring.RemovePluginKey(0); err != nil {
		t.Fatal(err)
	}
	s.Save()
	if _, err := s.ReencryptKeyed(); err != nil {
		t.Fatal(err)
	}
	if opens(key, work.Safe) {
		t.Error("the removed key still opens work.age")
	}
	key2 := fakeKey(t, false)
	if err := s.Keyring.AddPluginKeys([]string{key2}); err != nil {
		t.Fatal(err)
	}
	s.Save()
	if _, err := s.ReencryptKeyed(); err != nil {
		t.Fatal(err)
	}
	if !opens(key2, work.Safe) || !opens(key2, p.Safe) {
		t.Error("the added key doesn't open both safes")
	}
}

func TestReservedSafeNames(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ds")
	for _, name := range []string{"identity", "identity.age", "identity.pub", "plugin-keys.txt", ".lock",
		filepath.Join(dir, "context"), "mysafe.age.1", "work.age.5", ".hidden", "identity.age.new"} {
		p := NewPaths(dir, name)
		if _, err := Create(p, prompt.NewScript(), func() (string, error) { return pass, nil }); err == nil ||
			!strings.Contains(err.Error(), "digisafe uses for its own files") {
			t.Errorf("Create(%q) = %v", name, err)
		}
		if _, err := SetContext(dir, p.Safe); err == nil || !strings.Contains(err.Error(), "its own files") {
			t.Errorf("SetContext(%q) = %v", name, err)
		}
	}
	// nothing was written
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("files written: %v", entries)
	}
	for _, ok := range []string{"work", "identity-cards", "my.identity.age", "context.age", "safe.v2.age"} {
		if err := CheckSafeName(SafePath(dir, ok)); err != nil {
			t.Errorf("CheckSafeName(%q) = %v", ok, err)
		}
	}
}
