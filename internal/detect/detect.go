// Package detect builds a launch plan from a project tree.
package detect

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"lnch.sh/internal/config"
	"lnch.sh/internal/plan"
)

// ErrNone means the project has nothing lnch knows how to start.
var ErrNone = errors.New("no launchable services")

const walkDepth = 3

// Detect returns lnch.toml when it exists, otherwise Procfile, otherwise auto-detection.
func Detect(root string) (*plan.Plan, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	p, err := config.LoadRoot(root)
	if err == nil {
		return p, nil
	}
	if !config.IsNotExist(err) {
		return nil, err
	}
	return DetectLoose(root)
}

// DetectLoose ignores lnch.toml. lnch init uses it to fill a new file.
func DetectLoose(root string) (*plan.Plan, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if p, err := loadProcfile(root); p != nil || err != nil {
		return p, err
	}
	p, err := auto(root)
	if err != nil {
		return nil, err
	}
	if p == nil || len(p.Services) == 0 {
		return nil, ErrNone
	}
	return p, nil
}

func auto(root string) (*plan.Plan, error) {
	seenNames := map[string]struct{}{}
	seenNode := map[string]struct{}{}
	var svcs []plan.Service
	add := func(list ...plan.Service) {
		for _, s := range list {
			if strings.TrimSpace(s.Command) == "" && len(s.Argv) == 0 {
				continue
			}
			if s.Name == "" {
				s.Name = "app"
			}
			s.Name = takeName(seenNames, sanitize(s.Name))
			if s.Dir == "" {
				s.Dir = root
			}
			if s.Protocol == "" {
				s.Protocol = "http"
			}
			svcs = append(svcs, s)
		}
	}

	skipNestedNode := false
	if svc, meta, ok := detectNode(root, root); ok {
		add(svc)
		seenNode[clean(root)] = struct{}{}
		skipNestedNode = meta
	}

	for _, dir := range expandGlobs(root, workspaceGlobs(root)) {
		if skipNestedNode {
			break
		}
		if _, ok := seenNode[clean(dir)]; ok {
			continue
		}
		if svc, _, ok := detectNode(dir, root); ok {
			add(svc)
			seenNode[clean(dir)] = struct{}{}
		}
	}

	goWork := goWorkDirs(root)
	if len(goWork) > 0 {
		for _, dir := range goWork {
			add(detectGo(dir)...)
		}
	} else {
		add(detectGo(root)...)
	}
	claimedCargo := map[string]struct{}{}
	cargoSvcs, claimed := detectCargo(root)
	add(cargoSvcs...)
	for _, dir := range claimed {
		claimedCargo[clean(dir)] = struct{}{}
	}
	add(detectPython(root)...)
	rootCompose := detectCompose(root)
	add(rootCompose...)

	for _, dir := range subdirs(root, walkDepth) {
		c := clean(dir)
		if _, ok := seenNode[c]; !ok && !skipNestedNode {
			if svc, _, ok := detectNode(dir, root); ok {
				add(svc)
				seenNode[c] = struct{}{}
			}
		}
		if len(goWork) == 0 {
			add(detectGo(dir)...)
		}
		if _, ok := claimedCargo[c]; !ok {
			sv, claim := detectCargo(dir)
			add(sv...)
			for _, d := range claim {
				claimedCargo[clean(d)] = struct{}{}
			}
		}
		add(detectPython(dir)...)
		if len(rootCompose) == 0 {
			add(detectCompose(dir)...)
		}
	}

	if len(svcs) == 0 {
		if s, ok := detectJust(root); ok {
			add(s)
		} else if s, ok := detectMake(root); ok {
			add(s)
		}
	}
	if len(svcs) == 0 {
		return nil, nil
	}

	sort.SliceStable(svcs, func(i, j int) bool {
		if rank(svcs[i]) != rank(svcs[j]) {
			return rank(svcs[i]) < rank(svcs[j])
		}
		return svcs[i].Name < svcs[j].Name
	})
	wireDepends(svcs)
	markPrimary(svcs)

	p := &plan.Plan{
		Name:     projectName(root),
		Root:     root,
		Source:   "detect",
		HTTPS:    true,
		Services: svcs,
	}
	if n := primaryName(svcs); n != "" {
		p.Share.Service = n
	}
	if err := plan.Validate(p); err != nil {
		for i := range p.Services {
			p.Services[i].DependsOn = nil
		}
		if err := plan.Validate(p); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func rank(s plan.Service) int {
	if s.Protocol == "tcp" || s.Source == "compose" {
		return 0
	}
	switch s.Source {
	case "go.mod", "cargo", "python":
		return 1
	default:
		return 2
	}
}

func wireDepends(svcs []plan.Service) {
	index := map[string]int{}
	for i, s := range svcs {
		index[s.Name] = i
	}
	find := func(names ...string) string {
		for _, n := range names {
			if _, ok := index[n]; ok {
				return n
			}
		}
		return ""
	}
	api := find("api", "server", "backend")
	db := find("db", "postgres", "redis", "mysql", "mongo", "database")
	if api != "" && db != "" && api != db && len(svcs[index[api]].DependsOn) == 0 {
		svcs[index[api]].DependsOn = []string{db}
	}
	if api == "" {
		return
	}
	for _, web := range []string{"web", "frontend", "app", "client", "ui", "site"} {
		i, ok := index[web]
		if !ok || web == api || len(svcs[i].DependsOn) > 0 {
			continue
		}
		svcs[i].DependsOn = []string{api}
	}
}

func markPrimary(svcs []plan.Service) {
	for _, s := range svcs {
		if s.Primary {
			return
		}
	}
	prefer := []string{"web", "frontend", "app", "client", "ui", "site"}
	for _, n := range prefer {
		for i := range svcs {
			if svcs[i].Name == n && svcs[i].Protocol != "tcp" {
				svcs[i].Primary = true
				return
			}
		}
	}
	for i := range svcs {
		if svcs[i].Protocol != "tcp" && (svcs[i].Source == "package.json" || svcs[i].Source == "python") {
			svcs[i].Primary = true
			return
		}
	}
	for i := range svcs {
		if svcs[i].Protocol != "tcp" {
			svcs[i].Primary = true
			return
		}
	}
	if len(svcs) > 0 {
		svcs[0].Primary = true
	}
}

func primaryName(svcs []plan.Service) string {
	for _, s := range svcs {
		if s.Primary {
			return s.Name
		}
	}
	return ""
}

func projectName(root string) string {
	if pkg, ok := readPkg(root); ok && strings.TrimSpace(pkg.Name) != "" {
		return sanitize(pkg.Name)
	}
	return sanitize(filepath.Base(root))
}

func takeName(seen map[string]struct{}, name string) string {
	if _, ok := seen[name]; !ok {
		seen[name] = struct{}{}
		return name
	}
	for i := 2; ; i++ {
		n := name + "-" + itoa(i)
		if _, ok := seen[n]; !ok {
			seen[n] = struct{}{}
			return n
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

var nonName = regexp.MustCompile(`[^a-z0-9_-]+`)

func sanitize(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.TrimPrefix(name, "@")
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, `\`, "-")
	name = nonName.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if name == "" {
		return "app"
	}
	if len(name) > 40 {
		name = strings.Trim(name[:40], "-")
	}
	if name == "" {
		return "app"
	}
	if name[0] == '_' || name[0] == '-' {
		name = "app-" + strings.TrimLeft(name, "_-")
	}
	return name
}

func subdirs(root string, depth int) []string {
	var out []string
	var walk func(string, int)
	walk = func(dir string, d int) {
		if d == 0 {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() || skipDir(e.Name()) {
				continue
			}
			p := filepath.Join(dir, e.Name())
			out = append(out, p)
			walk(p, d-1)
		}
	}
	walk(root, depth)
	sort.Strings(out)
	return out
}

func skipDir(name string) bool {
	if name == "" || name[0] == '.' {
		return true
	}
	switch strings.ToLower(name) {
	case "node_modules", "vendor", "dist", "build", "target", "coverage", "out", "tmp", "temp",
		"venv", "__pycache__", "site-packages", "testdata":
		return true
	default:
		return false
	}
}

func clean(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return filepath.Clean(a)
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func readLimit(path string, n int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b := make([]byte, n)
	k, err := f.Read(b)
	if err != nil && k == 0 {
		return "", err
	}
	return string(b[:k]), nil
}

type pkgFile struct {
	Name       string            `json:"name"`
	Scripts    map[string]string `json:"scripts"`
	Workspaces json.RawMessage   `json:"workspaces"`
}

func readPkg(dir string) (pkgFile, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return pkgFile{}, false
	}
	var p pkgFile
	if json.Unmarshal(b, &p) != nil {
		return pkgFile{}, false
	}
	return p, true
}
