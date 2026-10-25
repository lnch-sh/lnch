package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lnch.sh/internal/plan"
)

func TestExampleAndRoundTrip(t *testing.T) {
	example := filepath.Join("..", "..", "examples", "lnch.toml")
	p, err := Load(example)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "shop" || len(p.Services) != 3 {
		t.Fatalf("plan %+v", p)
	}
	if p.Services[1].Env["DATABASE_URL"] == "" {
		t.Fatal("missing env")
	}
	if !p.Services[2].Primary || !p.Services[2].Public {
		t.Fatal("web should be primary and public")
	}
	text := Format(p)
	dir := t.TempDir()
	path := filepath.Join(dir, "lnch.toml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.Services[0].Command != p.Services[0].Command || again.Services[1].DependsOn[0] != "db" {
		t.Fatalf("round trip %+v", again)
	}
}

func TestCycle(t *testing.T) {
	dir := t.TempDir()
	body := `
name = "loop"

[[services]]
name = "a"
command = "echo a"
depends_on = ["b"]

[[services]]
name = "b"
command = "echo b"
depends_on = ["a"]
`
	path := filepath.Join(dir, "lnch.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle, got %v", err)
	}
}

func TestFormatEnv(t *testing.T) {
	text := Format(&plan.Plan{
		Name: "app",
		Root: t.TempDir(),
		Services: []plan.Service{{
			Name:    "api",
			Command: "go run .",
			Env:     map[string]string{"PORT": "8080"},
		}},
	})
	if !strings.Contains(text, `env.PORT = "8080"`) {
		t.Fatalf("format:\n%s", text)
	}
}
