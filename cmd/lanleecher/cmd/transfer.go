package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/lan-dot-party/lanleecher/internal/store"
	"github.com/lan-dot-party/lanleecher/internal/transfer"
	"github.com/lan-dot-party/lanleecher/pkg/version"
)

func init() {
	exportCmd.Flags().StringSlice("event", nil, "export only these events (slug, repeatable)")
	exportCmd.Flags().StringP("output", "o", "", "output file (default lanleecher-export-<date>.json.gz, - for stdout)")
	importCmd.Flags().Bool("dry-run", false, "show what would be imported and change nothing")
	importCmd.Flags().String("event-conflict", store.ConflictNew,
		"event whose slug exists here: new (import under <slug>-import) or skip")
	importCmd.Flags().Bool("no-backup", false, "skip the database backup before the import")
	rootCmd.AddCommand(exportCmd, importCmd)
}

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export switches, ports, devices, MACs and events (no credentials or users)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		events, _ := cmd.Flags().GetStringSlice("event")
		out, _ := cmd.Flags().GetString("output")
		e, err := open(cmd.Context())
		if err != nil {
			return err
		}
		defer e.close()
		doc, stats, err := e.db.Export(cmd.Context(), store.ExportOptions{Events: events,
			Software: "lanleecher " + version.GetVersion(), Source: e.cfg.PublicURL})
		if err != nil {
			return err
		}
		var w io.Writer = os.Stdout
		if out != "-" {
			if out == "" {
				out = "lanleecher-export-" + time.Now().Format("2006-01-02-150405") + ".json.gz"
			}
			f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
			if err != nil {
				return err
			}
			defer f.Close()
			w = f
		}
		if err := transfer.Write(w, doc); err != nil {
			return err
		}
		e.audit(cmd.Context(), store.AuditEntry{Actor: "cli", Action: "transfer.export",
			Details: fmt.Sprintf("%d events, %d MACs", len(doc.Events), len(doc.MACs))})
		if out != "-" {
			fmt.Fprintf(os.Stderr, "exported %d switches, %d devices, %d MACs, %d events to %s\n",
				len(doc.Switches), len(doc.Devices), len(doc.MACs), len(doc.Events), out)
		}
		if stats.SkippedRows > 0 {
			fmt.Fprintf(os.Stderr, "skipped %d traffic rows of deleted ports or unknown MACs\n", stats.SkippedRows)
		}
		return nil
	},
}

var importCmd = &cobra.Command{
	Use:   "import <file>",
	Short: "Import an export file (switches are created as archived, events as ended)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dry, _ := cmd.Flags().GetBool("dry-run")
		conflict, _ := cmd.Flags().GetString("event-conflict")
		noBackup, _ := cmd.Flags().GetBool("no-backup")
		f, err := os.Open(args[0])
		if err != nil {
			return err
		}
		defer f.Close()
		doc, err := transfer.Read(f)
		if err != nil {
			return err
		}
		e, err := open(cmd.Context())
		if err != nil {
			return err
		}
		defer e.close()
		if !dry && !noBackup {
			target := filepath.Join(filepath.Dir(e.cfg.ResolvePath(e.cfg.Storage.Path)), "backups",
				"lanleecher-before-import-"+time.Now().Format("2006-01-02-150405")+".db")
			if err := e.db.Backup(cmd.Context(), target); err != nil {
				return fmt.Errorf("backup before import: %w", err)
			}
			fmt.Fprintln(os.Stderr, "backup written to", target)
		}
		rep, err := e.db.Import(cmd.Context(), doc, store.ImportOptions{EventConflict: conflict, DryRun: dry})
		if err != nil {
			return err
		}
		printImportReport(os.Stdout, doc, rep)
		if !dry {
			e.audit(cmd.Context(), store.AuditEntry{Actor: "cli", Action: "transfer.import", Target: filepath.Base(args[0]),
				Details: importSummary(rep)})
		}
		return nil
	},
}

func importSummary(rep *store.ImportReport) string {
	imported := 0
	for _, ev := range rep.Events {
		if ev.Action != store.ActionSkipped {
			imported++
		}
	}
	return fmt.Sprintf("%d switches, %d new MACs, %d merged MACs, %d events", len(rep.Switches), rep.MACsNew,
		rep.MACsMerged, imported)
}

func printImportReport(w io.Writer, doc *transfer.Doc, rep *store.ImportReport) {
	if rep.DryRun {
		fmt.Fprintln(w, "DRY RUN – nothing was changed.")
	}
	fmt.Fprintf(w, "Source: %s, exported %s\n\n", strings.TrimSpace(doc.Software+" "+doc.Source),
		time.Unix(doc.ExportedAt, 0).Format(time.RFC3339))
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SWITCH\tHOST\tACTION\tAS\tNEW PORTS")
	for _, s := range rep.Switches {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\n", s.Name, s.Host, s.Action, s.TargetName, s.PortsNew)
	}
	tw.Flush()
	fmt.Fprintf(w, "\nDevices: %d new. MACs: %d new, %d already known (history merged).\n\n",
		rep.DevicesNew, rep.MACsNew, rep.MACsMerged)
	tw = tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "EVENT\tNAME\tACTION\tAS\tNOTE")
	for _, ev := range rep.Events {
		note := ""
		if ev.Closed {
			note = "was running in the source; imported as ended"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", ev.Slug, ev.Name, ev.Action, ev.TargetSlug, note)
	}
	tw.Flush()
	fmt.Fprintf(w, "\nTraffic rows: %d\n", rep.TrafficRows)
}
