package plan

import (
	"regexp"
	"strconv"
	"strings"
)

var tokenRE = regexp.MustCompile(`\$\{([A-Za-z0-9_-]+)\.port\}|\$\{[Pp][Oo][Rr][Tt]\}|\$PORT\b`)

// Expand replaces ${port}, $PORT, and ${name.port}. Unknown tokens are left
// in place and reported so a service log shows why a command looked wrong.
func Expand(s string, self int, ports map[string]int) (string, []string) {
	var warns []string
	out := tokenRE.ReplaceAllStringFunc(s, func(m string) string {
		if m == "$PORT" || strings.EqualFold(m, "${port}") || strings.EqualFold(m, "${PORT}") {
			if self > 0 {
				return strconv.Itoa(self)
			}
			warns = append(warns, m+" is unset")
			return m
		}
		inner := strings.TrimSuffix(strings.TrimPrefix(m, "${"), "}")
		name := strings.TrimSuffix(inner, ".port")
		if ports != nil && ports[name] > 0 {
			return strconv.Itoa(ports[name])
		}
		warns = append(warns, m+" is unset")
		return m
	})
	return out, warns
}

// NeedsPort reports whether lnch should reserve a port before launch.
func NeedsPort(s Service) bool {
	if s.Port > 0 {
		return true
	}
	if containsPortToken(s.Command) {
		return true
	}
	for _, v := range s.Env {
		if containsPortToken(v) {
			return true
		}
	}
	return false
}

func containsPortToken(s string) bool {
	return strings.Contains(s, "$PORT") || strings.Contains(s, "${port}") || strings.Contains(s, "${PORT}")
}
