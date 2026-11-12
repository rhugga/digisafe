package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v3"

	"github.com/rhugga/digisafe/internal/record"
	"github.com/rhugga/digisafe/internal/safe"
)

func (a *App) mergeCmd() *cli.Command {
	return &cli.Command{
		Name:      "merge",
		Usage:     "merge another safe's records into this one; for a record in both, the newest version wins",
		ArgsUsage: "<other safe>",
		Description: "Copies into the current safe (--safe, else the context) the records of the other safe (a\n" +
			"name in the base directory, or a path). The other safe isn't changed. Records are matched by\n" +
			"id: one only in the other safe is added; for one in both, the version updated most recently\n" +
			"is kept. A login keeps the losing version's secrets in its history. Run it the other way\n" +
			"round too to make both safes the same.\n\n" +
			"It's a union: a record deleted from one safe comes back if the other still has it, and the\n" +
			"same login added separately to each (two ids) ends up twice.",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "dry-run", Usage: "show what would change, and change nothing"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if err := needArgs(cmd, 1); err != nil {
				return err
			}
			dryRun := cmd.Bool("dry-run")
			target, err := a.paths(cmd)
			if err != nil {
				return err
			}
			other := safe.NewPaths(a.baseDir(cmd), cmd.Args().First())
			if same, err := sameFile(target.Safe, other.Safe); err != nil {
				return err
			} else if same {
				return fmt.Errorf("%s is the current safe: merge another safe into it", other.Safe)
			}
			if _, err := os.Stat(other.Safe); err != nil {
				return fmt.Errorf("no safe at %s", other.Safe)
			}
			s, err := a.open(cmd, !dryRun)
			if err != nil {
				return err
			}
			defer s.Close()
			in, err := a.readOther(cmd, s, other)
			if err != nil {
				return err
			}
			res, err := s.Doc.Merge(in.Records)
			if err != nil {
				return fmt.Errorf("merging %s: %w", other.Safe, err)
			}
			a.mergeReport(res, filepath.Base(other.Safe))
			verb := "merged"
			switch {
			case dryRun:
				verb = "would merge"
			case res.Changed:
				if err := s.Save(); err != nil {
					return err
				}
			}
			fmt.Fprintf(a.Out, "%s %s into %s: %d added, %d updated from it, %d kept (newer here), %d the same\n",
				verb, other.Safe, s.Paths.Safe, len(res.Added), len(res.Updated), len(res.Kept)+len(res.Conflicts), res.Same)
			return nil
		},
	}
}

// readOther reads the safe to merge from. In the current safe's directory it shares the keys and
// the lock already held; elsewhere it's opened (and unlocked) on its own.
func (a *App) readOther(cmd *cli.Command, s *safe.Safe, other safe.Paths) (*safe.Document, error) {
	if same, err := sameFile(other.Dir, s.Paths.Dir); err != nil {
		return nil, err
	} else if same {
		return s.ReadSibling(other.Safe)
	}
	o, err := safe.Open(other, a.UI, safe.Options{PassphraseOnly: cmd.Bool("passphrase")})
	if err != nil {
		return nil, err
	}
	defer o.Close()
	return o.Doc, nil
}

// mergeReport lists, on stderr, what a merge does to each record that isn't the same in both.
func (a *App) mergeReport(res safe.MergeResult, from string) {
	line := func(what string, r *record.Record) {
		a.UI.Message("  %-8s %s  %-12s %s", what, shortID(r.ID), r.Type, record.Printable(r.Name))
	}
	for _, r := range res.Added {
		line("add", r)
	}
	for _, r := range res.Updated {
		line("update", r)
	}
	for _, r := range res.Conflicts {
		line("conflict", r)
	}
	if len(res.Conflicts) > 0 {
		a.UI.Message("conflict: updated at the same time in both safes but different; this safe's version was kept "+
			"(compare with `digisafe --safe %s record show <id>`)", from)
	}
}

// sameFile reports whether a and b are the same file or directory (paths may differ: relative,
// symlinks).
func sameFile(a, b string) (bool, error) {
	sa, err := os.Stat(a)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	sb, err := os.Stat(b)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return os.SameFile(sa, sb), nil
}
