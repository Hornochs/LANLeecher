// Command lanleecher-agent reports byte counters per MAC of a Linux host or
// Proxmox VE node to LANLeecher. Configuration: /etc/lanleecher/agent.conf.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/lan-dot-party/lanleecher/internal/agent"
	"github.com/lan-dot-party/lanleecher/pkg/version"
)

func main() {
	var configPath string
	var debug bool
	root := &cobra.Command{
		Use:           "lanleecher-agent",
		Short:         "Report traffic per MAC of this host (and its Proxmox guests) to LANLeecher",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVarP(&configPath, "config", "c", agent.DefaultConfigPath, "configuration file")
	root.PersistentFlags().BoolVar(&debug, "debug", false, "log every report")

	load := func() (*agent.Agent, *slog.Logger, error) {
		level := slog.LevelInfo
		if debug {
			level = slog.LevelDebug
		}
		log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
		cfg, warnings, err := agent.LoadConfig(configPath)
		if err != nil {
			return nil, nil, err
		}
		for _, w := range warnings {
			log.Warn(w)
		}
		a, err := agent.New(cfg, log, version.GetVersion())
		return a, log, err
	}

	root.AddCommand(&cobra.Command{
		Use:   "run",
		Short: "Collect and report every interval (the systemd service runs this)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, _, err := load()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return a.Run(ctx)
		},
	}, &cobra.Command{
		Use:   "test",
		Short: "Show what would be reported and send one report (checks connection, TLS and token)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, _, err := load()
			if err != nil {
				return err
			}
			ifs, err := a.Collect()
			if err != nil {
				return err
			}
			fmt.Printf("Mode: %s, %d interfaces\n\n", a.Mode(), len(ifs))
			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "INTERFACE\tKIND\tMAC\tNAME\tDOWN BYTES\tUP BYTES\tIPS")
			for _, in := range ifs {
				name := in.Name
				if in.VMID > 0 {
					name = fmt.Sprintf("%d %s", in.VMID, in.Name)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%v\n", in.IfName, in.Kind, in.MAC, name, in.Down, in.Up, in.IPs)
			}
			tw.Flush()
			res, err := a.Send(cmd.Context(), a.BuildReport(ifs))
			if err != nil {
				return fmt.Errorf("sending failed: %w", err)
			}
			fmt.Printf("\nSent: %d accepted, %d ignored, %d reported by another agent.\n", res.Accepted, res.Ignored, res.Conflicts)
			return nil
		},
	}, &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run:   func(cmd *cobra.Command, args []string) { fmt.Println(version.GetVersion()) },
	})

	if err := root.Execute(); err != nil {
		var se *agent.StatusError
		if errors.As(err, &se) {
			fmt.Fprintln(os.Stderr, "lanleecher-agent:", err)
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "lanleecher-agent:", err)
		os.Exit(1)
	}
}
