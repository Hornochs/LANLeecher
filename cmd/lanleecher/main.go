// Command lanleecher counts per-client LAN traffic via SNMP and serves
// rankings through a web UI and a REST API.
package main

import (
	"os"

	"github.com/lan-dot-party/lanleecher/cmd/lanleecher/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
