package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestHelpExamples(t *testing.T) {
	root := NewRoot()
	var missing []string
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		switch cmd.Name() {
		case "help", "completion":
		default:
			if !cmd.Hidden && !strings.Contains(cmd.Example, "lnch") {
				missing = append(missing, cmd.CommandPath())
			}
		}
		for _, c := range cmd.Commands() {
			walk(c)
		}
	}
	walk(root)
	if len(missing) > 0 {
		t.Fatalf("commands missing examples: %s", strings.Join(missing, ", "))
	}

	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"up", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "lnch up --dry-run") {
		t.Fatalf("up help:\n%s", buf.String())
	}
}

func TestDetectJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Procfile"), []byte("web: echo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := NewRoot()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"detect", "--root", dir, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "echo hi") {
		t.Fatalf("detect output:\n%s", buf.String())
	}
}

func TestDefaultUpArgs(t *testing.T) {
	if got := strings.Join(defaultUp(nil), " "); got != "up" {
		t.Fatal(got)
	}
	if got := strings.Join(defaultUp([]string{"--dry-run"}), " "); got != "up --dry-run" {
		t.Fatal(got)
	}
	if got := strings.Join(defaultUp([]string{"status", "--json"}), " "); got != "status --json" {
		t.Fatal(got)
	}
	if got := strings.Join(defaultUp([]string{"--help"}), " "); got != "--help" {
		t.Fatal(got)
	}
}
