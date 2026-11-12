package safe

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"

	"github.com/rhugga/digisafe/internal/prompt"
)

// Safe is an open safe: its keys, its decrypted document and the lock held on it.
type Safe struct {
	Paths   Paths
	Keyring *Keyring
	Doc     *Document
	lock    *Lock
	sum     [sha256.Size]byte // of the safe file as read, to notice another digisafe's save
}

// Options control how a safe is opened.
type Options struct {
	PassphraseOnly bool // skip the plugin keys
	Write          bool // take the exclusive lock (the command will save)
}

// Open locks, reads and decrypts the safe.
func Open(paths Paths, ui prompt.Prompter, opts Options) (*Safe, error) {
	if _, err := os.Stat(paths.Safe); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no safe at %s: run `digisafe safe create` first", paths.Safe)
	}
	lock, err := Acquire(paths.Lock, opts.Write)
	if err != nil {
		return nil, err
	}
	s, err := open(paths, ui, opts)
	if err != nil {
		lock.Release()
		return nil, err
	}
	s.lock = lock
	return s, nil
}

func open(paths Paths, ui prompt.Prompter, opts Options) (*Safe, error) {
	k, err := LoadKeyring(paths, ui, opts.PassphraseOnly)
	if err != nil {
		return nil, err
	}
	enc, err := os.ReadFile(paths.Safe)
	if err != nil {
		return nil, err
	}
	plain, err := k.Decrypt(enc)
	if err != nil {
		return nil, fmt.Errorf("unlocking %s: %w", paths.Safe, err)
	}
	doc, err := Decode(plain)
	if err != nil {
		return nil, err
	}
	return &Safe{Paths: paths, Keyring: k, Doc: doc, sum: sha256.Sum256(enc)}, nil
}

// Release drops the lock while the safe stays open, so a long interactive edit doesn't block
// other digisafe commands. Call Reacquire before saving.
func (s *Safe) Release() {
	s.lock.Release()
	s.lock = nil
}

// Reacquire takes the exclusive lock again after Release. If another digisafe saved the safe
// meanwhile, it's read again (Doc is replaced) and changed is true; the caller decides whether
// its edit still applies.
func (s *Safe) Reacquire() (changed bool, err error) {
	lock, err := Acquire(s.Paths.Lock, true)
	if err != nil {
		return false, err
	}
	s.lock = lock
	enc, err := os.ReadFile(s.Paths.Safe)
	if err != nil {
		return false, err
	}
	sum := sha256.Sum256(enc)
	if sum == s.sum {
		return false, nil
	}
	plain, err := s.Keyring.Decrypt(enc)
	if err != nil {
		return true, fmt.Errorf("re-reading %s: %w", s.Paths.Safe, err)
	}
	doc, err := Decode(plain)
	if err != nil {
		return true, err
	}
	s.Doc, s.sum = doc, sum
	return true, nil
}

// Close releases the lock.
func (s *Safe) Close() { s.lock.Release() }

// Save encrypts the document and replaces the safe, keeping the previous version as a backup.
func (s *Safe) Save() error {
	plain, err := s.Doc.Encode()
	if err != nil {
		return err
	}
	enc, err := s.Keyring.Encrypt(plain)
	if err != nil {
		return err
	}
	return WriteAtomic(s.Paths.Safe, enc, true)
}

// Create makes a new, empty safe. With no key in the directory yet it generates one, protected
// by the passphrase askPassphrase returns; otherwise the new safe uses the existing key (and
// its plugin keys), so several safes can share one passphrase.
func Create(paths Paths, ui prompt.Prompter, askPassphrase func() (string, error)) (created bool, err error) {
	if err := CheckSafeName(paths.Safe); err != nil {
		return false, err
	}
	if _, err := os.Stat(paths.Safe); err == nil {
		return false, fmt.Errorf("%s already exists", paths.Safe)
	}
	open, err := EnsureDir(paths.Dir)
	if err != nil {
		return false, err
	}
	if open {
		ui.Message("note: other users can list %s; the safe and its key are encrypted, but `chmod 700 %s` "+
			"would keep even their names private", paths.Dir, paths.Dir)
	}
	lock, err := Acquire(paths.Lock, true)
	if err != nil {
		return false, err
	}
	defer lock.Release()

	var k *Keyring
	if _, err := os.Stat(paths.IdentityPub); err == nil {
		if k, err = LoadKeyring(paths, ui, false); err != nil {
			return false, err
		}
	} else {
		if _, err := os.Stat(paths.Identity); err == nil {
			return false, fmt.Errorf("%s exists without %s; restore it or remove both", paths.Identity, paths.IdentityPub)
		}
		pass, err := askPassphrase()
		if err != nil {
			return false, err
		}
		if k, err = CreateKeyring(paths, ui, pass); err != nil {
			return false, err
		}
		created = true
	}
	s := &Safe{Paths: paths, Keyring: k, Doc: NewDocument()}
	plain, err := s.Doc.Encode()
	if err != nil {
		return created, err
	}
	enc, err := k.Encrypt(plain)
	if err != nil {
		return created, err
	}
	return created, WriteAtomic(paths.Safe, enc, false)
}

