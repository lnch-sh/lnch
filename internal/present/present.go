// Package present formats plans and runtime state for humans and agents.
package present

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"lnch.sh/internal/plan"
	"lnch.sh/internal/state"
)

// PlanText writes a dry-run table.
func PlanText(w io.Writer, p *plan.Plan) {
	fmt.Fprintf(w, "project: %s\nroot: %s\nsource: %s\nhttps: %t\n\n", p.Name, p.Root, p.Source, p.HTTPS)
	fmt.Fprintf(w, "%-14s %-14s %-18s %s\n", "SERVICE", "SOURCE", "DIR", "COMMAND")
	for _, s := range p.Services {
		dir := s.Dir
		if rel, err := filepath.Rel(p.Root, s.Dir); err == nil && rel != "" {
			dir = filepath.ToSlash(rel)
		}
		cmd := s.Command
		if cmd == "" {
			cmd = strings.Join(s.Argv, " ")
		}
		extra := ""
		if len(s.DependsOn) > 0 {
			extra = "  (after " + strings.Join(s.DependsOn, ", ") + ")"
		}
		fmt.Fprintf(w, "%-14s %-14s %-18s %s%s\n", s.Name, s.Source, trunc(dir, 18), cmd, extra)
	}
	fmt.Fprintf(w, "\nprimary: %s\n", p.PrimaryName())
	fmt.Fprintf(w, "Run: lnch up\n")
}

// RuntimeText writes a one-shot status table.
func RuntimeText(w io.Writer, rt state.Runtime) {
	rt = rt.Public()
	fmt.Fprintf(w, "%s  pid %d\n\n", rt.Project, rt.PID)
	fmt.Fprintf(w, "%-14s %-12s %s\n", "SERVICE", "STATUS", "URL")
	for _, s := range rt.Services {
		url := state.BestLocal(s)
		if url == "" {
			url = "-"
		}
		fmt.Fprintf(w, "%-14s %-12s %s\n", s.Name, s.Status, url)
		if s.Status == state.Exited || s.Status == state.Unhealthy {
			if s.LastLine != "" {
				fmt.Fprintf(w, "  %s\n", s.LastLine)
			}
		}
	}
	if rt.ShareURL != "" {
		fmt.Fprintf(w, "\nshare: %s\n", rt.ShareURL)
	}
	if rt.ShareHint != "" {
		fmt.Fprintf(w, "%s\n", rt.ShareHint)
	}
	if rt.ShareError != "" {
		fmt.Fprintf(w, "share error: %s\n", rt.ShareError)
	}
}

// JSON writes indented JSON.
func JSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Event writes one JSON object for a stream.
func Event(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	return enc.Encode(v)
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}
