// Package run is the command layer shared by the CLI and the MCP server.
package run

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"lnch.sh/internal/control"
	"lnch.sh/internal/detect"
	"lnch.sh/internal/lncherr"
	"lnch.sh/internal/plan"
	"lnch.sh/internal/present"
	"lnch.sh/internal/proc"
	"lnch.sh/internal/state"
	"lnch.sh/internal/supervisor"
	"lnch.sh/internal/tty"
	"lnch.sh/internal/tui"
	"lnch.sh/internal/tunnel"
)

// Options controls one launch.
type Options struct {
	Root       string
	JSON       bool
	DryRun     bool
	Detach     bool
	NoTUI      bool
	Share      bool
	HTTPS      *bool
	Relay      string
	RelayToken string
	Wait       time.Duration
	Stdout     io.Writer
	Stderr     io.Writer
}

// Result is the machine-readable outcome of a launch.
type Result struct {
	Plan    *plan.Plan     `json:"plan,omitempty"`
	Runtime *state.Runtime `json:"runtime,omitempty"`
	Message string         `json:"message,omitempty"`
}

func (o Options) out() io.Writer {
	if o.Stdout != nil {
		return o.Stdout
	}
	return os.Stdout
}

func (o Options) err() io.Writer {
	if o.Stderr != nil {
		return o.Stderr
	}
	return os.Stderr
}

// LoadPlan resolves config or detection for root.
func LoadPlan(root string) (*plan.Plan, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	p, err := detect.Detect(root)
	if err != nil {
		if err == detect.ErrNone {
			return nil, lncherr.New(
				fmt.Sprintf("no launchable services found in %s", root),
				"Looked for lnch.toml, Procfile, package.json, go.mod, Cargo.toml, Python, Compose, Makefile, and justfile.",
				"Example: lnch init --root "+root,
			)
		}
		return nil, err
	}
	return p, nil
}

// Launch detects a project and starts it, or prints the plan for --dry-run.
func Launch(ctx context.Context, opt Options) (Result, error) {
	if opt.Root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return Result{}, err
		}
		opt.Root = wd
	}
	root, err := filepath.Abs(opt.Root)
	if err != nil {
		return Result{}, err
	}
	opt.Root = root
	if opt.Wait <= 0 {
		opt.Wait = 20 * time.Second
	}
	if opt.RelayToken == "" {
		opt.RelayToken = os.Getenv("LNCH_RELAY_TOKEN")
	}

	p, err := LoadPlan(root)
	if err != nil {
		return Result{}, err
	}
	p.Share.Relay = resolveRelay(opt.Relay, p.Share.Relay, os.Getenv("LNCH_RELAY"))
	if opt.HTTPS != nil {
		p.HTTPS = *opt.HTTPS
	}

	running, alive := state.LoadIfRunning(root)
	if opt.DryRun {
		if opt.JSON {
			payload := map[string]any{"plan": p, "share": opt.Share}
			if alive {
				payload["already_running"] = true
			}
			if err := present.JSON(opt.out(), payload); err != nil {
				return Result{}, err
			}
		} else {
			present.PlanText(opt.out(), p)
			if opt.Share {
				fmt.Fprintln(opt.out(), "share: enabled")
			}
			if alive {
				fmt.Fprintf(opt.err(), "A stack is already running.\nExample: lnch status --root %s\n", root)
			}
		}
		return Result{Plan: p, Message: "dry-run"}, nil
	}

	if alive && control.Ping(running) {
		return attach(ctx, opt, p, running)
	}
	if alive && time.Since(running.Started) < 15*time.Second {
		return Result{}, lncherr.New(
			"lnch is already starting for "+running.Project,
			"Example: lnch status --root "+root,
		)
	}
	if alive {
		_ = proc.KillTree(running.PID)
		_ = state.Remove(root)
	}

	if opt.Detach {
		rt, err := detach(ctx, opt, p)
		if err != nil {
			return Result{}, err
		}
		res := Result{Plan: p, Runtime: &rt, Message: "running"}
		writeRuntime(opt, rt, opt.Share)
		return res, nil
	}
	return foreground(ctx, opt, p)
}

