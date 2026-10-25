// Package control is the local API a running lnch supervisor exposes.
package control

import (
	"context"

	"lnch.sh/internal/state"
)

// Backend is implemented by the supervisor.
type Backend interface {
	Runtime() state.Runtime
	Logs(service string, tail int) map[string][]string
	Restart(context.Context, string) error
	Share(context.Context) (string, error)
	Stop()
	Shutdown(context.Context) error
}
