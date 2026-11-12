package safe

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"filippo.io/age"
	"filippo.io/age/plugin"

	"github.com/rhugga/digisafe/internal/prompt"
)

// ScryptWorkFactor is the scrypt cost (log2 N) protecting the passphrase-encrypted key. 18 is
// age's default; tests lower it.
var ScryptWorkFactor = 18

// MinPassphrase is the minimum passphrase length.
const MinPassphrase = 12

// Paths are the files that make up a safe. See digisafe.md, "Files".
type Paths struct {
	Dir         string
	Safe        string
	Identity    string // the X25519 private key, passphrase-encrypted
	IdentityPub string // its public key
	PluginKeys  string // plugin identities (Secure Enclave, YubiKey)
	Lock        string
}

// DefaultSafe is the safe used when none is named and no context is set.
const DefaultSafe = "mysafe.age"

// SafePath resolves a safe name the way every command does: a bare name is a file in dir, with
// ".age" added if it has no extension ("work" is dir/work.age); anything with a separator (either
// kind, so Windows users can write C:/x/y.age or C:\x\y.age) is a path. "" is DefaultSafe.
func SafePath(dir, name string) string {
	if name == "" {
		name = DefaultSafe
	}
	if strings.ContainsAny(name, `/\`) || filepath.VolumeName(name) != "" {
		return filepath.Clean(name)
	}
	if filepath.Ext(name) == "" {
		name += ".age"
	}
	return filepath.Join(dir, name)
}

// reserved are the names of digisafe's own files, which a safe must not take: a safe named
// "identity" (identity.age) would overwrite the key it was just created with.
var reserved = map[string]bool{
	"identity.age": true, "identity.pub": true, "identity.age.new": true, "identity.pub.new": true,
	"plugin-keys.txt": true, ".lock": true, ContextFile: true,
}

var backupName = regexp.MustCompile(`\.age\.[0-9]+$`)

// CheckSafeName refuses safe paths that would collide with digisafe's own files: the key files,
// the lock, the context, a backup (x.age.1) or a hidden file (temporary files start with ".").
func CheckSafeName(path string) error {
	base := filepath.Base(path)
	if reserved[base] || backupName.MatchString(base) || strings.HasPrefix(base, ".") {
		return fmt.Errorf("%s is a name digisafe uses for its own files; pick another name for the safe", base)
	}
	return nil
}

// NewPaths lays out a safe's files. A safe's keys always sit beside it: for a bare name that's
// dir; for a path, the path's own directory (so a safe on a USB stick carries its own key).
func NewPaths(dir, safeName string) Paths {
	safe := SafePath(dir, safeName)
	dir = filepath.Dir(safe)
	return Paths{
		Dir:         dir,
		Safe:        safe,
		Identity:    filepath.Join(dir, "identity.age"),
		IdentityPub: filepath.Join(dir, "identity.pub"),
		PluginKeys:  filepath.Join(dir, "plugin-keys.txt"),
		Lock:        filepath.Join(dir, ".lock"),
	}
}

// Keyring holds the keys a safe is encrypted to and unlocks them on demand.
type Keyring struct {
	paths          Paths
	ui             prompt.Prompter
	passphraseOnly bool

	pub     *age.X25519Recipient
	secret  *age.X25519Identity // nil until the passphrase has been entered
	plugins []*plugin.Identity
}

// LoadKeyring reads the public key and plugin keys. Nothing is decrypted yet.
func LoadKeyring(paths Paths, ui prompt.Prompter, passphraseOnly bool) (*Keyring, error) {
	b, err := os.ReadFile(paths.IdentityPub)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no safe key in %s: run `digisafe safe create` first", paths.Dir)
	}
	if err != nil {
		return nil, err
	}
	pub, err := age.ParseX25519Recipient(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", paths.IdentityPub, err)
	}
	k := &Keyring{paths: paths, ui: ui, passphraseOnly: passphraseOnly, pub: pub}
	ids, err := readPluginKeys(paths.PluginKeys)
	if err != nil {
		return nil, err
	}
	for _, s := range ids {
		id, err := plugin.NewIdentity(s, k.clientUI())
		if err != nil {
			return nil, fmt.Errorf("%s: %w", paths.PluginKeys, err)
		}
		k.plugins = append(k.plugins, id)
	}
	return k, nil
}

func readPluginKeys(path string) ([]string, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseIdentityLines(f)
}

// parseIdentityLines returns the AGE-PLUGIN- lines of an identity file, skipping comments.
func parseIdentityLines(r io.Reader) ([]string, error) {
	var out []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "AGE-PLUGIN-") {
			return nil, fmt.Errorf("not an age plugin identity: %.20s...", line)
		}
		out = append(out, line)
	}
	return out, sc.Err()
}

func (k *Keyring) clientUI() *plugin.ClientUI {
	return &plugin.ClientUI{
		DisplayMessage: func(name, message string) error {
			k.ui.Message("%s: %s", name, message)
			return nil
		},
		RequestValue: func(name, p string, secret bool) (string, error) {
			if secret {
				return k.ui.Secret(fmt.Sprintf("%s: %s ", name, p))
			}
			return k.ui.Line(fmt.Sprintf("%s: %s ", name, p))
		},
		Confirm: func(name, p, yes, no string) (bool, error) {
			if no == "" {
				k.ui.Message("%s: %s", name, p)
				return true, nil
			}
			return k.ui.Confirm(fmt.Sprintf("%s: %s (y = %s, n = %s)", name, p, yes, no), true)
		},
		WaitTimer: func(name string) {
			k.ui.Message("waiting on the %s key (touch it or use Touch ID)...", name)
		},
	}
}

// Recipients are the keys the safe is encrypted to: the X25519 public key and every plugin key.
func (k *Keyring) Recipients() []age.Recipient {
	rs := []age.Recipient{k.pub}
	for _, p := range k.plugins {
		rs = append(rs, p.Recipient())
	}
	return rs
}

// PublicKey is the safe's X25519 public key.
func (k *Keyring) PublicKey() string { return k.pub.String() }

// PluginKeys are the plugin identities.
func (k *Keyring) PluginKeys() []*plugin.Identity { return k.plugins }

// Encrypt encrypts data to every recipient.
func (k *Keyring) Encrypt(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, k.Recipients()...)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Decrypt decrypts data with a plugin key if one is configured (and --passphrase wasn't
// given), falling back to the passphrase.
func (k *Keyring) Decrypt(data []byte) ([]byte, error) {
	if k.secret == nil && len(k.plugins) > 0 && !k.passphraseOnly {
		ids := make([]age.Identity, len(k.plugins))
		for i, p := range k.plugins {
			ids[i] = p
		}
		out, err := decrypt(data, ids...)
		if err == nil {
			return out, nil
		}
		k.ui.Message("hardware key did not unlock the safe (%v); using the passphrase", err)
	}
	if err := k.Unlock(); err != nil {
		return nil, err
	}
	return decrypt(data, k.secret)
}

func decrypt(data []byte, ids ...age.Identity) ([]byte, error) {
	r, err := age.Decrypt(bytes.NewReader(data), ids...)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

// Unlock decrypts the X25519 private key with the passphrase, allowing 3 attempts.
func (k *Keyring) Unlock() error {
	if k.secret != nil {
		return nil
	}
	enc, err := os.ReadFile(k.paths.Identity)
	if err != nil {
		return fmt.Errorf("reading the safe key: %w", err)
	}
	for attempt := 1; ; attempt++ {
		pass, err := k.ui.Secret("passphrase for " + filepath.Base(k.paths.Safe) + ": ")
		if err != nil {
			return err
		}
		id, err := age.NewScryptIdentity(pass)
		if err != nil {
			return err
		}
		plain, err := decrypt(enc, id)
		var noMatch *age.NoIdentityMatchError
		if errors.As(err, &noMatch) {
			if attempt == 3 {
				return errors.New("wrong passphrase")
			}
			k.ui.Message("wrong passphrase, try again")
			continue
		}
		if err != nil {
			return fmt.Errorf("decrypting the safe key: %w", err)
		}
		secret, err := parseX25519(plain)
		if err != nil {
			return err
		}
		if secret.Recipient().String() != k.pub.String() {
			return fmt.Errorf("%s does not match %s", k.paths.Identity, k.paths.IdentityPub)
		}
		k.secret = secret
		return nil
	}
}

func parseX25519(b []byte) (*age.X25519Identity, error) {
	ids, err := age.ParseIdentities(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("reading the safe key: %w", err)
	}
	for _, id := range ids {
		if x, ok := id.(*age.X25519Identity); ok {
			return x, nil
		}
	}
	return nil, errors.New("the safe key file holds no X25519 key")
}

// CheckPassphrase enforces the minimum length.
func CheckPassphrase(p string) error {
	if len([]rune(p)) < MinPassphrase {
		return fmt.Errorf("a passphrase needs at least %d characters", MinPassphrase)
	}
	return nil
}

// AskNewPassphrase prompts twice for a new passphrase.
func AskNewPassphrase(ui prompt.Prompter, what string) (string, error) {
	for attempt := 1; ; attempt++ {
		p, err := ui.Secret(what + ": ")
		if err != nil {
			return "", err
		}
		if err := CheckPassphrase(p); err != nil {
			if attempt == 3 {
				return "", err
			}
			ui.Message("%v", err)
			continue
		}
		again, err := ui.Secret("again: ")
		if err != nil {
			return "", err
		}
		if p != again {
			if attempt == 3 {
				return "", errors.New("the passphrases don't match")
			}
			ui.Message("the passphrases don't match, try again")
			continue
		}
		return p, nil
	}
}

// keyFiles are a new X25519 key's two files.
type keyFiles struct {
	secret  *age.X25519Identity
	enc     []byte // identity.age content
	pubText []byte // identity.pub content
}

func newKeyFiles(passphrase string) (*keyFiles, error) {
	if err := CheckPassphrase(passphrase); err != nil {
		return nil, err
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, err
	}
	return sealKey(id, passphrase)
}

func sealKey(id *age.X25519Identity, passphrase string) (*keyFiles, error) {
	r, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, err
	}
	r.SetWorkFactor(ScryptWorkFactor)
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, r)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(w, "# created: %s\n# public key: %s\n%s\n", time.Now().UTC().Format(time.RFC3339), id.Recipient(), id)
	if err := w.Close(); err != nil {
		return nil, err
	}
	return &keyFiles{secret: id, enc: buf.Bytes(), pubText: []byte(id.Recipient().String() + "\n")}, nil
}

// CreateKeyring generates the safe's key pair, protected by passphrase.
func CreateKeyring(paths Paths, ui prompt.Prompter, passphrase string) (*Keyring, error) {
	kf, err := newKeyFiles(passphrase)
	if err != nil {
		return nil, err
	}
	if err := WriteAtomic(paths.Identity, kf.enc, false); err != nil {
		return nil, err
	}
	if err := WriteAtomic(paths.IdentityPub, kf.pubText, false); err != nil {
		return nil, err
	}
	return &Keyring{paths: paths, ui: ui, pub: kf.secret.Recipient(), secret: kf.secret}, nil
}

// ChangePassphrase re-encrypts the private key with a new passphrase. The safe itself is
// unchanged: it is encrypted to the key, not the passphrase.
func (k *Keyring) ChangePassphrase(newPassphrase string) error {
	if err := k.Unlock(); err != nil {
		return err
	}
	if err := CheckPassphrase(newPassphrase); err != nil {
		return err
	}
	kf, err := sealKey(k.secret, newPassphrase)
	if err != nil {
		return err
	}
	// No backup: a copy still sealed with the old passphrase would defeat changing it.
	return WriteAtomic(k.paths.Identity, kf.enc, false)
}

// AddPluginKeys adds plugin identities and writes plugin-keys.txt. The caller re-encrypts the
// safe so the new keys can open it.
func (k *Keyring) AddPluginKeys(ids []string) error {
	existing := map[string]bool{}
	for _, p := range k.plugins {
		existing[p.String()] = true
	}
	for _, s := range ids {
		if existing[s] {
			return fmt.Errorf("that %s key is already added", pluginName(s))
		}
		id, err := plugin.NewIdentity(s, k.clientUI())
		if err != nil {
			return err
		}
		k.plugins = append(k.plugins, id)
		existing[s] = true
	}
	return k.writePluginKeys()
}

// RemovePluginKey removes plugin key i (0-based) and writes plugin-keys.txt.
func (k *Keyring) RemovePluginKey(i int) error {
	if i < 0 || i >= len(k.plugins) {
		return fmt.Errorf("no plugin key %d (there are %d)", i+1, len(k.plugins))
	}
	k.plugins = append(k.plugins[:i], k.plugins[i+1:]...)
	return k.writePluginKeys()
}

func (k *Keyring) writePluginKeys() error {
	var b strings.Builder
	b.WriteString("# age plugin identities that can unlock the safe (managed by `digisafe keys`)\n")
	for _, p := range k.plugins {
		fmt.Fprintf(&b, "%s\n", p)
	}
	return WriteAtomic(k.paths.PluginKeys, []byte(b.String()), false)
}

func pluginName(s string) string {
	rest := strings.TrimPrefix(s, "AGE-PLUGIN-")
	if i := strings.Index(rest, "-"); i > 0 {
		return strings.ToLower(rest[:i])
	}
	return "plugin"
}

// ReadPluginIdentityFile reads the plugin identities from a file such as the one
// `age-plugin-se keygen` writes.
func ReadPluginIdentityFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	ids, err := parseIdentityLines(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("%s holds no age plugin identity (AGE-PLUGIN-...)", path)
	}
	return ids, nil
}
