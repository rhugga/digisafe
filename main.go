// digisafe keeps private records in a local encrypted safe. See digisafe.md.
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/rhugga/digisafe/internal/app"
)

func main() {
	os.Exit(run(context.Background(), app.New(), os.Args, os.Stderr))
}

// run runs the CLI and returns the exit code: 0, or 1 for an error. (An interrupt exits with 130
// from prompt.Terminal.RestoreOnInterrupt, after restoring the terminal.)
func run(ctx context.Context, a *app.App, args []string, stderr io.Writer) int {
	if err := a.Run(ctx, args); err != nil {
		fmt.Fprintf(stderr, "digisafe: %v\n", err)
		return 1
	}
	return 0
}
