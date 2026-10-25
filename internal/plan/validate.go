package plan

import (
	"fmt"
	"regexp"
	"strings"
)

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,40}$`)

// Validate checks names, protocols, dependencies, and cycles.
func Validate(p *Plan) error {
	if p == nil || len(p.Services) == 0 {
		return fmt.Errorf("no services")
	}
	seen := make(map[string]Service, len(p.Services))
	for _, s := range p.Services {
		if !nameRE.MatchString(s.Name) {
			return fmt.Errorf("service name %q must match %s", s.Name, nameRE.String())
		}
		if _, ok := seen[s.Name]; ok {
			return fmt.Errorf("duplicate service name %q", s.Name)
		}
		if strings.TrimSpace(s.Command) == "" && len(s.Argv) == 0 {
			return fmt.Errorf("service %s is missing a command", s.Name)
		}
		switch s.Protocol {
		case "", "http", "tcp":
		default:
			return fmt.Errorf("service %s has unknown protocol %q (use http or tcp)", s.Name, s.Protocol)
		}
		switch s.Restart {
		case "", "no", "on-failure":
		default:
			return fmt.Errorf("service %s has unknown restart %q (use no or on-failure)", s.Name, s.Restart)
		}
		if s.Port < 0 || s.Port > 65535 {
			return fmt.Errorf("service %s has invalid port %d", s.Name, s.Port)
		}
		seen[s.Name] = s
	}
	for _, s := range p.Services {
		for _, d := range s.DependsOn {
			if _, ok := seen[d]; !ok {
				return fmt.Errorf("service %s depends on unknown service %q", s.Name, d)
			}
			if d == s.Name {
				return fmt.Errorf("service %s depends on itself", s.Name)
			}
		}
	}
	return cycles(p.Services)
}

func cycles(svcs []Service) error {
	deps := make(map[string][]string, len(svcs))
	for _, s := range svcs {
		deps[s.Name] = s.DependsOn
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var stack []string
	var walk func(string) error
	walk = func(n string) error {
		switch color[n] {
		case black:
			return nil
		case gray:
			i := 0
			for i < len(stack) && stack[i] != n {
				i++
			}
			cyc := append(append([]string{}, stack[i:]...), n)
			return fmt.Errorf("cycle in depends_on: %s", strings.Join(cyc, " -> "))
		}
		color[n] = gray
		stack = append(stack, n)
		for _, d := range deps[n] {
			if err := walk(d); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		color[n] = black
		return nil
	}
	for _, s := range svcs {
		if err := walk(s.Name); err != nil {
			return err
		}
	}
	return nil
}
