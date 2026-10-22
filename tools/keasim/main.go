// Command keasim simulates the Kea management API, so LANLeecher's DHCP
// integration can be tried without a Kea server.
//
//	go run ./tools/keasim -mode agent -user lanleecher -password secret-password
//	go run ./tools/keasim -mode dhcp4 -addr 127.0.0.1:8004      # Kea 3 style
//
// It serves leases for the demo switch (testdata/snmpsim/demo.snmprec) over
// HTTPS with a fresh self-signed certificate (a new one on every start, which
// is handy to try the "certificate changed" warning) and prints its SHA-256
// fingerprint.
package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/kea"
	"github.com/lan-dot-party/lanleecher/internal/kea/keasim"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8000", "listen address")
	mode := flag.String("mode", "agent", "agent (Kea < 3 Control Agent), dhcp4 or dhcp6 (Kea >= 3 server socket)")
	version := flag.String("version", "", "version to report (default 2.6.3 for agent, 3.0.1 otherwise)")
	user := flag.String("user", "", "HTTP basic auth user (empty: no authentication)")
	password := flag.String("password", "", "HTTP basic auth password")
	plain := flag.Bool("http", false, "serve plain HTTP instead of HTTPS (LANLeecher allows this for localhost only)")
	noHook := flag.Bool("no-lease-cmds", false, "simulate a server without the lease_cmds hook")
	flag.Parse()

	if *version == "" {
		*version = "3.0.1"
		if *mode == "agent" {
			*version = "2.6.3"
		}
	}
	sim := &keasim.Server{Mode: *mode, Version: *version, Username: *user, Password: *password, NoLeaseCmds: *noHook,
		Leases4: keasim.DemoLeases()}
	srv := &http.Server{Addr: *addr, Handler: sim, ReadHeaderTimeout: 10 * time.Second}
	if *plain {
		log.Printf("keasim (%s, Kea %s) on http://%s/", *mode, *version, *addr)
		log.Fatal(srv.ListenAndServe())
	}
	pair, cert, err := keasim.SelfSigned("127.0.0.1", "localhost")
	if err != nil {
		log.Fatal(err)
	}
	srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	fmt.Printf("keasim (%s, Kea %s) on https://%s/\nSHA-256: %s\n", *mode, *version, *addr,
		kea.FormatFingerprint(kea.Fingerprint(cert)))
	log.Fatal(srv.ListenAndServeTLS("", ""))
}
