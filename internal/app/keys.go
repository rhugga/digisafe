package app

import (
	"context"
	"fmt"
	"strconv"
	"text/tabwriter"

	"github.com/urfave/cli/v3"

	"github.com/rhugga/digisafe/internal/safe"
)

func (a *App) createCmd() *cli.Command {
	return &cli.Command{
		Name:      "create",
		Usage:     "create a new safe (and its passphrase key, the first time)",
		ArgsUsage: "[name]",
		Description: "name is a safe in the base directory (work = work.age) or a path; without one, the\n" +
			"safe --safe or the context names, else mysafe.age. A safe's key is in its own\n" +
			"directory, so safes in one directory share a key and a passphrase.",
		Flags: []cli.Flag{&cli.BoolFlag{Name: "use", Usage: "make the new safe the current context"}},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() > 1 {
				return fmt.Errorf("usage: %s %s", cmd.FullName(), cmd.ArgsUsage)
			}
			var p safe.Paths
			if name := cmd.Args().First(); name != "" {
				p = safe.NewPaths(a.baseDir(cmd), name)
			} else {
				var err error
				if p, err = a.paths(cmd); err != nil {
					return err
				}
			}
			created, err := safe.Create(p, a.UI, func() (string, error) {
				a.UI.Message("choose a passphrase (at least %d characters); it can't be recovered if lost", safe.MinPassphrase)
				return safe.AskNewPassphrase(a.UI, "new passphrase")
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(a.Out, "created %s\n", p.Safe)
			if created {
				fmt.Fprintf(a.Out, "its key is in %s, sealed with your passphrase\n", p.Identity)
				fmt.Fprintln(a.Out, "to unlock with Touch ID or a YubiKey too, see `digisafe keys add --help`")
			} else {
				fmt.Fprintf(a.Out, "it uses the existing key in %s\n", p.Dir)
			}
			if cmd.Bool("use") {
				abs, err := safe.SetContext(a.baseDir(cmd), p.Safe)
				if err != nil {
					return err
				}
				fmt.Fprintf(a.Out, "context: %s\n", abs)
			}
			return nil
		},
	}
}

func (a *App) passwdCmd() *cli.Command {
	return &cli.Command{
		Name:  "passwd",
		Usage: "change the passphrase",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			p, err := a.paths(cmd)
			if err != nil {
				return err
			}
			lock, err := safe.Acquire(p.Lock, true)
			if err != nil {
				return err
			}
			defer lock.Release()
			k, err := safe.LoadKeyring(p, a.UI, true)
			if err != nil {
				return err
			}
			a.UI.Message("current passphrase")
			if err := k.Unlock(); err != nil {
				return err
			}
			np, err := safe.AskNewPassphrase(a.UI, "new passphrase")
			if err != nil {
				return err
			}
			if err := k.ChangePassphrase(np); err != nil {
				return err
			}
			fmt.Fprintln(a.Out, "passphrase changed")
			return nil
		},
	}
}

func (a *App) rekeyCmd() *cli.Command {
	return &cli.Command{
		Name:  "rekey",
		Usage: "replace the safe's key pair and re-encrypt the safe and its backups",
		Description: "Use this if the key file or passphrase may be compromised. It needs the current\n" +
			"passphrase and sets a new one. Hardware keys stay. Every safe in the directory shares\n" +
			"the key, so all of them, and their backups, are re-encrypted.",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			p, err := a.paths(cmd)
			if err != nil {
				return err
			}
			s, err := safe.Open(p, a.UI, safe.Options{PassphraseOnly: true, Write: true})
			if err != nil {
				return err
			}
			defer s.Close()
			np, err := safe.AskNewPassphrase(a.UI, "new passphrase")
			if err != nil {
				return err
			}
			done, skipped, err := s.Rekey(np)
			a.skipped(skipped)
			if err != nil {
				return err
			}
			fmt.Fprintf(a.Out, "new key %s; %d file(s) re-encrypted to it (the safes in %s and their backups)\n",
				s.Keyring.PublicKey(), done, s.Paths.Dir)
			return nil
		},
	}
}

