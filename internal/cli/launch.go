package cli

import (
	"context"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"

	"lnch.sh/internal/run"
)

func addLaunchFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("dry-run", false, "Print the launch plan and exit")
	cmd.Flags().Bool("detach", false, "Run in the background and print URLs")
	cmd.Flags().Bool("no-tui", false, "Stream prefixed logs instead of the dashboard")
	cmd.Flags().Bool("share", false, "Expose the stack on the LAN and on https://lnch.sh")
	cmd.Flags().Bool("https", true, "Publish trusted local HTTPS URLs")
	cmd.Flags().Bool("no-https", false, "Disable local HTTPS proxies")
	cmd.Flags().String("relay", "", "Tunnel relay base URL. Defaults to https://lnch.sh. Use off for LAN only")
	cmd.Flags().String("relay-token", "", "Relay registration token (overrides LNCH_RELAY_TOKEN)")
	cmd.Flags().Duration("wait", 20*time.Second, "How long --detach waits for services to be ready")
}

func newUp(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Detect and launch the dev stack",
		Example: `  lnch up
  lnch up --dry-run
  lnch up --detach
  lnch up --no-tui
  lnch up --json --detach
  lnch share --relay http://127.0.0.1:9090`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLaunch(cmd, g, false)
		},
	}
	addLaunchFlags(cmd)
	return cmd
}

func newShare(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "share",
		Short: "Launch the stack and publish a LAN or public URL",
		Long: `share starts the stack when it is not already running, then prints
https://<id>.lnch.sh and a QR code. The hosted relay is https://lnch.sh.

--relay off keeps the link on the LAN. --relay http://127.0.0.1:9090 uses a
relay started with lnch relay. share.relay in lnch.toml and LNCH_RELAY do the
same job. If the relay cannot be reached within five seconds, the LAN URL is
kept.

Anyone with the link can reach the dev server.`,
		Example: `  lnch share
  lnch share --detach
  lnch share --relay off
  lnch relay --addr 127.0.0.1:9090`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLaunch(cmd, g, true)
		},
	}
	addLaunchFlags(cmd)
	return cmd
}

func runLaunch(cmd *cobra.Command, g *globals, forceShare bool) error {
	root, err := g.projectRoot()
	if err != nil {
		return err
	}
	share, _ := cmd.Flags().GetBool("share")
	https, httpsSet := optionalBool(cmd, "https")
	if cmd.Flags().Changed("no-https") {
		off, _ := cmd.Flags().GetBool("no-https")
		if off {
			v := false
			https = &v
			httpsSet = true
		}
	}
	if !httpsSet {
		https = nil
	}
	wait, _ := cmd.Flags().GetDuration("wait")
	relay, _ := cmd.Flags().GetString("relay")
	token, _ := cmd.Flags().GetString("relay-token")
	dry, _ := cmd.Flags().GetBool("dry-run")
	detach, _ := cmd.Flags().GetBool("detach")
	noTUI, _ := cmd.Flags().GetBool("no-tui")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	_, err = run.Launch(ctx, run.Options{
		Root:       root,
		JSON:       g.JSON,
		DryRun:     dry,
		Detach:     detach,
		NoTUI:      noTUI,
		Share:      forceShare || share,
		HTTPS:      https,
		Relay:      relay,
		RelayToken: token,
		Wait:       wait,
		Stdout:     cmd.OutOrStdout(),
		Stderr:     cmd.ErrOrStderr(),
	})
	return err
}

func optionalBool(cmd *cobra.Command, name string) (*bool, bool) {
	if !cmd.Flags().Changed(name) {
		return nil, false
	}
	v, err := cmd.Flags().GetBool(name)
	if err != nil {
		return nil, false
	}
	return &v, true
}
