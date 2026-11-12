//go:build windows

package clipboard

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"github.com/atotto/clipboard"
	"golang.org/x/sys/windows"
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	openClipboard           = user32.NewProc("OpenClipboard")
	closeClipboard          = user32.NewProc("CloseClipboard")
	emptyClipboard          = user32.NewProc("EmptyClipboard")
	setClipboardData        = user32.NewProc("SetClipboardData")
	registerClipboardFormat = user32.NewProc("RegisterClipboardFormatW")
	globalAlloc             = kernel32.NewProc("GlobalAlloc")
	globalLock              = kernel32.NewProc("GlobalLock")
	globalUnlock            = kernel32.NewProc("GlobalUnlock")
	globalFree              = kernel32.NewProc("GlobalFree")
	moveMemory              = kernel32.NewProc("RtlMoveMemory")
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

// private are the clipboard formats that keep a value out of Windows' clipboard history
// (Win+V) and cloud clipboard sync, and tell clipboard monitors to ignore it. They're set
// alongside the text.
var private = []struct {
	name string
	data []byte
}{
	{"ExcludeClipboardContentFromMonitorProcessing", []byte{0}},
	{"CanIncludeInClipboardHistory", []byte{0, 0, 0, 0}}, // DWORD 0: no
	{"CanUploadToCloudClipboard", []byte{0, 0, 0, 0}},    // DWORD 0: no
}

// Copy puts value on the clipboard through the Windows clipboard API (no helper process, so the
// value never appears in a command line), marked so it isn't kept in the clipboard history or
// synced to other devices. If that fails it falls back to a plain copy.
func Copy(value string) error {
	if err := copyPrivate(value); err != nil {
		return clipboard.WriteAll(value)
	}
	return nil
}

func copyPrivate(value string) error {
	text, err := windows.UTF16FromString(value)
	if err != nil {
		return err // a NUL in the value
	}
	var opened uintptr
	for i := 0; i < 20 && opened == 0; i++ { // another program may hold it for a moment
		if opened, _, _ = openClipboard.Call(0); opened == 0 {
			time.Sleep(25 * time.Millisecond)
		}
	}
	if opened == 0 {
		return fmt.Errorf("the clipboard is in use")
	}
	defer closeClipboard.Call()
	if r, _, err := emptyClipboard.Call(); r == 0 {
		return fmt.Errorf("emptying the clipboard: %w", err)
	}
	b := unsafe.Slice((*byte)(unsafe.Pointer(&text[0])), len(text)*2)
	if err := setData(cfUnicodeText, b); err != nil {
		return err
	}
	for _, f := range private {
		name, _ := syscall.UTF16PtrFromString(f.name)
		format, _, err := registerClipboardFormat.Call(uintptr(unsafe.Pointer(name)))
		if format == 0 {
			return fmt.Errorf("registering %s: %w", f.name, err)
		}
		if err := setData(format, f.data); err != nil {
			return err
		}
	}
	return nil
}

// setData puts data on the open clipboard in format. Once SetClipboardData succeeds, the system
// owns the memory.
func setData(format uintptr, data []byte) error {
	h, _, err := globalAlloc.Call(gmemMoveable, uintptr(len(data)))
	if h == 0 {
		return fmt.Errorf("allocating clipboard memory: %w", err)
	}
	p, _, err := globalLock.Call(h)
	if p == 0 {
		globalFree.Call(h)
		return fmt.Errorf("locking clipboard memory: %w", err)
	}
	moveMemory.Call(p, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)))
	globalUnlock.Call(h)
	if r, _, err := setClipboardData.Call(format, h); r == 0 {
		globalFree.Call(h)
		return fmt.Errorf("setting clipboard data: %w", err)
	}
	return nil
}

// Read returns the clipboard's text.
func Read() (string, error) { return clipboard.ReadAll() }

// detached starts the background clearer with no console and outside the terminal's process
// group, so closing the terminal doesn't kill it.
func detached() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
}
