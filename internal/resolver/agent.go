package resolver

import (
	"context"
	"net/netip"
	"sync"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/macaddr"
	"github.com/lan-dot-party/lanleecher/internal/store"
)

// SourceAgent is the IP/hostname source of host agents.
const SourceAgent = "agent"

// AgentEntry is what a host agent knows about one of its MACs.
type AgentEntry struct {
	MAC      macaddr.MAC
	IPs      []netip.Addr
	Hostname string
}

// agentRefresh is how often an unchanged agent IP or hostname is written
// again (to move last_seen); agents report every few seconds.
const agentRefresh = 10 * time.Minute

type agentSeen struct {
	mu   sync.Mutex
	last map[string]time.Time
}

var agentCache = &agentSeen{last: map[string]time.Time{}}

// due reports whether key should be written now and remembers it.
func (a *agentSeen) due(key string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t, ok := a.last[key]; ok && now.Sub(t) < agentRefresh {
		return false
	}
	if len(a.last) > 100000 {
		for k, t := range a.last {
			if now.Sub(t) >= agentRefresh {
				delete(a.last, k)
			}
		}
	}
	a.last[key] = now
	return true
}

// UsableAgentIP reports addresses worth recording: no link-local,
// loopback, multicast or unspecified addresses (every interface has an
// fe80:: address, and agents must not be able to flood the history).
func UsableAgentIP(ip netip.Addr) bool {
	return ip.IsValid() && !ip.IsLinkLocalUnicast() && !ip.IsLoopback() && !ip.IsMulticast() &&
		!ip.IsUnspecified() && !ip.IsInterfaceLocalMulticast()
}

// ObserveAgent records IPs and hostnames reported by a host agent for MACs
// LANLeecher already knows (seen in a forwarding table), with source
// "agent". Unknown MACs are ignored, so an agent cannot create devices.
func (s *Service) ObserveAgent(ctx context.Context, entries []AgentEntry) {
	now := store.Now()
	for _, e := range entries {
		if e.MAC.IsZero() || e.MAC.IsMulticast() {
			continue
		}
		mac := e.MAC.String()
		ok, err := s.db.MACKnown(ctx, mac)
		if err != nil {
			s.log.Warn("lookup mac", "err", err)
			return
		}
		if !ok {
			continue
		}
		for i, ip := range e.IPs {
			if i >= 8 {
				break
			}
			if !UsableAgentIP(ip) || !agentCache.due(mac+"|ip|"+ip.String(), now) {
				continue
			}
			isNew, err := s.db.RecordIP(ctx, mac, ip.String(), SourceAgent, now)
			if err != nil {
				s.log.Warn("record ip", "mac", mac, "err", err)
				continue
			}
			if isNew {
				s.enqueue(Pair{MAC: e.MAC, IP: ip})
			}
		}
		if name := cleanHostname(e.Hostname); name != "" && agentCache.due(mac+"|name|"+name, now) {
			s.recordHostname(ctx, mac, name, SourceAgent)
		}
	}
}
