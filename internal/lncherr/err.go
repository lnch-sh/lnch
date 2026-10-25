// Package lncherr carries user-facing CLI errors with a next step.
package lncherr

import "strings"

// E is a failure the CLI can print with copy-pasteable hints.
type E struct {
	Message string
	Hints   []string
}

func (e *E) Error() string {
	if e == nil {
		return ""
	}
	if len(e.Hints) == 0 {
		return e.Message
	}
	var b strings.Builder
	b.WriteString(e.Message)
	for _, h := range e.Hints {
		b.WriteByte('\n')
		b.WriteString(h)
	}
	return b.String()
}

// New builds an error whose message is the first line and whose hints follow.
func New(message string, hints ...string) error {
	return &E{Message: message, Hints: hints}
}
