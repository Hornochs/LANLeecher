package cmd

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/spf13/cobra"

	"github.com/lan-dot-party/lanleecher/internal/api"
	"github.com/lan-dot-party/lanleecher/internal/app"
	"github.com/lan-dot-party/lanleecher/internal/board"
	"github.com/lan-dot-party/lanleecher/internal/resolver"
	"github.com/lan-dot-party/lanleecher/internal/scheduler"
	"github.com/lan-dot-party/lanleecher/internal/web"
	"github.com/lan-dot-party/lanleecher/pkg/version"
)

func init() {
	rootCmd.AddCommand(serverCmd)
}

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Run the poller, web UI and API",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		e, err := open(ctx)
		if err != nil {
			return err
		}
		defer e.close()
		log := e.log
		log.Info("starting lanleecher", "version", version.GetVersion(), "listen", e.cfg.Listen, "public_url", e.cfg.PublicURL)

		a := app.New(e.cfg, e.db, e.key, log)
		a.Resolver = resolver.New(e.db, e.cfg.Resolver, log)
		a.Sched = scheduler.New(e.db, e.key, e.cfg, a.Resolver, log)
		a.Board = board.New(e.db, a.Sched.Throughput)
		a.Sched.OnPoll = a.Board.Notify

		ui, err := web.New(a)
		if err != nil {
			return err
		}
		apiHandler := api.New(a)

		r := chi.NewRouter()
		r.Use(middleware.Recoverer, app.SecurityHeaders, a.RequestLog)
		r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			if err := e.db.Ping(r.Context()); err != nil {
				http.Error(w, "db unavailable", http.StatusServiceUnavailable)
				return
			}
			w.Write([]byte("ok"))
		})
		r.Get("/llms.txt", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write([]byte(llmsTxt))
		})
		r.Mount("/api", apiHandler.Routes())
		r.With(a.SessionMiddleware).Mount("/", ui.Routes())

		go a.Resolver.Run(ctx)
		if err := a.Sched.Start(ctx); err != nil {
			return err
		}
		go func() {
			hup := make(chan os.Signal, 1)
			signal.Notify(hup, syscall.SIGHUP)
			for range hup {
				log.Info("SIGHUP: reloading switches")
				a.Sched.Reload(ctx)
			}
		}()

		srv := &http.Server{
			Addr:              e.cfg.Listen,
			Handler:           r,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			IdleTimeout:       120 * time.Second,
			// No WriteTimeout: the board stream is long-lived.
			MaxHeaderBytes: 64 << 10,
		}
		errc := make(chan error, 1)
		go func() {
			if e.cfg.TLS.CertFile != "" {
				errc <- srv.ListenAndServeTLS(e.cfg.ResolvePath(e.cfg.TLS.CertFile), e.cfg.ResolvePath(e.cfg.TLS.KeyFile))
			} else {
				errc <- srv.ListenAndServe()
			}
		}()
		log.Info("http listening", "addr", e.cfg.Listen)
		select {
		case err := <-errc:
			if !errors.Is(err, http.ErrServerClosed) {
				return err
			}
		case <-ctx.Done():
		}
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	},
}

const llmsTxt = `# LANLeecher

> Counts download and upload per client (MAC address) for each LAN party event via SNMP from the
> switches, and serves rankings through a web UI and a REST API.

Key concepts:
- Event: a LAN party. Traffic is only booked while an event is running; at most one is active.
- Device: one or more MACs (merged by an admin) that belong to one machine.
- Port group: a switch port with several clients (small switch); one shared ranking entry.
- Port roles: client, multi-client, server (hidden by default), uplink (never attributed to a MAC).
- Hidden: ports/devices are always counted; hiding only removes them from rankings.
- Direction: bytes_down = to the client (ifHCOutOctets of the port), bytes_up = from the client (ifHCInOctets).
- Only deltas between polls are stored; a counter reset on a switch never reduces a total.

Units: bytes as integers, throughput in bit/s, timestamps RFC 3339 UTC, MACs as aa:bb:cc:dd:ee:ff.
Auth: Authorization: Bearer llk_... with scopes read:rankings, read:clients, write:events, read:board.
Errors: application/problem+json (RFC 9457) with a stable "code".

- [OpenAPI spec (JSON)](/api/openapi.json): single source of truth for the API
- [API docs](/api/)
- [Top downloaders](/api/v1/rankings/download?limit=10)
- [Beamer data](/api/v1/board)
`
