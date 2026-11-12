//go:build windows

package safe

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLock locks the whole file with LockFileEx, without waiting.
func tryLock(f *os.File, exclusive bool) error {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, ^uint32(0), ^uint32(0), &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return errBusy
	}
	return err
}

// cantWrite reports whether err means the lock file can't be created or opened for writing:
// write-protected media, or no write permission.
func cantWrite(err error) bool {
	return errors.Is(err, windows.ERROR_WRITE_PROTECT) || errors.Is(err, os.ErrPermission)
}

func unlock(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, ^uint32(0), ^uint32(0), &windows.Overlapped{})
}

// syncDir is a no-op: Windows can't open a directory to fsync it. WriteAtomic has already
// fsynced the new file itself before renaming it into place.
func syncDir(string) error { return nil }
