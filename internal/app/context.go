package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/urfave/cli/v3"

	"github.com/rhugga/digisafe/internal/safe"
)

func (a *App) contextCmd() *cli.Command {
	return &cli.Command{
		Name:    "context",
		Aliases: []string{"ctx"},
		Usage:   "the current safe, which every command uses unless --safe says otherwise",
		Description: "Order: --safe (or DIGISAFE_SAFE), then the context, then mysafe.age in the base\n" +
			"directory. The context is the file `context` in the base directory (--dir).",
		Commands: []*cli.Command{
			{
				Name:  "show",
				Usage: "show the current safe and where that choice comes from",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					p, from, err := a.resolve(cmd)
					if err != nil {
						return err
					}
					state := ""
					if _, err := os.Stat(p.Safe); err != nil {
						state = " (doesn't exist yet)"
					}
					switch from {
					case fromContext:
						fmt.Fprintf(a.Out, "context: %s%s\n", p.Safe, state)
					case fromFlag:
						fmt.Fprintf(a.Out, "--safe: %s%s (overrides the context for this command)\n", p.Safe, state)
					default:
						fmt.Fprintf(a.Out, "no context: using the default %s%s\n", p.Safe, state)
					}
					return nil
				},
			},
			{
				Name:      "set",
				Usage:     "make a safe the current one",
				ArgsUsage: "<safe>",
				Description: "safe is a name in the base directory (work = work.age) or a path. It must exist;\n" +
					"`digisafe safe create <name> --use` creates one and sets it.",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if err := needArgs(cmd, 1); err != nil {
						return err
					}
					base := a.baseDir(cmd)
					abs, err := safe.SetContext(base, safe.SafePath(base, cmd.Args().First()))
					if err != nil {
						return err
					}
					fmt.Fprintf(a.Out, "context: %s\n", abs)
					return nil
				},
			},
			{
				Name:  "clear",
				Usage: "clear the context (back to the default safe)",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					base := a.baseDir(cmd)
					had, err := safe.ClearContext(base)
					if err != nil {
						return err
					}
					def := safe.SafePath(base, "")
					if had {
						fmt.Fprintf(a.Out, "context cleared; using the default %s\n", def)
					} else {
						fmt.Fprintf(a.Out, "no context was set; using the default %s\n", def)
					}
					return nil
				},
			},
		},
	}
}

// safeListCmd lists the safes in the base directory, marking the one in use.
func (a *App) safeListCmd() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "list the safes in the base directory (* marks the current one)",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			base := a.baseDir(cmd)
			cur, from, err := a.resolve(cmd)
			if err != nil {
				return err
			}
			safes, err := safe.ListSafes(base)
			if err != nil {
				return err
			}
			listed := map[string]bool{}
			for _, s := range safes {
				listed[s] = true
			}
			if !listed[cur.Safe] {
				if _, err := os.Stat(cur.Safe); err == nil {
					safes = append(safes, cur.Safe) // the current safe lives elsewhere
				}
			}
			if len(safes) == 0 {
				a.UI.Message("no safes in %s: `digisafe safe create`", base)
				return nil
			}
			tw := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "\tNAME\tPATH")
			for _, s := range safes {
				mark := ""
				if s == cur.Safe {
					mark = "*"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", mark, filepath.Base(s), s)
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			if from == fromDefault {
				a.UI.Message("no context set: using the default (`digisafe context set <safe>` to change it)")
			}
			return nil
		},
	}
}
