// Package discover reads dev-server log lines for listen addresses.
package discover

import (
	"regexp"
	"strconv"
	"strings"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

var portREs = []*regexp.Regexp{
	regexp.MustCompile(`https?://(?:localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\]|\[::\]):(\d{2,5})`),
	regexp.MustCompile(`(?i)\blocal:\s+\S+?:(\d{2,5})\b`),
	regexp.MustCompile(`(?i)\blistening (?:on|at)\s+(?:https?://\S+?:|[^0-9\n]{0,48}?)(\d{2,5})\b`),
	regexp.MustCompile(`(?i)\b(?:started|ready|running) (?:on|at)\s+(?:https?://\S+?:)?(\d{2,5})\b`),
}

// StripANSI removes common color sequences.
func StripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

// Port returns the first plausible TCP port mentioned in a log line.
func Port(line string) int {
	line = strings.TrimSpace(strings.TrimRight(StripANSI(line), "\r"))
	if line == "" {
		return 0
	}
	for _, re := range portREs {
		m := re.FindStringSubmatch(line)
		if len(m) < 2 {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil || n < 1 || n > 65535 {
			continue
		}
		return n
	}
	return 0
}

// CleanLine prepares a process log line for storage.
func CleanLine(s string) string {
	s = strings.TrimRight(StripANSI(s), "\r")
	if strings.IndexByte(s, 0) >= 0 {
		return "[binary output]"
	}
	printable := 0
	for _, r := range s {
		if r == '\t' || (r >= 32 && r != 127) {
			printable++
		}
	}
	if len(s) > 0 && printable*5 < len([]rune(s)) {
		return "[binary output]"
	}
	if len(s) > 4000 {
		s = s[:4000] + "…"
	}
	return s
}
