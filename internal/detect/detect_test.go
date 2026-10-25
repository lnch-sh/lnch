package detect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commands(p *testing.T, root string) map[string]string {
	t := p
	plan, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, s := range plan.Services {
		out[s.Name] = s.Command
	}
	return out
}

func TestNodePackageManager(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"name":"@acme/web","scripts":{"dev":"vite"}}`)
	write(t, dir, "pnpm-lock.yaml", "lockfileVersion: 9\n")
	got := commands(t, dir)
	if got["acme-web"] != "pnpm run dev" {
		t.Fatalf("%v", got)
	}
}

func TestMetaRunnerSkipsNestedNode(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"name":"mono","scripts":{"dev":"turbo run dev"}}`)
	write(t, dir, "apps/web/package.json", `{"name":"web","scripts":{"dev":"vite"}}`)
	got := commands(t, dir)
	if len(got) != 1 || got["mono"] != "npm run dev" {
		t.Fatalf("%v", got)
	}
}

func TestWorkspaces(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"private":true,"workspaces":["apps/*"]}`)
	write(t, dir, "apps/web/package.json", `{"name":"web","scripts":{"dev":"vite"}}`)
	write(t, dir, "apps/api/package.json", `{"name":"api","scripts":{"start":"node server.js"}}`)
	got := commands(t, dir)
	if got["web"] != "npm run dev" || got["api"] != "npm run start" {
		t.Fatalf("%v", got)
	}
	plan, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Share.Service != "web" {
		t.Fatalf("primary %s", plan.Share.Service)
	}
	for _, s := range plan.Services {
		if s.Name == "web" && len(s.DependsOn) == 1 && s.DependsOn[0] == "api" {
			return
		}
	}
	t.Fatal("web should depend on api")
}

func TestGoCommands(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/demo\n\ngo 1.22\n")
	write(t, dir, "main.go", "package main\nfunc main() {}\n")
	write(t, dir, "cmd/api/main.go", "package main\nfunc main() {}\n")
	write(t, dir, "cmd/worker/main.go", "package main\nfunc main() {}\n")
	got := commands(t, dir)
	if got["api"] != "go run ./cmd/api" || got["worker"] != "go run ./cmd/worker" {
		t.Fatalf("%v", got)
	}
	if _, ok := got["demo"]; ok {
		t.Fatal("root main should not be used when cmd/ exists")
	}
}

func TestGoWorkDeep(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.work", "go 1.22\n\nuse (\n\t./a/b/c/api\n)\n")
	write(t, dir, "a/b/c/api/go.mod", "module example.com/api\n\ngo 1.22\n")
	write(t, dir, "a/b/c/api/main.go", "package main\nfunc main() {}\n")
	got := commands(t, dir)
	if got["api"] != "go run ." {
		t.Fatalf("%v", got)
	}
}

func TestPythonFastAPI(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "requirements.txt", "fastapi\nuvicorn\n")
	write(t, dir, "main.py", "from fastapi import FastAPI\napp = FastAPI()\n")
	got := commands(t, dir)
	found := false
	for _, cmd := range got {
		if strings.Contains(cmd, "uvicorn main:app") && strings.Contains(cmd, "${port}") {
			found = true
		}
	}
	if !found {
		t.Fatalf("%v", got)
	}
}

func TestProcfileWins(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"scripts":{"dev":"vite"}}`)
	write(t, dir, "Procfile", "web: npm run dev\nworker: python worker.py\n")
	got := commands(t, dir)
	if got["web"] != "npm run dev" || got["worker"] != "python worker.py" {
		t.Fatalf("%v", got)
	}
	if _, ok := got["app"]; ok {
		t.Fatal("package.json should not be merged with Procfile")
	}
}

func TestConfigWins(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"scripts":{"dev":"vite"}}`)
	write(t, dir, "lnch.toml", "name = \"custom\"\n\n[[services]]\nname = \"web\"\ncommand = \"echo hi\"\n")
	plan, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Source != "lnch.toml" || plan.Services[0].Command != "echo hi" {
		t.Fatalf("%+v", plan)
	}
}

func TestMakeFallback(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Makefile", "dev:\n\techo hi\n")
	got := commands(t, dir)
	if got["dev"] != "make dev" {
		t.Fatalf("%v", got)
	}
}

func TestJustFallback(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "justfile", "dev:\n    echo hi\n")
	got := commands(t, dir)
	if got["dev"] != "just dev" {
		t.Fatalf("%v", got)
	}
}

func TestCargoAndCompose(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Cargo.toml", "[package]\nname = \"api\"\nversion = \"0.1.0\"\n")
	write(t, dir, "src/main.rs", "fn main() {}\n")
	write(t, dir, "compose.yaml", "services:\n  db:\n    image: postgres\n")
	got := commands(t, dir)
	if got["api"] != "cargo run" || got["compose"] != "docker compose up" {
		t.Fatalf("%v", got)
	}
}

func TestEmpty(t *testing.T) {
	dir := t.TempDir()
	_, err := Detect(dir)
	if err != ErrNone {
		t.Fatalf("got %v", err)
	}
}
