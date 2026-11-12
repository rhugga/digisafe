//go:build !windows

package prompt

import "os"

// openTTY opens the process's controlling terminal.
func openTTY() (*os.File, error) { return os.OpenFile("/dev/tty", os.O_RDWR, 0) }
