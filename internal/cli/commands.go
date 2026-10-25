package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"

	"lnch.sh/internal/browser"
	"lnch.sh/internal/certs"
	"lnch.sh/internal/lncherr"
	"lnch.sh/internal/mcp"
	"lnch.sh/internal/present"
	"lnch.sh/internal/run"
	"lnch.sh/internal/tunnel"
	"lnch.sh/internal/version"
)

func newDown(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Stop the running stack",
		Example: `  lnch down
  lnch down --root C:\src\my-app`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := g.projectRoot()
			if err != nil {
				return err
			}
			msg, err := run.Down(context.Background(), root)
			if err != nil {
				return err
			}
			if g.JSON {
				return present.JSON(cmd.OutOrStdout(), map[string]string{"status": msg})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "lnch: %s\n", msg)
			return nil
		},
	}
}

func newStatus(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show ports, health, and URLs",
		Example: `  lnch status
  lnch status --json
  lnch status --watch`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := g.projectRoot()
			if err != nil {
				return err
			}
			watch, _ := cmd.Flags().GetBool("watch")
			if watch {
				ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
				defer stop()
				return run.Watch(ctx, root, g.JSON, cmd.OutOrStdout())
			}
			rt, err := run.Status(context.Background(), root)
			if err != nil {
				return err
			}
			if g.JSON {
				return present.JSON(cmd.OutOrStdout(), rt)
			}
			present.RuntimeText(cmd.OutOrStdout(), rt)
			return nil
		},
	}
	cmd.Flags().Bool("watch", false, "Open the live dashboard, or follow logs when stdout is not a terminal")
	return cmd
}

func newLogs(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs [service]",
		Short: "Print logs from the running stack",
		Example: `  lnch logs
  lnch logs web --tail 50
  lnch logs api --follow`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := g.projectRoot()
			if err != nil {
				return err
			}
			service := ""
			if len(args) == 1 {
				service = args[0]
			}
			tail, _ := cmd.Flags().GetInt("tail")
			follow, _ := cmd.Flags().GetBool("follow")
			ctx := context.Background()
			var stop context.CancelFunc
			if follow {
				ctx, stop = signal.NotifyContext(context.Background(), os.Interrupt)
				defer stop()
				return run.FollowLogs(ctx, root, service, tail, g.JSON, cmd.OutOrStdout())
			}
			lines, err := run.LogSnapshot(ctx, root, service, tail)
			if err != nil {
				return err
			}
			if g.JSON {
				return present.JSON(cmd.OutOrStdout(), map[string]any{"service": service, "lines": lines})
			}
			for name, chunk := range lines {
				for _, line := range chunk {
					if service == "" {
						fmt.Fprintf(cmd.OutOrStdout(), "%-14s | %s\n", name, line)
					} else {
						fmt.Fprintln(cmd.OutOrStdout(), line)
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().Int("tail", 100, "Number of lines to print")
	cmd.Flags().BoolP("follow", "f", false, "Keep printing new lines until interrupted")
	return cmd
}

func newRestart(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "restart <service>",
		Short: "Restart one service",
		Example: `  lnch restart web
  lnch restart api --root .`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return lncherr.New("service name required", "Example: lnch restart web")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := g.projectRoot()
			if err != nil {
				return err
			}
			rt, err := run.Restart(context.Background(), root, args[0])
			if err != nil {
				return err
			}
			if g.JSON {
				return present.JSON(cmd.OutOrStdout(), rt)
			}
			present.RuntimeText(cmd.OutOrStdout(), rt)
			return nil
		},
	}
}

func newOpen(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "open",
		Short: "Open the primary local URL in the browser",
		Example: `  lnch open
  lnch open --public`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := g.projectRoot()
			if err != nil {
				return err
			}
			public, _ := cmd.Flags().GetBool("public")
			u, err := run.OpenURL(root, public)
			if err != nil {
				return err
			}
			if err := browser.Open(u); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), u)
			return nil
		},
	}
	cmd.Flags().Bool("public", false, "Open the share URL instead of the local URL")
	return cmd
}

func newDetect(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "detect",
		Short: "Print the services lnch would start",
		Example: `  lnch detect
  lnch detect --json
  lnch detect --root ./apps/web`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := g.projectRoot()
			if err != nil {
				return err
			}
			p, err := run.LoadPlan(root)
			if err != nil {
				return err
			}
			if g.JSON {
				return present.JSON(cmd.OutOrStdout(), p)
			}
			present.PlanText(cmd.OutOrStdout(), p)
			return nil
		},
	}
}

