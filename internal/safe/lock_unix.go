//go:build !windows

package safe

import (
	"errors"
	"os"
	"syscall"
)

func tryLock(f *os.File, exclusive bool) error {
	how := syscall.LOCK_SH
	if exclusive {
		how = syscall.LOCK_EX
	}
	err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errBusy
	}
	return err
}

// cantWrite reports whether err means the lock file can't be created or opened for writing:
// read-only media, or no write permission.
func cantWrite(err error) bool {
	return errors.Is(err, syscall.EROFS) || errors.Is(err, os.ErrPermission)
}

func unlock(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }

// syncDir fsyncs a directory so a rename in it is durable.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
