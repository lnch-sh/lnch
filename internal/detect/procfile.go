package detect

import (
	"fmt"
	"path/filepath"
	"strings"

	"lnch.sh/internal/plan"
)

func loadProcfile(root string) (*plan.Plan, error) {
	var text string
	var found bool
	for _, name := range []string{"Procfile", "Procfile.dev"} {
		s, err := readLimit(filepath.Join(root, name), 256*1024)
		if err != nil {
			continue
		}
		text = s
		found = true
		break
	}
	if !found {
		return nil, nil
	}
	seen := map[string]struct{}{}
	var svcs []plan.Service
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, cmd, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = sanitize(strings.TrimSpace(name))
		cmd = strings.TrimSpace(cmd)
		if name == "" || cmd == "" {
			continue
		}
		name = takeName(seen, name)
		protocol := "tcp"
		switch name {
		case "web", "app", "frontend", "api", "client", "ui", "site", "server", "backend":
			protocol = "http"
		}
		svcs = append(svcs, plan.Service{
			Name:     name,
			Command:  cmd,
			Dir:      root,
			Protocol: protocol,
			Source:   "procfile",
		})
	}
	if len(svcs) == 0 {
		return nil, fmt.Errorf("Procfile in %s has no processes", root)
	}
	markPrimary(svcs)
	p := &plan.Plan{
		Name:     projectName(root),
		Root:     root,
		Source:   "procfile",
		HTTPS:    true,
		Services: svcs,
	}
	if n := primaryName(svcs); n != "" {
		p.Share.Service = n
	}
	wireDepends(svcs)
	if err := plan.Validate(p); err != nil {
		for i := range p.Services {
			p.Services[i].DependsOn = nil
		}
	}
	return p, nil
}

func detectMake(dir string) (plan.Service, bool) {
	var text string
	for _, name := range []string{"Makefile", "makefile", "GNUmakefile"} {
		s, err := readLimit(filepath.Join(dir, name), 256*1024)
		if err == nil {
			text = s
			break
		}
	}
	if text == "" {
		return plan.Service{}, false
	}
	target := recipeTarget(text, false)
	if target == "" {
		return plan.Service{}, false
	}
	return plan.Service{
		Name:     target,
		Command:  "make " + target,
		Dir:      dir,
		Protocol: "http",
		Source:   "makefile",
	}, true
}

func detectJust(dir string) (plan.Service, bool) {
	var text string
	for _, name := range []string{"justfile", "Justfile", ".justfile"} {
		s, err := readLimit(filepath.Join(dir, name), 256*1024)
		if err == nil {
			text = s
			break
		}
	}
	if text == "" {
		return plan.Service{}, false
	}
	target := recipeTarget(text, true)
	if target == "" {
		return plan.Service{}, false
	}
	return plan.Service{
		Name:     target,
		Command:  "just " + target,
		Dir:      dir,
		Protocol: "http",
		Source:   "justfile",
	}, true
}

func recipeTarget(text string, just bool) string {
	for _, t := range []string{"dev", "start"} {
		for _, line := range strings.Split(text, "\n") {
			trim := strings.TrimSpace(strings.TrimRight(line, "\r"))
			if just {
				trim = strings.TrimPrefix(trim, "@")
			}
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				continue
			}
			if !strings.HasPrefix(trim, t) {
				continue
			}
			rest := strings.TrimPrefix(trim, t)
			if rest == ":" || strings.HasPrefix(rest, ":") || strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "\t") {
				if strings.Contains(trim, ":") {
					return t
				}
			}
		}
	}
	return ""
}