func (a *App) keysCmd() *cli.Command {
	return &cli.Command{
		Name:    "keys",
		Aliases: []string{"key", "k"},
		Usage:   "the keys that unlock the safe: hardware keys, the passphrase, the key pair",
		Description: "The key pair, the passphrase and the hardware keys belong to the safe's directory:\n" +
			"every safe in it shares them, so add, remove, passwd and rekey apply to all of them.",
		Commands: []*cli.Command{
			{
				Name:  "list",
				Usage: "list the keys",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					p, err := a.paths(cmd)
					if err != nil {
						return err
					}
					k, err := safe.LoadKeyring(p, a.UI, false)
					if err != nil {
						return err
					}
					tw := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
					fmt.Fprintln(tw, "N\tKIND\tKEY")
					fmt.Fprintf(tw, "-\tpassphrase\t%s\n", k.PublicKey())
					for i, p := range k.PluginKeys() {
						fmt.Fprintf(tw, "%d\t%s\t%s\n", i+1, p.Name(), abbrev(p.String()))
					}
					return tw.Flush()
				},
			},
			{
				Name:      "add",
				Usage:     "add a hardware key from an age plugin identity file",
				ArgsUsage: "<identity-file>",
				Description: "For Touch ID (macOS):\n" +
					"  brew install age-plugin-se\n" +
					"  age-plugin-se keygen --access-control=any-biometry -o ~/.digisafe/se-key.txt\n" +
					"  digisafe keys add ~/.digisafe/se-key.txt\n" +
					"For a YubiKey (macOS, Linux, Windows): install age-plugin-yubikey, then\n" +
					"  age-plugin-yubikey --generate > yubikey-key.txt\n" +
					"  digisafe keys add yubikey-key.txt\n" +
					"Every safe in the directory (they share their keys) and its backups are re-encrypted\n" +
					"so the new key opens them; this needs the passphrase. The plugin must be on PATH.",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if err := needArgs(cmd, 1); err != nil {
						return err
					}
					ids, err := safe.ReadPluginIdentityFile(cmd.Args().First())
					if err != nil {
						return err
					}
					p, err := a.paths(cmd)
					if err != nil {
						return err
					}
					// the passphrase, not a hardware key: the other safes in the directory are
					// re-encrypted too, and that needs the private key
					s, err := safe.Open(p, a.UI, safe.Options{PassphraseOnly: true, Write: true})
					if err != nil {
						return err
					}
					defer s.Close()
					if err := s.Keyring.AddPluginKeys(ids); err != nil {
						return err
					}
					if err := s.Save(); err != nil {
						return err
					}
					skipped, err := s.ReencryptKeyed()
					a.skipped(skipped)
					if err != nil {
						return err
					}
					fmt.Fprintf(a.Out, "added %d key(s); every safe in %s now opens with them or the passphrase\n", len(ids), s.Paths.Dir)
					return nil
				},
			},
			{
				Name:      "remove",
				Usage:     "remove hardware key n (see keys list)",
				ArgsUsage: "<n>",
				Description: "Every safe in the directory (they share their keys) and its backups are\n" +
					"re-encrypted without the key, so it can't open any version of them. This needs\n" +
					"the passphrase.",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if err := needArgs(cmd, 1); err != nil {
						return err
					}
					n, err := strconv.Atoi(cmd.Args().First())
					if err != nil {
						return fmt.Errorf("key number must be a number, got %q", cmd.Args().First())
					}
					p, err := a.paths(cmd)
					if err != nil {
						return err
					}
					s, err := safe.Open(p, a.UI, safe.Options{PassphraseOnly: true, Write: true})
					if err != nil {
						return err
					}
					defer s.Close()
					if err := s.Keyring.RemovePluginKey(n - 1); err != nil {
						return err
					}
					if err := s.Save(); err != nil {
						return err
					}
					// after Save, which keeps the version the removed key could open as a backup
					skipped, err := s.ReencryptKeyed()
					a.skipped(skipped)
					if err != nil {
						return err
					}
					fmt.Fprintf(a.Out, "removed key %d; no safe in %s opens with it any more\n", n, s.Paths.Dir)
					return nil
				},
			},
			a.passwdCmd(),
			a.rekeyCmd(),
		},
	}
}

// skipped notes the files that look like safes but aren't encrypted to the directory's key.
func (a *App) skipped(files []string) {
	for _, f := range files {
		a.UI.Message("skipped %s: not encrypted to this directory's key", f)
	}
}

func abbrev(s string) string {
	if len(s) > 40 {
		return s[:24] + "..." + s[len(s)-8:]
	}
	return s
}
