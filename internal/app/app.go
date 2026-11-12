// Package app is the digisafe command line.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	"github.com/rhugga/digisafe/internal/clipboard"
	"github.com/rhugga/digisafe/internal/prompt"
	"github.com/rhugga/digisafe/internal/record"
	"github.com/rhugga/digisafe/internal/safe"
	"github.com/rhugga/digisafe/internal/tui"
)

// Version is the digisafe version. version.sh is the source of truth: `make bump-version`
// changes both, and version_test.go checks they agree.
const Version = "0.1.0"

// Clipboard is how `copy` reaches the system clipboard (replaced in tests).
type Clipboard interface {
	Copy(value string) error
	ScheduleClear(value string, after time.Duration) error
	// ClearIfUnchanged waits, then clears the clipboard if it still hashes to sum (the
	// background half of ScheduleClear).
	ClearIfUnchanged(sum string, after time.Duration) error
}

type systemClipboard struct{}

func (systemClipboard) Copy(v string) error { return clipboard.Copy(v) }
func (systemClipboard) ScheduleClear(v string, d time.Duration) error {
	return clipboard.ScheduleClear(v, d)
}
func (systemClipboard) ClearIfUnchanged(sum string, d time.Duration) error {
	return clipboard.ClearIfUnchanged(sum, d)
}

// RecordForm is the interactive record editor `record add` and `record edit` use on a
// terminal (tui.Form); without one they prompt field by field.
type RecordForm interface {
	Edit(r *record.Record, title string) error
	// Pick lets the user choose one of rs (`record edit` without an id).
	Pick(rs []*record.Record, title string) (*record.Record, error)
}

// App holds what the commands need from the outside world, so tests can replace it.
type App struct {
	UI        prompt.Prompter
	In        io.Reader // only the hidden clipboard clearer reads it (the hash ScheduleClear sends)
	Out       io.Writer // command output (records, tables, codes); prompts go to UI
	Now       func() time.Time
	Clipboard Clipboard
	Home      string     // for the default --dir
	Form      RecordForm // nil: no terminal for a form, so prompt line by line
	Styled    bool       // Out is a terminal: `record show` draws a card
}

// New returns an App for the real terminal, clipboard and clock.
func New() *App {
	home, _ := os.UserHomeDir()
	t := prompt.NewTerminal()
	t.RestoreOnInterrupt()
	a := &App{UI: t, In: os.Stdin, Out: os.Stdout, Now: time.Now, Clipboard: systemClipboard{}, Home: home,
		Styled: term.IsTerminal(int(os.Stdout.Fd()))}
	if tty := t.TTY(); tty != nil && term.IsTerminal(int(os.Stderr.Fd())) {
		a.Form = tui.Form{In: tty, Out: os.Stderr}
	}
	return a
}

// plainFlag is --plain, on the commands with a form or styled output (record add, edit and
// show). DIGISAFE_PLAIN sets it for all of them.
func plainFlag() cli.Flag {
	return &cli.BoolFlag{Name: "plain", Usage: "prompt line by line and print plain text: no form or colours",
		Sources: cli.EnvVars("DIGISAFE_PLAIN")}
}

// form is the record form, unless --plain or there's no terminal for one.
func (a *App) form(cmd *cli.Command) RecordForm {
	if cmd.Bool("plain") {
		return nil
	}
	return a.Form
}

// styled reports whether to draw styled output rather than plain text.
func (a *App) styled(cmd *cli.Command) bool { return a.Styled && !cmd.Bool("plain") }

// Run runs the CLI with args (os.Args).
func (a *App) Run(ctx context.Context, args []string) error {
	return a.Command().Run(ctx, args)
}

// Command builds the CLI.
func (a *App) Command() *cli.Command {
	return &cli.Command{
		Name:    "digisafe",
		Usage:   "keep private records in a local encrypted safe",
		Version: Version,
		Writer:  a.Out,
		Suggest: true,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "dir", Usage: "the base directory: safes, keys and the context (default $HOME/.digisafe)", Sources: cli.EnvVars("DIGISAFE_DIR")},
			&cli.StringFlag{Name: "safe", Usage: "the safe: a name in --dir (work = work.age) or a path (default: the context, else mysafe.age)", Sources: cli.EnvVars("DIGISAFE_SAFE")},
			&cli.BoolFlag{Name: "passphrase", Usage: "unlock with the passphrase, not a hardware key"},
		},
		Commands: []*cli.Command{
			a.safeCmd(),
			a.recordCmd(),
			a.keysCmd(),
			a.queryCmd(),
			a.contextCmd(),
			a.lsCmd(),
			a.clearClipboardCmd(),
		},
	}
}