func newInit(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write lnch.toml from the detected stack",
		Example: `  lnch init
  lnch init --stdout
  lnch init --force`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := g.projectRoot()
			if err != nil {
				return err
			}
			force, _ := cmd.Flags().GetBool("force")
			stdout, _ := cmd.Flags().GetBool("stdout")
			return run.Init(root, force, stdout, cmd.OutOrStdout())
		},
	}
	cmd.Flags().Bool("force", false, "Overwrite an existing lnch.toml")
	cmd.Flags().Bool("stdout", false, "Print the file instead of writing it")
	return cmd
}

func newTrust(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:     "trust",
		Short:   "Install the local CA so https://localhost is trusted",
		Example: `  lnch trust`,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := certs.Trust()
			if err != nil {
				if path != "" {
					fmt.Fprintf(cmd.ErrOrStderr(), "CA: %s\n", path)
				}
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Trusted the lnch local CA for this user.\nCA: %s\nRestart the browser, then open the https://localhost URL.\n", path)
			return nil
		},
	}
}

func newMCP(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:     "mcp",
		Short:   "Serve the lnch MCP tool API on stdio",
		Example: `  lnch mcp`,
		Long: `mcp speaks the Model Context Protocol on stdin and stdout so an agent can
detect, launch, inspect, restart, and share the dev stack.

Cursor mcp.json:
  {"mcpServers":{"lnch":{"command":"lnch","args":["mcp"]}}}`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := g.projectRoot()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			return mcp.Serve(ctx, os.Stdin, os.Stdout, root)
		},
	}
}

func newRelay(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "relay",
		Short: "Run a tunnel relay that issues public URLs",
		Example: `  lnch relay
  lnch relay --addr 127.0.0.1:9090
  lnch relay --addr :8080 --domain lnch.sh --scheme https`,
		RunE: func(cmd *cobra.Command, args []string) error {
			addr, _ := cmd.Flags().GetString("addr")
			domain, _ := cmd.Flags().GetString("domain")
			scheme, _ := cmd.Flags().GetString("scheme")
			token, _ := cmd.Flags().GetString("register-token")
			if token == "" {
				token = os.Getenv("LNCH_RELAY_TOKEN")
			}
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			rl := &tunnel.Relay{Domain: domain, Scheme: scheme, RegisterToken: token}
			rl.SetAddr(ln.Addr().String())
			srv := &http.Server{Handler: rl, ReadHeaderTimeout: 10 * time.Second}
			errCh := make(chan error, 1)
			go func() { errCh <- srv.Serve(ln) }()
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "lnch relay listening on http://%s\n", ln.Addr())
			if domain != "" {
				fmt.Fprintf(out, "public urls: %s://<id>.%s\n", scheme, domain)
			}
			fmt.Fprintf(out, "Example: lnch share --relay http://%s\n", ln.Addr())
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			select {
			case <-ctx.Done():
				shut, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				return srv.Shutdown(shut)
			case err := <-errCh:
				if err == http.ErrServerClosed {
					return nil
				}
				return err
			}
		},
	}
	cmd.Flags().String("addr", "127.0.0.1:9090", "Listen address")
	cmd.Flags().String("domain", "", "Public domain for https://<id>.domain URLs")
	cmd.Flags().String("scheme", "http", "Scheme advertised for --domain URLs")
	cmd.Flags().String("register-token", "", "Require this token to open a tunnel")
	return cmd
}

func newVersion(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Print the lnch version",
		Example: `  lnch version`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if g.JSON {
				return present.JSON(cmd.OutOrStdout(), map[string]string{"version": version.Version})
			}
			fmt.Fprintln(cmd.OutOrStdout(), "lnch "+version.Version)
			return nil
		},
	}
}

func newSupervise(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "supervise",
		Short:   "Run a supervisor from a saved plan",
		Hidden:  true,
		Example: `  lnch supervise --plan C:\Users\me\.lnch\plans\plan.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, _ := cmd.Flags().GetString("plan")
			if path == "" {
				return lncherr.New("missing --plan", "Example: lnch supervise --plan <file>")
			}
			return run.Supervise(context.Background(), path)
		},
	}
	cmd.Flags().String("plan", "", "Path to the launch plan JSON")
	return cmd
}
