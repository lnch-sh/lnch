package detect

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"lnch.sh/internal/plan"
)

func detectGo(dir string) []plan.Service {
	if !exists(dir, "go.mod") {
		return nil
	}
	if cmds := goCommands(dir); len(cmds) > 0 {
		var svcs []plan.Service
		for _, c := range cmds {
			svcs = append(svcs, plan.Service{
				Name:     sanitize(c),
				Command:  "go run ./cmd/" + filepath.ToSlash(c),
				Dir:      dir,
				Protocol: "http",
				Source:   "go.mod",
			})
		}
		return svcs
	}
	if !goHasMain(dir) {
		return nil
	}
	name := goModName(dir)
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if name == "" {
		name = filepath.Base(dir)
	}
	return []plan.Service{{
		Name:     sanitize(name),
		Command:  "go run .",
		Dir:      dir,
		Protocol: "http",
		Source:   "go.mod",
	}}
}

func goCommands(dir string) []string {
	entries, err := os.ReadDir(filepath.Join(dir, "cmd"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && goHasMain(filepath.Join(dir, "cmd", e.Name())) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func goHasMain(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		s, err := readLimit(filepath.Join(dir, e.Name()), 256*1024)
		if err != nil {
			continue
		}
		if strings.Contains(s, "package main") && strings.Contains(s, "func main(") {
			return true
		}
	}
	return false
}

func goModName(dir string) string {
	s, err := readLimit(filepath.Join(dir, "go.mod"), 64*1024)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "module" {
			return f[1]
		}
	}
	return ""
}

func goWorkDirs(root string) []string {
	s, err := readLimit(filepath.Join(root, "go.work"), 64*1024)
	if err != nil {
		return nil
	}
	var dirs []string
	in := false
	for _, line := range strings.Split(s, "\n") {
		trim := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if trim == "" || strings.HasPrefix(trim, "//") {
			continue
		}
		if strings.HasPrefix(trim, "use (") {
			in = true
			continue
		}
		if in && trim == ")" {
			in = false
			continue
		}
		var p string
		switch {
		case strings.HasPrefix(trim, "use "):
			p = strings.TrimSpace(strings.TrimPrefix(trim, "use "))
		case in:
			p = trim
		default:
			continue
		}
		p = strings.Trim(p, `"'`)
		if p == "" || p == "(" || p == ")" {
			continue
		}
		dirs = append(dirs, filepath.Join(root, filepath.FromSlash(p)))
	}
	return dirs
}

func detectCargo(dir string) ([]plan.Service, []string) {
	text, err := readLimit(filepath.Join(dir, "Cargo.toml"), 256*1024)
	if err != nil {
		return nil, nil
	}
	if members := cargoMembers(text); len(members) > 0 {
		var svcs []plan.Service
		var claimed []string
		for _, m := range members {
			for _, md := range expandMember(dir, m) {
				claimed = append(claimed, md)
				if cargoHasBin(md) {
					svcs = append(svcs, plan.Service{
						Name:     sanitize(filepath.Base(md)),
						Command:  "cargo run",
						Dir:      md,
						Protocol: "http",
						Source:   "cargo",
					})
				}
			}
		}
		if strings.Contains(text, "[package]") && cargoHasBin(dir) {
			name := cargoName(text)
			if name == "" {
				name = filepath.Base(dir)
			}
			svcs = append(svcs, plan.Service{
				Name:     sanitize(name),
				Command:  "cargo run",
				Dir:      dir,
				Protocol: "http",
				Source:   "cargo",
			})
		}
		claimed = append(claimed, dir)
		return svcs, claimed
	}
	if !cargoHasBin(dir) {
		return nil, []string{dir}
	}
	name := cargoName(text)
	if name == "" {
		name = filepath.Base(dir)
	}
	return []plan.Service{{
		Name:     sanitize(name),
		Command:  "cargo run",
		Dir:      dir,
		Protocol: "http",
		Source:   "cargo",
	}}, []string{dir}
}

func expandMember(dir, member string) []string {
	if strings.Contains(member, "*") {
		return glob(dir, member)
	}
	return []string{filepath.Join(dir, filepath.FromSlash(member))}
}

func cargoMembers(text string) []string {
	if !strings.Contains(text, "[workspace]") {
		return nil
	}
	re := regexp.MustCompile(`(?s)members\s*=\s*\[(.*?)\]`)
	m := re.FindStringSubmatch(text)
	if len(m) < 2 {
		return nil
	}
	item := regexp.MustCompile(`"([^"]+)"`)
	var out []string
	for _, mm := range item.FindAllStringSubmatch(m[1], -1) {
		out = append(out, mm[1])
	}
	return out
}

func cargoHasBin(dir string) bool {
	if exists(dir, filepath.Join("src", "main.rs")) {
		return true
	}
	text, err := readLimit(filepath.Join(dir, "Cargo.toml"), 256*1024)
	return err == nil && strings.Contains(text, "[[bin]]")
}

func cargoName(text string) string {
	i := strings.Index(text, "[package]")
	if i < 0 {
		return ""
	}
	rest := text[i:]
	if j := strings.Index(rest[1:], "\n["); j >= 0 {
		rest = rest[:j+1]
	}
	re := regexp.MustCompile(`(?m)^name\s*=\s*"([^"]+)"`)
	if m := re.FindStringSubmatch(rest); len(m) == 2 {
		return m[1]
	}
	return ""
}

func detectPython(dir string) []plan.Service {
	if !pythonProject(dir) && !exists(dir, "manage.py") {
		return nil
	}
	name := sanitize(filepath.Base(dir))
	if exists(dir, "manage.py") {
		return []plan.Service{{
			Name:     name,
			Command:  djangoCmd(dir),
			Dir:      dir,
			Port:     8000,
			Protocol: "http",
			Source:   "python",
		}}
	}
	if file, src, ok := findMarker(dir, "FastAPI("); ok {
		app := assignName(src, "FastAPI(")
		mod := moduleFromFile(dir, file)
		return []plan.Service{{
			Name:     name,
			Command:  uvicornCmd(dir, mod, app),
			Dir:      dir,
			Port:     8000,
			Protocol: "http",
			Source:   "python",
		}}
	}
	if file, src, ok := findMarker(dir, "Flask("); ok {
		app := assignName(src, "Flask(")
		mod := moduleFromFile(dir, file)
		return []plan.Service{{
			Name:     name,
			Command:  flaskCmd(dir, mod, app),
			Dir:      dir,
			Port:     5000,
			Protocol: "http",
			Source:   "python",
		}}
	}
	return nil
}

func pythonProject(dir string) bool {
	return exists(dir, "pyproject.toml") || exists(dir, "requirements.txt") || exists(dir, "Pipfile")
}

func findMarker(dir, marker string) (string, string, bool) {
	candidates := []string{
		"main.py", "app.py", "wsgi.py",
		filepath.Join("app", "main.py"),
		filepath.Join("src", "main.py"),
		filepath.Join("src", "app.py"),
	}
	for _, c := range candidates {
		path := filepath.Join(dir, c)
		s, err := readLimit(path, 256*1024)
		if err == nil && strings.Contains(s, marker) {
			return path, s, true
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".py") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		s, err := readLimit(path, 128*1024)
		if err == nil && strings.Contains(s, marker) {
			return path, s, true
		}
	}
	return "", "", false
}

func assignName(src, ctor string) string {
	re := regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*` + regexp.QuoteMeta(ctor))
	if m := re.FindStringSubmatch(src); len(m) == 2 {
		return m[1]
	}
	return "app"
}

func moduleFromFile(dir, file string) string {
	rel, err := filepath.Rel(dir, file)
	if err != nil {
		rel = filepath.Base(file)
	}
	rel = strings.TrimSuffix(filepath.ToSlash(rel), ".py")
	parts := strings.Split(rel, "/")
	if len(parts) > 1 && parts[0] == "src" && !exists(dir, filepath.Join("src", "__init__.py")) {
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return "app"
	}
	return strings.Join(parts, ".")
}

func pyUV(dir string) bool {
	if exists(dir, "uv.lock") {
		return true
	}
	s, err := readLimit(filepath.Join(dir, "pyproject.toml"), 128*1024)
	return err == nil && strings.Contains(s, "[tool.uv]")
}

func pyBin(dir string) (string, bool) {
	if pyUV(dir) {
		return "uv run", true
	}
	if runtime.GOOS != "windows" {
		return "python3", false
	}
	return "python", false
}

func uvicornCmd(dir, mod, app string) string {
	bin, uv := pyBin(dir)
	if uv {
		return bin + " uvicorn " + mod + ":" + app + " --reload --host 127.0.0.1 --port ${port}"
	}
	return bin + " -m uvicorn " + mod + ":" + app + " --reload --host 127.0.0.1 --port ${port}"
}

func flaskCmd(dir, mod, app string) string {
	bin, uv := pyBin(dir)
	if uv {
		return bin + " flask --app " + mod + ":" + app + " run --host 127.0.0.1 --port ${port}"
	}
	return bin + " -m flask --app " + mod + ":" + app + " run --host 127.0.0.1 --port ${port}"
}

func djangoCmd(dir string) string {
	bin, uv := pyBin(dir)
	if uv {
		return bin + " python manage.py runserver 127.0.0.1:${port}"
	}
	return bin + " manage.py runserver 127.0.0.1:${port}"
}

func detectCompose(dir string) []plan.Service {
	for _, name := range []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"} {
		text, err := readLimit(filepath.Join(dir, name), 256*1024)
		if err != nil {
			continue
		}
		if !strings.Contains(text, "services:") {
			continue
		}
		return []plan.Service{{
			Name:     "compose",
			Command:  "docker compose up",
			Dir:      dir,
			Protocol: "tcp",
			Source:   "compose",
		}}
	}
	return nil
}
