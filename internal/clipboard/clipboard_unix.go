//go:build !windows

package clipboard

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
)

type tool struct{ copy, paste []string }

// goos picks the tool set; a variable so the tests can check Linux's on macOS.
var goos = runtime.GOOS

// macCopy sets the pasteboard to the text on stdin for this Mac only (prepareForNewContents
// with NSPasteboardContentsCurrentHostOnly: it isn't handed to other devices through Universal
// Clipboard), marked org.nspasteboard.ConcealedType so clipboard managers that follow the
// convention don't keep it. pbcopy can do neither; it's the fallback.
const macCopy = `ObjC.import('AppKit');
var data = $.NSFileHandle.fileHandleWithStandardInput.readDataToEndOfFile;
var text = $.NSString.alloc.initWithDataEncoding(data, $.NSUTF8StringEncoding);
var pb = $.NSPasteboard.generalPasteboard;
pb.prepareForNewContentsWithOptions(1);
pb.setStringForType(text, $.NSPasteboardTypeString);
pb.setStringForType($(''), 'org.nspasteboard.ConcealedType');
''`

func tools() ([]tool, error) {
	if goos == "darwin" {
		return []tool{
			{[]string{"osascript", "-l", "JavaScript", "-e", macCopy}, []string{"pbpaste"}},
			{[]string{"pbcopy"}, []string{"pbpaste"}},
		}, nil
	}
	var ts []tool
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		ts = append(ts, tool{[]string{"wl-copy"}, []string{"wl-paste", "--no-newline"}})
	}
	ts = append(ts,
		tool{[]string{"xclip", "-selection", "clipboard"}, []string{"xclip", "-selection", "clipboard", "-o"}},
		tool{[]string{"xsel", "--clipboard", "--input"}, []string{"xsel", "--clipboard", "--output"}})
	return ts, nil
}

// available lists the tools that are installed, in order of preference.
func available() ([]tool, error) {
	ts, _ := tools()
	var out []tool
	for _, t := range ts {
		if _, err := exec.LookPath(t.copy[0]); err == nil {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no clipboard tool found (pbcopy, wl-copy, xclip or xsel)")
	}
	return out, nil
}

// Copy puts value on the clipboard. The value goes to the tool on stdin, never as an argument.
//
// The tool's stdout and stderr go to /dev/null, not to pipes: xclip and wl-copy leave a child
// running to serve the clipboard, and it inherits them, so waiting for a pipe to close would
// hang until something else is copied.
// Each installed tool is tried in turn until one works.
func Copy(value string) error {
	ts, err := available()
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range ts {
		cmd := exec.Command(t.copy[0], t.copy[1:]...)
		cmd.Stdin = strings.NewReader(value)
		if err := cmd.Run(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", t.copy[0], err))
			continue
		}
		return nil
	}
	return errors.Join(errs...)
}

// Read returns the clipboard's contents.
func Read() (string, error) {
	ts, err := available()
	if err != nil {
		return "", err
	}
	t := ts[0]
	out, err := exec.Command(t.paste[0], t.paste[1:]...).Output()
	if err != nil {
		return "", fmt.Errorf("%s: %w", t.paste[0], err)
	}
	return string(out), nil
}

// detached starts the background clearer in its own session, so it outlives the terminal.
func detached() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
