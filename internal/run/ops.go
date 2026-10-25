package run

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"lnch.sh/internal/config"
	"lnch.sh/internal/control"
	"lnch.sh/internal/detect"
	"lnch.sh/internal/lncherr"
	"lnch.sh/internal/present"
	"lnch.sh/internal/proc"
	"lnch.sh/internal/state"
	"lnch.sh/internal/tty"
	"lnch.sh/internal/tui"
)

// Down stops the stack for root. It is a no-op when nothing is running.
func Down(ctx context.Context, root string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rt, ok := state.LoadIfRunning(root)
	if !ok {
		return "already stopped", nil
	}
	if control.Ping(rt) {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = control.New(rt).Shutdown(cctx)
		cancel()
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && proc.Alive(rt.PID) {
		time.Sleep(100 * time.Millisecond)
	}
	if proc.Alive(rt.PID) {
		_ = proc.KillTree(rt.PID)
	}
	_ = state.Remove(root)
	return "stopped", nil
}

// Status returns the public runtime for a running project.
func Status(ctx context.Context, root string) (state.Runtime, error) {
	rt, err := running(root)
	if err != nil {
		return state.Runtime{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cur, err := control.New(rt).Runtime(cctx)
	if err != nil {
		return rt.Public(), nil
	}
	return cur.Public(), nil
}

// Watch opens the dashboard, or follows logs when stdout is not a terminal.
func Watch(ctx context.Context, root string, jsonOut bool, w io.Writer) error {
	rt, err := running(root)
	if err != nil {
		return err
	}
	if !jsonOut && tty.Is(os.Stdin) && tty.Is(os.Stdout) {
		return tui.Run(control.New(rt), tui.Options{QuitShutsDown: false})
	}
	return FollowLogs(ctx, root, "", 200, jsonOut, w)
}

// LogSnapshot returns retained logs.
func LogSnapshot(ctx context.Context, root, service string, tail int) (map[string][]string, error) {
	rt, err := running(root)
	if err != nil {
		return nil, err
	}
	if tail <= 0 {
		tail = 100
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return control.New(rt).Logs(cctx, service, tail)
}

// FollowLogs prints logs until ctx is cancelled.
func FollowLogs(ctx context.Context, root, service string, tail int, jsonOut bool, w io.Writer) error {
	if tail <= 0 {
		tail = 100
	}
	prev := map[string][]string{}
	first := true
	for {
		lines, err := LogSnapshot(ctx, root, service, tail)
		if err != nil {
			return err
		}
		for name, chunk := range lines {
			fresh := chunk
			if !first {
				fresh = newLines(prev[name], chunk)
			}
			for _, line := range fresh {
				if jsonOut {
					_ = present.Event(w, map[string]any{"event": "log", "service": name, "line": line})
				} else if service == "" {
					fmt.Fprintf(w, "%-14s | %s\n", name, line)
				} else {
					fmt.Fprintln(w, line)
				}
			}
			prev[name] = chunk
		}
		first = false
		if ctx.Err() != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(400 * time.Millisecond):
		}
	}
}

func newLines(prev, next []string) []string {
	if len(prev) == 0 {
		return nil
	}
	last := prev[len(prev)-1]
	for i := len(next) - 1; i >= 0; i-- {
		if next[i] == last {
			return next[i+1:]
		}
	}
	return next
}

// Restart restarts one service and returns the updated runtime.
func Restart(ctx context.Context, root, name string) (state.Runtime, error) {
	rt, err := running(root)
	if err != nil {
		return state.Runtime{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := control.New(rt).Restart(cctx, name); err != nil {
		return state.Runtime{}, err
	}
	return Status(ctx, root)
}

// Share asks a running stack to publish itself.
func Share(ctx context.Context, root string) (state.Runtime, error) {
	rt, err := running(root)
	if err != nil {
		return state.Runtime{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if _, err := control.New(rt).Share(cctx); err != nil {
		return state.Runtime{}, err
	}
	return Status(ctx, root)
}

// OpenURL returns the URL lnch open should launch.
func OpenURL(root string, public bool) (string, error) {
	rt, err := running(root)
	if err != nil {
		return "", err
	}
	cur, err := Status(context.Background(), root)
	if err != nil {
		cur = rt.Public()
	}
	if public {
		if cur.ShareURL == "" {
			return "", lncherr.New(
				"this stack has no share URL",
				"Example: lnch share --root "+root,
			)
		}
		return cur.ShareURL, nil
	}
	u := state.OpenURL(cur)
	if u == "" {
		return "", lncherr.New(
			"no local URL is ready yet",
			"Example: lnch status --root "+root,
		)
	}
	return u, nil
}

// Init writes lnch.toml from detection, or a starter file when nothing is detected.
func Init(root string, force, stdout bool, w io.Writer) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	path := filepath.Join(root, "lnch.toml")
	if !stdout {
		if _, err := os.Stat(path); err == nil && !force {
			return lncherr.New(
				"lnch.toml already exists",
				"Example: lnch init --stdout",
				"Example: lnch init --force",
			)
		}
	}
	p, err := detect.DetectLoose(root)
	var text, note string
	if err != nil {
		if err != detect.ErrNone {
			return err
		}
		text = skeleton(root)
		note = "No dev command detected. Wrote a starter lnch.toml."
	} else {
		text = config.Format(p)
		note = fmt.Sprintf("Wrote lnch.toml (%d services).", len(p.Services))
	}
	if stdout {
		_, err = io.WriteString(w, text)
		return err
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return err
	}
	fmt.Fprintln(w, note)
	fmt.Fprintln(w, "Example: lnch up --dry-run")
	return nil
}

func skeleton(root string) string {
	name := filepath.Base(root)
	return fmt.Sprintf(`# Nothing was detected in this directory.
# Fill in a service, then run: lnch up --dry-run

name = %q

[[services]]
name = "web"
command = "npm run dev"
cwd = "."
protocol = "http"
primary = true
`, name)
}

func running(root string) (state.Runtime, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return state.Runtime{}, err
	}
	rt, ok := state.LoadIfRunning(root)
	if !ok || !control.Ping(rt) {
		return state.Runtime{}, lncherr.New(
			"nothing is running in "+root,
			"Example: lnch up --root "+root,
		)
	}
	return rt, nil
}
