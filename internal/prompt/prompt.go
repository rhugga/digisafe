// Package prompt reads input from the user: plain lines, secrets (without echo) and yes/no
// confirmations.
package prompt

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/term"
)

// Prompter is how commands ask the user for input. Prompts and messages go to the user's
// terminal (stderr), never to stdout, so output can be piped.
type Prompter interface {
	// Line reads one line of plain input, without the trailing newline.
	Line(prompt string) (string, error)
	// Secret reads one line without echoing it. It needs a terminal.
	Secret(prompt string) (string, error)
	// Confirm asks a yes/no question; an empty answer returns def.
	Confirm(prompt string, def bool) (bool, error)
	// Message prints a line for the user.
	Message(format string, args ...any)
}

// ErrNoTerminal is returned when a secret is needed and there is no terminal to type it on.
var ErrNoTerminal = errors.New("a terminal is needed to enter secrets (they are never read from a pipe or file)")

// Terminal prompts on stderr and reads the user's terminal: stdin when it is one, otherwise the
// controlling terminal itself (/dev/tty, CONIN$ on Windows), so `digisafe record add --file -` can take
// the record on stdin and still ask for the passphrase, as ssh and gpg do.
type Terminal struct {
	in   *bufio.Reader
	file *os.File
	fd   int
	out  io.Writer
}

// NewTerminal returns a Prompter for the process's terminal and stderr.
func NewTerminal() *Terminal { return newTerminal(os.Stdin, openTTY, os.Stderr) }

// newTerminal reads from stdin if it's a terminal, else from the terminal openTTY opens; with
// no terminal at all (a pipe in CI), it reads stdin, and Secret refuses.
func newTerminal(stdin *os.File, openTTY func() (*os.File, error), out io.Writer) *Terminal {
	in := stdin
	if !term.IsTerminal(int(stdin.Fd())) {
		if tty, err := openTTY(); err == nil {
			in = tty
		}
	}
	return &Terminal{in: bufio.NewReader(in), file: in, fd: int(in.Fd()), out: out}
}

// TTY is the terminal the prompts read, or nil when there's none (input from a pipe or file).
// Full-screen UIs read it too, so they work when stdin is a pipe.
func (t *Terminal) TTY() *os.File {
	if !term.IsTerminal(t.fd) {
		return nil
	}
	return t.file
}

// RestoreOnInterrupt makes an interrupt (ctrl+c, SIGTERM) put the terminal back the way it was
// and exit with status 130. Killed while reading a passphrase, the process would otherwise leave
// the terminal with echo off. Call it once, at startup.
func (t *Terminal) RestoreOnInterrupt() {
	if !term.IsTerminal(t.fd) {
		return
	}
	state, err := term.GetState(t.fd)
	if err != nil {
		return
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		_ = term.Restore(t.fd, state)
		fmt.Fprintln(t.out)
		os.Exit(130)
	}()
}

func (t *Terminal) Line(prompt string) (string, error) {
	fmt.Fprint(t.out, prompt)
	s, err := t.in.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && s != "") {
		if errors.Is(err, io.EOF) {
			return "", fmt.Errorf("no input: %w", io.ErrUnexpectedEOF)
		}
		return "", err
	}
	return strings.TrimRight(s, "\r\n"), nil
}

func (t *Terminal) Secret(prompt string) (string, error) {
	if !term.IsTerminal(t.fd) {
		return "", ErrNoTerminal
	}
	fmt.Fprint(t.out, prompt)
	b, err := term.ReadPassword(t.fd)
	fmt.Fprintln(t.out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (t *Terminal) Confirm(prompt string, def bool) (bool, error) {
	hint := " [y/N] "
	if def {
		hint = " [Y/n] "
	}
	for {
		s, err := t.Line(prompt + hint)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Fprintln(t.out, "please answer y or n")
	}
}

func (t *Terminal) Message(format string, args ...any) {
	fmt.Fprintf(t.out, format+"\n", args...)
}
