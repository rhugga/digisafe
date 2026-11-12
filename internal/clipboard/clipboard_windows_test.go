//go:build windows

package clipboard

import (
	"syscall"
	"testing"
	"unsafe"
)

// On a desktop (CI's Windows runners have one), Copy puts the text and the private formats on
// the clipboard. Skipped where there's no clipboard to use.
func TestCopyIsPrivate(t *testing.T) {
	saved, _ := Read()
	t.Cleanup(func() { _ = Copy(saved) })
	if err := copyPrivate("digisafe test value"); err != nil {
		t.Skipf("no usable clipboard: %v", err)
	}
	if got, err := Read(); err != nil || got != "digisafe test value" {
		t.Errorf("Read = %q, %v", got, err)
	}
	isAvailable := user32.NewProc("IsClipboardFormatAvailable")
	for _, f := range private {
		name, _ := syscall.UTF16PtrFromString(f.name)
		format, _, _ := registerClipboardFormat.Call(uintptr(unsafe.Pointer(name)))
		if ok, _, _ := isAvailable.Call(format); ok == 0 {
			t.Errorf("%s isn't on the clipboard", f.name)
		}
	}
}
