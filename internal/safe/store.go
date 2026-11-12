package safe

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Backups is how many previous versions of the safe are kept (mysafe.age.1 is the newest).
const Backups = 5

// EnsureDir creates dir (and its parents) with mode 0700. An existing directory is left as it
// is: a safe can be created in a folder the user already has (~/Documents, a USB stick, a sync
// folder), and digisafe mustn't change who can read the rest of it. open reports whether an
// existing directory lets group or other users in (always false on Windows, which has no such
// bits), so the caller can say so.
func EnsureDir(dir string) (open bool, err error) {
	st, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, os.MkdirAll(dir, 0o700)
	}
	if err != nil {
		return false, err
	}
	if !st.IsDir() {
		return false, fmt.Errorf("%s is not a directory", dir)
	}
	return runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0, nil
}

// BackupPath is the path of backup n (1 is the newest).
func BackupPath(path string, n int) string { return fmt.Sprintf("%s.%d", path, n) }

// ExistingBackups lists the backups of path that exist, newest first.
func ExistingBackups(path string) []string {
	var out []string
	for i := 1; i <= Backups; i++ {
		if _, err := os.Stat(BackupPath(path, i)); err == nil {
			out = append(out, BackupPath(path, i))
		}
	}
	return out
}

// WriteAtomic replaces path with data so that a crash leaves either the old or the new file:
// it writes a temporary file in the same directory, fsyncs it, renames it over path and fsyncs
// the directory. The file is mode 0600. With backup, the current file is kept as path.1 first
// and older backups shift down (path.Backups is dropped).
func WriteAtomic(path string, data []byte, backup bool) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(tmp.Name())
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if backup {
		if err := rotate(path); err != nil {
			return fmt.Errorf("keeping a backup: %w", err)
		}
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	return syncDir(dir)
}

// rotate shifts path.1..path.(Backups-1) down one and keeps the current path as path.1. The
// current file stays in place (hard link, or copy where links aren't possible) until the new
// one is renamed over it.
func rotate(path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := os.Remove(BackupPath(path, Backups)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for i := Backups - 1; i >= 1; i-- {
		if err := os.Rename(BackupPath(path, i), BackupPath(path, i+1)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Link(path, BackupPath(path, 1)); err == nil {
		return nil
	}
	return copyFile(path, BackupPath(path, 1))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Lock is an advisory lock on the safe's directory: shared for reading, exclusive for writing.
// tryLock / unlock are per OS (lock_unix.go: flock; lock_windows.go: LockFileEx).
type Lock struct{ f *os.File }

// LockTimeout is how long Acquire waits for another digisafe to finish.
var LockTimeout = 10 * time.Second

// errBusy is tryLock's "someone else holds it".
var errBusy = errors.New("lock is held")

// Acquire takes the lock at path, waiting up to LockTimeout.
//
// A shared (read) lock also works where the lock file can't be created or written: on read-only
// media, or in a directory the user can read but not write. Then an existing lock file is
// locked through a read-only handle, and with none, reading goes ahead unlocked, since nobody can
// be writing there either.
func Acquire(path string, exclusive bool) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil && !exclusive && cantWrite(err) {
		if f, err = os.Open(path); errors.Is(err, os.ErrNotExist) {
			return &Lock{}, nil
		}
	}
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(LockTimeout)
	for {
		err := tryLock(f, exclusive)
		if err == nil {
			return &Lock{f: f}, nil
		}
		if !errors.Is(err, errBusy) {
			f.Close()
			return nil, fmt.Errorf("locking the safe: %w", err)
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, errors.New("the safe is in use by another digisafe; try again when it finishes")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Release drops the lock.
func (l *Lock) Release() {
	if l != nil && l.f != nil {
		unlock(l.f)
		l.f.Close()
		l.f = nil
	}
}
