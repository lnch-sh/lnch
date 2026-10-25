// Package cli is the lnch command line.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"lnch.sh/internal/lncherr"
	"lnch.sh/internal/version"
)

type globals struct {
	JSON bool
	Root string
}

// Execute runs lnch with os.Args.
func Execute() error {
	root := NewRoot()
	root.SetArgs(defaultUp(os.Args[1:]))
	if err := root.Execute(); err != nil {
		printErr(err)
		return err
	}
	return nil
}

// NewRoot builds the command tree.
func NewRoot() *cobra.Command {
	g := &globals{}
	root := &cobra.Command{
		Use:   "lnch",
		Short: "Launch your stack. Share it instantly.",
		Long: `lnch starts the dev servers in a project, collects their logs, and can publish
a LAN or public URL. It reads lnch.toml when that file exists. Otherwise it
detects package.json, Go, Cargo, Python, Docker Compose, Procfile, Make, and just.

Run lnch with no arguments to launch the project in the current directory.`,
		Example: `  lnch
  lnch up --dry-run
  lnch share
  lnch status --json
  lnch down`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Version,
	}
	root.SetVersionTemplate("lnch {{.Version}}\n")
	root.PersistentFlags().BoolVar(&g.JSON, "json", false, "Print machine-readable JSON")
	root.PersistentFlags().StringVar(&g.Root, "root", "", "Project directory (default: current directory)")

	root.AddCommand(
		newUp(g),
		newShare(g),
		newDown(g),
		newStatus(g),
		newLogs(g),
		newRestart(g),
		newOpen(g),
		newDetect(g),
		newInit(g),
		newTrust(g),
		newMCP(g),
		newRelay(g),
		newVersion(g),
		newSupervise(g),
	)
	return root
}

func defaultUp(args []string) []string {
	if len(args) == 0 {
		return []string{"up"}
	}
	for _, a := range args {
		if a == "--" || !strings.HasPrefix(a, "-") {
			return args
		}
		name := strings.TrimLeft(strings.SplitN(a, "=", 2)[0], "-")
		switch name {
		case "help", "h", "version", "v":
			return args
		}
	}
	return append([]string{"up"}, args...)
}

func printErr(err error) {
	var e *lncherr.E
	if jsonMode() && errors.As(err, &e) {
		b, _ := json.Marshal(map[string]any{"error": e.Message, "hints": e.Hints})
		fmt.Fprintln(os.Stderr, string(b))
		return
	}
	if jsonMode() {
		b, _ := json.Marshal(map[string]string{"error": err.Error()})
		fmt.Fprintln(os.Stderr, string(b))
		return
	}
	fmt.Fprintf(os.Stderr, "Error: %s\n", err.Error())
}

func jsonMode() bool {
	for _, a := range os.Args[1:] {
		if a == "--json" || strings.HasPrefix(a, "--json=") {
			return true
		}
	}
	return false
}