// with sets a command's name and aliases (the constructors are shared by groups and shortcuts).
func with(c *cli.Command, name string, aliases ...string) *cli.Command {
	c.Name, c.Aliases = name, aliases
	return c
}

func (a *App) safeCmd() *cli.Command {
	return &cli.Command{
		Name:    "safe",
		Aliases: []string{"s"},
		Usage:   "create, list, merge, export and import safes",
		Commands: []*cli.Command{
			with(a.createCmd(), "create", "c"),
			with(a.safeListCmd(), "list", "l", "ls"),
			with(a.mergeCmd(), "merge", "m"),
			with(a.exportCmd(), "export"),
			with(a.importCmd(), "import"),
		},
	}
}

func (a *App) recordCmd() *cli.Command {
	return &cli.Command{
		Name:    "record",
		Aliases: []string{"rec", "r"},
		Usage:   "add, list, show, edit, delete and copy records",
		Commands: []*cli.Command{
			with(a.addCmd(), "add", "a"),
			with(a.listCmd(), "list", "l", "ls"),
			with(a.showCmd(), "show", "s"),
			with(a.editCmd(), "edit", "e"),
			with(a.delCmd(), "del", "d", "rm"),
			with(a.copyCmd(), "copy", "c"),
			with(a.otpCmd(), "otp", "o"),
			with(a.typesCmd(), "types"),
		},
	}
}

// lsCmd is a top-level shortcut for `record list`.
func (a *App) lsCmd() *cli.Command {
	c := with(a.listCmd(), "ls")
	c.Usage = "list the records (shortcut for `record list`)"
	return c
}

// baseDir is --dir (or DIGISAFE_DIR), else ~/.digisafe: where named safes, their keys and the
// context live.
func (a *App) baseDir(cmd *cli.Command) string {
	if dir := cmd.String("dir"); dir != "" {
		return dir
	}
	return filepath.Join(a.Home, ".digisafe")
}

// Where the safe a command uses came from.
const (
	fromFlag    = "--safe"
	fromContext = "context"
	fromDefault = "default"
)

// resolve picks the safe: --safe (or DIGISAFE_SAFE), then the context, then the default.
func (a *App) resolve(cmd *cli.Command) (safe.Paths, string, error) {
	base := a.baseDir(cmd)
	if cmd.IsSet("safe") {
		return safe.NewPaths(base, cmd.String("safe")), fromFlag, nil
	}
	ctxPath, ok, err := safe.ReadContext(base)
	if err != nil {
		return safe.Paths{}, "", fmt.Errorf("reading the context: %w", err)
	}
	if ok {
		return safe.NewPaths(base, ctxPath), fromContext, nil
	}
	return safe.NewPaths(base, ""), fromDefault, nil
}

func (a *App) paths(cmd *cli.Command) (safe.Paths, error) {
	p, _, err := a.resolve(cmd)
	return p, err
}

// open unlocks the safe; write takes the exclusive lock. The caller must Close it.
func (a *App) open(cmd *cli.Command, write bool) (*safe.Safe, error) {
	p, from, err := a.resolve(cmd)
	if err != nil {
		return nil, err
	}
	s, err := safe.Open(p, a.UI, safe.Options{PassphraseOnly: cmd.Bool("passphrase"), Write: write})
	if err != nil && from == fromContext {
		if _, statErr := os.Stat(p.Safe); statErr != nil {
			return nil, fmt.Errorf("the context points to %s, which doesn't exist: "+
				"`digisafe context set <safe>` or `digisafe context clear`", p.Safe)
		}
	}
	return s, err
}

func (a *App) now() time.Time { return a.Now().UTC().Truncate(time.Second) }

// needArgs checks a command got exactly n arguments.
func needArgs(cmd *cli.Command, n int) error {
	if cmd.NArg() != n {
		return fmt.Errorf("usage: %s %s", cmd.FullName(), cmd.ArgsUsage)
	}
	return nil
}

// errAborted is returned when the user declines a confirmation.
var errAborted = errors.New("aborted")

// clearClipboardCmd is the hidden background half of `copy`.
func (a *App) clearClipboardCmd() *cli.Command {
	return &cli.Command{
		Name:   clipboard.ClearCommand,
		Hidden: true,
		Flags:  []cli.Flag{&cli.DurationFlag{Name: "after", Value: 45 * time.Second}},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			var sum string
			if _, err := fmt.Fscanln(a.In, &sum); err != nil {
				return err
			}
			return a.Clipboard.ClearIfUnchanged(sum, cmd.Duration("after"))
		},
	}
}