func attach(ctx context.Context, opt Options, p *plan.Plan, rt state.Runtime) (Result, error) {
	if opt.Share && rt.ShareURL == "" {
		cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		_, err := control.New(rt).Share(cctx)
		cancel()
		if err != nil {
			return Result{}, err
		}
		if fresh, err := state.Load(opt.Root); err == nil {
			rt = fresh
		}
	}
	pub := rt.Public()
	res := Result{Plan: p, Runtime: &pub, Message: "already running"}
	interactive := !opt.Detach && !opt.NoTUI && !opt.JSON && tty.Is(os.Stdin) && tty.Is(os.Stdout)
	if interactive {
		fmt.Fprintf(opt.err(), "lnch: %s is already running. Attaching. q closes the dashboard.\n", rt.Project)
		err := tui.Run(control.New(rt), tui.Options{QuitShutsDown: false})
		return res, err
	}
	writeRuntime(opt, rt, opt.Share)
	return res, nil
}

func foreground(ctx context.Context, opt Options, p *plan.Plan) (Result, error) {
	useTUI := !opt.NoTUI && !opt.JSON && tty.Is(os.Stdin) && tty.Is(os.Stdout)
	var hooks supervisor.Hooks
	if !useTUI {
		pr := newPrinter(opt)
		hooks.OnLog = pr.log
		hooks.OnStatus = pr.status
		hooks.OnShare = func(url, hint, errText string) {
			pr.share(url, hint, errText, tty.Is(os.Stdout) && !opt.JSON)
		}
	}
	sup, err := supervisor.Start(supervisor.Options{
		Plan:       *p,
		Share:      opt.Share,
		RelayToken: opt.RelayToken,
		Hooks:      hooks,
	})
	if err != nil {
		return Result{}, err
	}
	if useTUI {
		errCh := make(chan error, 1)
		go func() { errCh <- sup.Run(context.Background()) }()
		select {
		case err := <-errCh:
			return Result{}, err
		case <-sup.Ready():
		}
		err = tui.Run(sup, tui.Options{QuitShutsDown: true})
		shutCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		_ = sup.Shutdown(shutCtx)
		cancel()
		runErr := <-errCh
		if err != nil {
			return Result{}, err
		}
		return Result{Plan: p, Message: "stopped"}, runErr
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	if !opt.JSON {
		fmt.Fprintf(opt.out(), "lnch: %s\n", p.Name)
	} else {
		_ = present.Event(opt.out(), map[string]any{"event": "start", "project": p.Name, "root": p.Root})
	}
	if err := sup.Run(ctx); err != nil {
		return Result{}, err
	}
	return Result{Plan: p, Message: "stopped"}, nil
}

func detach(ctx context.Context, opt Options, p *plan.Plan) (state.Runtime, error) {
	logPath, err := state.LogPath(p.Root)
	if err != nil {
		return state.Runtime{}, err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return state.Runtime{}, err
	}
	path, err := state.SavePlan(p.Root, launchFile{
		Plan:       *p,
		Share:      opt.Share,
		RelayToken: opt.RelayToken,
		LogFile:    logPath,
	})
	if err != nil {
		return state.Runtime{}, err
	}
	exe, err := os.Executable()
	if err != nil {
		return state.Runtime{}, err
	}
	cmd := exec.Command(exe, "supervise", "--plan", path)
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return state.Runtime{}, err
	}
	cmd.Stdout = lf
	cmd.Stderr = lf
	proc.ConfigureDetach(cmd)
	if err := cmd.Start(); err != nil {
		_ = lf.Close()
		return state.Runtime{}, err
	}
	_ = lf.Close()
	pid := cmd.Process.Pid

	deadline := time.Now().Add(10 * time.Second)
	var rt state.Runtime
	ok := false
	for time.Now().Before(deadline) {
		if !proc.Alive(pid) {
			return state.Runtime{}, lncherr.New(
				"supervisor exited before it was ready",
				tailFile(logPath, 40),
				"Log: "+logPath,
			)
		}
		if cur, loaded := state.LoadIfRunning(p.Root); loaded && control.Ping(cur) {
			rt = cur
			ok = true
			break
		}
		select {
		case <-ctx.Done():
			return state.Runtime{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !ok {
		return state.Runtime{}, lncherr.New(
			"timed out waiting for the supervisor",
			"Log: "+logPath,
			"Example: lnch status --root "+p.Root,
		)
	}

	end := time.Now().Add(opt.Wait)
	for time.Now().Before(end) {
		cctx, cancel := context.WithTimeout(ctx, time.Second)
		cur, err := control.New(rt).Runtime(cctx)
		cancel()
		if err == nil {
			if disk, err := state.Load(p.Root); err == nil {
				cur.Token = disk.Token
				cur.Control = disk.Control
			}
			rt = cur
			if state.ReadyEnough(cur) && (!opt.Share || cur.ShareURL != "") {
				break
			}
		}
		if !proc.Alive(pid) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if disk, err := state.Load(p.Root); err == nil && disk.PID != 0 {
		rt = disk
	}
	return rt, nil
}

type launchFile struct {
	Plan       plan.Plan `json:"plan"`
	Share      bool      `json:"share"`
	RelayToken string    `json:"relay_token,omitempty"`
	LogFile    string    `json:"log_file,omitempty"`
}

// Supervise runs a previously written plan in the foreground. It is the detached child.
func Supervise(ctx context.Context, planPath string) error {
	var file launchFile
	if err := state.LoadPlan(planPath, &file); err != nil {
		return err
	}
	var mu sync.Mutex
	sup, err := supervisor.Start(supervisor.Options{
		Plan:       file.Plan,
		Share:      file.Share,
		RelayToken: file.RelayToken,
		LogFile:    file.LogFile,
		Hooks: supervisor.Hooks{
			OnLog: func(name, line string) {
				mu.Lock()
				fmt.Printf("%s | %s\n", name, line)
				mu.Unlock()
			},
			OnStatus: func(svc state.Service) {
				mu.Lock()
				fmt.Printf("%s %s %s\n", svc.Name, svc.Status, state.BestLocal(svc))
				mu.Unlock()
			},
			OnShare: func(url, hint, errText string) {
				mu.Lock()
				fmt.Print(present.ShareLines(url, hint, errText))
				mu.Unlock()
			},
		},
	})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	return sup.Run(ctx)
}

type printer struct {
	mu   sync.Mutex
	out  io.Writer
	json bool
}

func newPrinter(opt Options) *printer {
	return &printer{out: opt.out(), json: opt.JSON}
}

func (p *printer) log(name, line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.json {
		_ = present.Event(p.out, map[string]any{"event": "log", "service": name, "line": line})
		return
	}
	fmt.Fprintf(p.out, "%-14s | %s\n", name, line)
}

func (p *printer) status(svc state.Service) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.json {
		_ = present.Event(p.out, map[string]any{
			"event": "service", "name": svc.Name, "status": svc.Status,
			"url": state.BestLocal(svc), "port": svc.Port,
		})
		return
	}
	url := state.BestLocal(svc)
	if url == "" {
		url = "-"
	}
	fmt.Fprintf(p.out, "%-14s %-12s %s\n", svc.Name, svc.Status, url)
}

func (p *printer) share(url, hint, errText string, qr bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.json {
		_ = present.Event(p.out, map[string]any{"event": "share", "url": url, "hint": hint, "error": errText})
		return
	}
	fmt.Fprint(p.out, present.ShareLines(url, hint, errText))
	if qr && url != "" && present.WantQR() {
		_ = present.FprintQR(p.out, url)
	}
}

func writeRuntime(opt Options, rt state.Runtime, qr bool) {
	pub := rt.Public()
	if opt.JSON {
		_ = present.JSON(opt.out(), pub)
		return
	}
	present.RuntimeText(opt.out(), pub)
	if qr && pub.ShareURL != "" && present.WantQR() && tty.Is(os.Stdout) {
		fmt.Fprintln(opt.out())
		_ = present.FprintQR(opt.out(), pub.ShareURL)
	}
}

func tailFile(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := splitLines(string(b))
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := ""
	for _, line := range lines {
		if line == "" {
			continue
		}
		out += line + "\n"
	}
	return out
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, trimCR(s[start:i]))
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, trimCR(s[start:]))
	}
	return lines
}

func trimCR(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\r' {
		return s[:len(s)-1]
	}
	return s
}

// resolveRelay picks the tunnel the share command registers with.
// A flag wins, then lnch.toml, then LNCH_RELAY. An empty choice uses the hosted relay.
// off, none, false, and - disable the relay and leave the share on the LAN.
func resolveRelay(flag, file, env string) string {
	if flag != "" {
		return normalizeRelay(flag)
	}
	if file != "" {
		return normalizeRelay(file)
	}
	if env != "" {
		return normalizeRelay(env)
	}
	return tunnel.PublicRelay
}

func normalizeRelay(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "off", "none", "false", "-":
		return ""
	default:
		return strings.TrimSpace(value)
	}
}
