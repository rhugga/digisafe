package prompt

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// terminal is a Terminal reading input from a string, with no tty (fd -1).
func terminal(input string) (*Terminal, *bytes.Buffer) {
	var out bytes.Buffer
	return &Terminal{in: bufio.NewReader(strings.NewReader(input)), fd: -1, out: &out}, &out
}

func TestTerminalLine(t *testing.T) {
	tm, out := terminal("first\r\nsecond\nlast")
	for _, want := range []string{"first", "second", "last"} {
		got, err := tm.Line("> ")
		if err != nil || got != want {
			t.Fatalf("Line = %q, %v; want %q", got, err, want)
		}
	}
	if _, err := tm.Line("> "); err == nil {
		t.Error("Line at end of input succeeded")
	}
	if out.String() != "> > > > " {
		t.Errorf("prompts written = %q", out.String())
	}
}

func TestTerminalConfirm(t *testing.T) {
	tm, out := terminal("\nmaybe\nYES\nn\n")
	if ok, _ := tm.Confirm("q?", true); !ok {
		t.Error("empty answer should take the default (true)")
	}
	if ok, _ := tm.Confirm("q?", false); !ok {
		t.Error("maybe, then YES, should be true")
	}
	if !strings.Contains(out.String(), "please answer y or n") || !strings.Contains(out.String(), "[Y/n]") || !strings.Contains(out.String(), "[y/N]") {
		t.Errorf("output = %q", out.String())
	}
	if ok, _ := tm.Confirm("q?", true); ok {
		t.Error("n should be false")
	}
	if _, err := tm.Confirm("q?", true); err == nil {
		t.Error("Confirm at end of input succeeded")
	}
}

func TestTerminalSecretNeedsATerminal(t *testing.T) {
	tm, _ := terminal("hunter2\n")
	if _, err := tm.Secret("password: "); !errors.Is(err, ErrNoTerminal) {
		t.Errorf("Secret without a tty = %v, want ErrNoTerminal", err)
	}
}

func TestTerminalMessage(t *testing.T) {
	tm, out := terminal("")
	tm.Message("%d records", 3)
	if out.String() != "3 records\n" {
		t.Errorf("Message wrote %q", out.String())
	}
	if NewTerminal() == nil {
		t.Error("NewTerminal returned nil")
	}
}

func TestScript(t *testing.T) {
	s := NewScript("line", "secret", "", "y", "no")
	if v, _ := s.Line("a: "); v != "line" {
		t.Errorf("Line = %q", v)
	}
	if v, _ := s.Secret("b: "); v != "secret" {
		t.Errorf("Secret = %q", v)
	}
	if ok, _ := s.Confirm("c?", true); !ok {
		t.Error("empty confirm should take the default")
	}
	if ok, _ := s.Confirm("d?", false); !ok {
		t.Error("y should be true")
	}
	if ok, _ := s.Confirm("e?", true); ok {
		t.Error("no should be false")
	}
	if _, err := s.Line("f: "); err == nil {
		t.Error("an empty script should fail")
	}
	if _, err := s.Confirm("g?", true); err == nil {
		t.Error("an empty script should fail Confirm")
	}
	s.Push("more")
	if v, _ := s.Line("h: "); v != "more" {
		t.Errorf("after Push, Line = %q", v)
	}
	s.Message("x %s", "y")
	if len(s.Prompts) != 8 || s.Messages[0] != "x y" {
		t.Errorf("prompts %q, messages %q", s.Prompts, s.Messages)
	}
}

// With stdin a pipe (e.g. `add --file -`), prompts read the terminal instead.
func TestNewTerminalUsesTheTTYWhenStdinIsPiped(t *testing.T) {
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdinR.Close()
	stdinW.WriteString("from stdin\n")
	stdinW.Close()

	ttyR, ttyW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	ttyW.WriteString("from the terminal\n")
	ttyW.Close()

	var out bytes.Buffer
	tm := newTerminal(stdinR, func() (*os.File, error) { return ttyR, nil }, &out)
	if got, _ := tm.Line("? "); got != "from the terminal" {
		t.Errorf("with a terminal to open, Line read %q", got)
	}

	// no terminal at all: stdin is read, and secrets are refused
	tm = newTerminal(stdinR, func() (*os.File, error) { return nil, errors.New("no tty") }, &out)
	if got, _ := tm.Line("? "); got != "from stdin" {
		t.Errorf("without a terminal, Line read %q", got)
	}
	if _, err := tm.Secret("pw: "); !errors.Is(err, ErrNoTerminal) {
		t.Errorf("Secret without a terminal = %v", err)
	}
}

func TestTTYIsNilWithoutATerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	noTTY := func() (*os.File, error) { return nil, os.ErrNotExist }
	if tty := newTerminal(r, noTTY, io.Discard).TTY(); tty != nil {
		t.Errorf("TTY() = %v for a pipe", tty)
	}
}
