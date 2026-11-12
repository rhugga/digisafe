// Package clipboard copies values to the system clipboard and clears them again later.
package clipboard

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// ClearCommand is the hidden digisafe subcommand that ScheduleClear runs in the background.
const ClearCommand = "_clear-clipboard"

// Sum is the hash ScheduleClear compares the clipboard against.
func Sum(value string) string {
	s := sha256.Sum256([]byte(value))
	return hex.EncodeToString(s[:])
}

// ScheduleClear starts a detached `digisafe _clear-clipboard` that clears the clipboard after
// the delay, unless it no longer holds value. The value's hash goes to it on stdin, so it never
// appears in the process list.
func ScheduleClear(value string, after time.Duration) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, ClearCommand, "--after", after.String())
	cmd.SysProcAttr = detached()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	_, werr := fmt.Fprintln(stdin, Sum(value))
	stdin.Close()
	if werr != nil {
		return werr
	}
	return cmd.Process.Release()
}

// ClearIfUnchanged waits, then clears the clipboard if it still hashes to sum.
func ClearIfUnchanged(sum string, after time.Duration) error {
	time.Sleep(after)
	cur, err := Read()
	if err != nil {
		return err
	}
	if Sum(cur) != sum {
		return nil // something else was copied since; leave it
	}
	return Copy("")
}