// keyedFiles lists the files encrypted to this directory's keys besides the safe itself: its
// backups, the other safes in the directory and their backups.
func (s *Safe) keyedFiles() ([]string, error) {
	others, err := s.OtherSafes()
	if err != nil {
		return nil, err
	}
	files := ExistingBackups(s.Paths.Safe)
	for _, o := range others {
		files = append(append(files, o), ExistingBackups(o)...)
	}
	return files, nil
}

// ReencryptKeyed re-encrypts the safe's backups and every other safe in its directory (and
// their backups) to the current keys. The keys belong to the directory, so after a hardware key
// is added or removed this makes the change apply to all of them: a removed key can then open
// no version of any safe here. The private key must be unlocked (passphrase). Files the key
// can't open aren't digisafe's and are skipped (returned). Call it after Save.
func (s *Safe) ReencryptKeyed() (skipped []string, err error) {
	if err := s.Keyring.Unlock(); err != nil {
		return nil, err
	}
	files, err := s.keyedFiles()
	if err != nil {
		return nil, err
	}
	for _, p := range files {
		enc, err := os.ReadFile(p)
		if err != nil {
			return skipped, err
		}
		plain, err := decrypt(enc, s.Keyring.secret)
		var noMatch *age.NoIdentityMatchError
		if errors.As(err, &noMatch) {
			skipped = append(skipped, p)
			continue
		}
		if err != nil {
			return skipped, fmt.Errorf("%s: %w", p, err)
		}
		if enc, err = s.Keyring.Encrypt(plain); err != nil {
			return skipped, err
		}
		if err := WriteAtomic(p, enc, false); err != nil {
			return skipped, fmt.Errorf("re-encrypting %s (it and the files after it still use the previous keys; "+
				"`digisafe keys rekey` re-encrypts everything): %w", p, err)
		}
	}
	return skipped, nil
}

// OtherSafes lists the other safes sharing this directory's key.
func (s *Safe) OtherSafes() ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(s.Paths.Dir, "*.age"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range matches {
		if m != s.Paths.Safe && m != s.Paths.Identity && !strings.HasPrefix(filepath.Base(m), ".") {
			out = append(out, m)
		}
	}
	return out, nil
}

// Rekey replaces the directory's key pair with a new one sealed by newPassphrase, and
// re-encrypts to it the safe, its backups, and the other safes in the directory and their
// backups (they share the key). The old key must be unlocked (passphrase) first. Plugin keys
// are kept. It returns how many files were re-encrypted, and the files skipped because the old
// key couldn't open them (not digisafe's).
//
// The new key files are written as identity.age.new / identity.pub.new first and renamed into
// place last: if it's interrupted after re-encrypting, the .new files open what was done.
func (s *Safe) Rekey(newPassphrase string) (done int, skipped []string, err error) {
	if err := s.Keyring.Unlock(); err != nil {
		return 0, nil, err
	}
	old := s.Keyring.secret
	kf, err := newKeyFiles(newPassphrase)
	if err != nil {
		return 0, nil, err
	}
	rest, err := s.keyedFiles()
	if err != nil {
		return 0, nil, err
	}
	// decrypt everything with the old key before changing anything
	var files []string
	var plains [][]byte
	for _, p := range append([]string{s.Paths.Safe}, rest...) {
		enc, err := os.ReadFile(p)
		if err != nil {
			return 0, nil, err
		}
		plain, err := decrypt(enc, old)
		var noMatch *age.NoIdentityMatchError
		if errors.As(err, &noMatch) && p != s.Paths.Safe {
			skipped = append(skipped, p)
			continue
		}
		if err != nil {
			return 0, nil, fmt.Errorf("%s: %w", p, err)
		}
		files, plains = append(files, p), append(plains, plain)
	}
	newIdentity, newPub := s.Paths.Identity+".new", s.Paths.IdentityPub+".new"
	if err := WriteAtomic(newIdentity, kf.enc, false); err != nil {
		return 0, skipped, err
	}
	if err := WriteAtomic(newPub, kf.pubText, false); err != nil {
		return 0, skipped, err
	}
	s.Keyring.secret, s.Keyring.pub = kf.secret, kf.secret.Recipient()
	for i, p := range files {
		enc, err := s.Keyring.Encrypt(plains[i])
		if err != nil {
			return i, skipped, err
		}
		if err := WriteAtomic(p, enc, false); err != nil {
			return i, skipped, fmt.Errorf("re-encrypting %s (the new key is in %s): %w", p, newIdentity, err)
		}
	}
	if err := os.Rename(newIdentity, s.Paths.Identity); err != nil {
		return len(files), skipped, err
	}
	if err := os.Rename(newPub, s.Paths.IdentityPub); err != nil {
		return len(files), skipped, err
	}
	return len(files), skipped, syncDir(s.Paths.Dir)
}
