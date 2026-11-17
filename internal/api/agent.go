package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"unicode/utf8"

	"github.com/lan-dot-party/lanleecher/internal/agentpool"
	"github.com/lan-dot-party/lanleecher/internal/macaddr"
	"github.com/lan-dot-party/lanleecher/internal/resolver"
	"github.com/lan-dot-party/lanleecher/internal/store"
)

// Limits of an agent report.
const (
	maxAgentBody       = 256 << 10
	maxAgentInterfaces = 512
	maxAgentIPs        = 16
)

// AgentInterfaceJSON is one interface in an agent report. Counters are
// absolute and in the MAC's point of view: down_bytes is traffic to the MAC.
type AgentInterfaceJSON struct {
	MAC       string   `json:"mac"`
	IfName    string   `json:"ifname"`
	Kind      string   `json:"kind"`
	Name      string   `json:"name,omitempty"`
	VMID      int      `json:"vmid,omitempty"`
	VLAN      int      `json:"vlan,omitempty"`
	DownBytes uint64   `json:"down_bytes"`
	UpBytes   uint64   `json:"up_bytes"`
	IPs       []string `json:"ips,omitempty"`
}

// AgentReportJSON is the body of POST /v1/agent/report.
type AgentReportJSON struct {
	AgentVersion string               `json:"agent_version"`
	Hostname     string               `json:"hostname"`
	Mode         string               `json:"mode"`
	Interfaces   []AgentInterfaceJSON `json:"interfaces"`
}

func shortText(s string, max int) bool {
	if len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func (a *API) agentReport(w http.ResponseWriter, r *http.Request) {
	tok := principalOf(r.Context()).token
	if tok == nil {
		problem(w, http.StatusForbidden, "insufficient_scope", "Forbidden", "agent reports need an agent token")
		return
	}
	isAgent, err := a.DB.IsAgentToken(r.Context(), tok.ID)
	if err != nil {
		a.internal(w, r, err)
		return
	}
	if !isAgent {
		problem(w, http.StatusForbidden, "insufficient_scope", "Forbidden", "this token is not an agent token")
		return
	}
	var body AgentReportJSON
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAgentBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			problem(w, http.StatusRequestEntityTooLarge, "invalid_body", "Payload Too Large", "a report may be at most 256 KiB")
			return
		}
		problem(w, http.StatusBadRequest, "invalid_body", "Bad Request", "expected a JSON agent report")
		return
	}
	switch {
	case !shortText(body.AgentVersion, 64) || !shortText(body.Hostname, 253) || !shortText(body.Mode, 16):
		problem(w, http.StatusBadRequest, "invalid_body", "Bad Request", "agent_version, hostname or mode invalid or too long")
		return
	case len(body.Interfaces) > maxAgentInterfaces:
		problem(w, http.StatusBadRequest, "invalid_body", "Bad Request", "at most 512 interfaces per report")
		return
	}

	ifs := make([]agentpool.Interface, 0, len(body.Interfaces))
	entries := make([]resolver.AgentEntry, 0, len(body.Interfaces))
	ignored := 0
	for _, in := range body.Interfaces {
		mac, err := macaddr.Parse(in.MAC)
		if err != nil || mac.IsZero() || mac.IsMulticast() || !shortText(in.IfName, 64) || in.IfName == "" ||
			!shortText(in.Name, 128) || len(in.IPs) > maxAgentIPs || in.VMID < 0 || in.VLAN < 0 || in.VLAN > 4095 {
			ignored++
			continue
		}
		switch in.Kind {
		case "host", "vm", "ct":
		default:
			ignored++
			continue
		}
		ifs = append(ifs, agentpool.Interface{MAC: mac, IfName: in.IfName, Kind: in.Kind, Name: in.Name, VMID: in.VMID,
			Down: in.DownBytes, Up: in.UpBytes})
		e := resolver.AgentEntry{MAC: mac, Hostname: in.Name}
		if in.Kind == "host" {
			e.Hostname = body.Hostname
		}
		for _, s := range in.IPs {
			if ip, err := netip.ParseAddr(strings.TrimSpace(s)); err == nil && resolver.UsableAgentIP(ip) {
				e.IPs = append(e.IPs, ip.Unmap())
			}
		}
		entries = append(entries, e)
	}

	now := store.Now()
	res := agentpool.ReportResult{}
	if a.Agents != nil {
		res = a.Agents.Report(tok.ID, now, ifs)
	}
	if err := a.DB.RecordAgentReport(r.Context(), tok.ID, body.Hostname, body.Mode, body.AgentVersion, a.ClientIP(r),
		len(ifs), now); err != nil {
		a.internal(w, r, err)
		return
	}
	if a.Resolver != nil {
		a.Resolver.ObserveAgent(r.Context(), entries)
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": res.Accepted, "conflicts": res.Conflicts, "ignored": ignored})
}
