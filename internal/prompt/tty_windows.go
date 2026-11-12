//go:build windows

package prompt

import "os"

// openTTY opens the console's input buffer (the Windows equivalent of /dev/tty).
func openTTY() (*os.File, error) { return os.OpenFile("CONIN$", os.O_RDWR, 0) }
