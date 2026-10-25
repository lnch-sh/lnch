package detect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"lnch.sh/internal/plan"
)

func detectNode(dir, root string) (plan.Service, bool, bool) {
	pkg, ok := readPkg(dir)
	if !ok {
		return plan.Service{}, false, false
	}
	script, body, ok := pickScript(pkg.Scripts)
	if !ok {
		return plan.Service{}, false, false
	}
	name := strings.TrimSpace(pkg.Name)
	if name == "" {
		name = filepath.Base(dir)
	}
	return plan.Service{
		Name:     sanitize(name),
		Command:  scriptCmd(pm(dir, root), script),
		Dir:      dir,
		Protocol: "http",
		Source:   "package.json",
	}, isMetaRunner(body), true
}

func pickScript(scripts map[string]string) (name, body string, ok bool) {
	for _, n := range []string{"dev", "start", "serve"} {
		body = strings.TrimSpace(scripts[n])
		if body != "" {
			return n, body, true
		}
	}
	return "", "", false
}

func pm(dir, root string) string {
	d := dir
	root = filepath.Clean(root)
	for {
		switch {
		case exists(d, "pnpm-lock.yaml"):
			return "pnpm"
		case exists(d, "yarn.lock"):
			return "yarn"
		case exists(d, "bun.lock"), exists(d, "bun.lockb"):
			return "bun"
		case exists(d, "package-lock.json"):
			return "npm"
		}
		if filepath.Clean(d) == root || filepath.Dir(d) == d {
			break
		}
		d = filepath.Dir(d)
	}
	return "npm"
}

func scriptCmd(manager, script string) string {
	switch manager {
	case "pnpm":
		return "pnpm run " + script
	case "yarn":
		if script == "dev" || script == "start" {
			return "yarn " + script
		}
		return "yarn run " + script
	case "bun":
		return "bun run " + script
	default:
		return "npm run " + script
	}
}

func isMetaRunner(script string) bool {
	s := " " + strings.ToLower(script) + " "
	markers := []string{
		" turbo ", " lerna ", " nx ", " npx nx", " pnpm -r", "pnpm --recursive",
		"pnpm --filter", "yarn workspaces", "--workspaces", "npm-run-all",
		" concurrently ", " wireit ",
	}
	for _, m := range markers {
		if strings.Contains(s, m) || strings.Contains(strings.ToLower(script), strings.TrimSpace(m)) {
			return true
		}
	}
	return false
}

func workspaceGlobs(root string) []string {
	var globs []string
	if pkg, ok := readPkg(root); ok {
		globs = append(globs, parseWorkspaces(pkg.Workspaces)...)
	}
	globs = append(globs, pnpmGlobs(root)...)
	return globs
}

func parseWorkspaces(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var obj struct {
		Packages []string `json:"packages"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.Packages
	}
	return nil
}

func pnpmGlobs(root string) []string {
	b, err := os.ReadFile(filepath.Join(root, "pnpm-workspace.yaml"))
	if err != nil {
		return nil
	}
	var globs []string
	in := false
	for _, line := range strings.Split(string(b), "\n") {
		trim := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		if strings.HasPrefix(trim, "packages:") {
			in = true
			continue
		}
		if in && strings.HasPrefix(trim, "-") {
			g := strings.TrimSpace(strings.TrimPrefix(trim, "-"))
			g = strings.Trim(g, `"'`)
			if g != "" {
				globs = append(globs, g)
			}
			continue
		}
		if in && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			in = false
		}
	}
	return globs
}

func expandGlobs(root string, globs []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, g := range globs {
		for _, m := range glob(root, g) {
			if _, ok := seen[m]; ok {
				continue
			}
			seen[m] = struct{}{}
			out = append(out, m)
		}
	}
	return out
}

func glob(root, pattern string) []string {
	pattern = strings.Trim(filepath.ToSlash(pattern), "/")
	if pattern == "" {
		return nil
	}
	if !strings.Contains(pattern, "**") {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil {
			return nil
		}
		var dirs []string
		for _, m := range matches {
			if fi, err := os.Stat(m); err == nil && fi.IsDir() {
				dirs = append(dirs, m)
			}
		}
		return dirs
	}
	var out []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if path != root && skipDir(d.Name()) {
			return filepath.SkipDir
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if matchSegments(strings.Split(pattern, "/"), strings.Split(filepath.ToSlash(rel), "/")) {
			out = append(out, path)
		}
		return nil
	})
	return out
}

func matchSegments(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		if matchSegments(pat[1:], segs) {
			return true
		}
		if len(segs) == 0 {
			return false
		}
		return matchSegments(pat, segs[1:])
	}
	if len(segs) == 0 {
		return false
	}
	ok, err := filepath.Match(pat[0], segs[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(pat[1:], segs[1:])
}
