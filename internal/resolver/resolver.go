// Package resolver finds IP addresses and hostnames for MACs.
//
// Two kinds of resolvers exist:
//   - Address sources (snmp-arp, local-neigh) deliver MAC/IP pairs.
//   - Name probes (netbios, mdns, llmnr, dns-ptr) ask an IP for its name.
//
// Every result lands in the history tables with its source. New MAC/IP pairs
// trigger name probes right away; afterwards every active IP is re-probed
// at probe_interval, rate-limited to probe_rate queries per second.
package resolver

import (
	"context"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/config"
	"github.com/lan-dot-party/lanleecher/internal/macaddr"
	"github.com/lan-dot-party/lanleecher/internal/store"
)

// Pair is a MAC/IP association from an address source.
type Pair struct {
	MAC macaddr.MAC
	IP  netip.Addr
}

// Prober asks an IP for its name.
type Prober interface {
	// Name is the source name stored in the history (e.g. "netbios").
	Name() string
	Probe(ctx context.Context, ip netip.Addr) (string, error)
}

// Service coordinates resolvers.
type Service struct {
	db      *store.DB
	cfg     config.Resolver
	log     *slog.Logger
	enabled map[string]bool
	probers []Prober
	queue   chan Pair

	mu        sync.Mutex
	lastProbe map[netip.Addr]time.Time
}

// New creates the service for the configured resolver order.
func New(db *store.DB, cfg config.Resolver, log *slog.Logger) *Service {
	s := &Service{
		db:        db,
		cfg:       cfg,
		log:       log.With("component", "resolver"),
		enabled:   map[string]bool{},
		queue:     make(chan Pair, 4096),
		lastProbe: map[netip.Addr]time.Time{},
	}
	for _, name := range cfg.Order {
		s.enabled[name] = true
		switch name {
		case "netbios":
			s.probers = append(s.probers, NetBIOS{Timeout: cfg.ProbeTimeout})
		case "mdns":
			s.probers = append(s.probers, MDNS{Timeout: cfg.ProbeTimeout})
		case "llmnr":
			s.probers = append(s.probers, LLMNR{Timeout: cfg.ProbeTimeout})
		case "dns-ptr":
			s.probers = append(s.probers, DNSPTR{Timeout: 2 * cfg.ProbeTimeout})
		}
	}
	return s
}

// Enabled reports whether a resolver is configured.
func (s *Service) Enabled(name string) bool { return s.enabled[name] }

// ObserveARP records MAC/IP pairs for known MACs and queues name probes for
// new pairs. Unknown MACs (never seen on a switch port) are ignored.
func (s *Service) ObserveARP(ctx context.Context, pairs []Pair, source string) {
	if !s.enabled[source] {
		return
	}
	now := store.Now()
	for _, p := range pairs {
		if !p.IP.IsValid() || p.IP.IsUnspecified() || p.IP.IsMulticast() || p.MAC.IsZero() || p.MAC.IsMulticast() {
			continue
		}
		mac := p.MAC.String()
		known, err := s.db.MACKnown(ctx, mac)
		if err != nil {
			s.log.Warn("lookup mac", "err", err)
			return
		}
		if !known {
			continue
		}
		isNew, err := s.db.RecordIP(ctx, mac, p.IP.String(), source, now)
		if err != nil {
			s.log.Warn("record ip", "mac", mac, "err", err)
			continue
		}
		if isNew {
			s.log.Debug("new ip", "mac", mac, "ip", p.IP, "source", source)
			s.enqueue(p)
		}
	}
}

// DHCPLease is an active lease from a DHCP server.
type DHCPLease struct {
	MAC      macaddr.MAC
	IP       netip.Addr
	Hostname string
}

// ObserveDHCP records IPs and client hostnames from DHCP leases for known
// MACs, with source "dhcp". It works regardless of resolver.order: a
// configured DHCP source is always used. New MAC/IP pairs are probed
// immediately. It returns the number of leases of known MACs.
func (s *Service) ObserveDHCP(ctx context.Context, leases []DHCPLease) int {
	now := store.Now()
	known := 0
	for _, l := range leases {
		if !l.IP.IsValid() || l.MAC.IsZero() || l.MAC.IsMulticast() {
			continue
		}
		mac := l.MAC.String()
		ok, err := s.db.MACKnown(ctx, mac)
		if err != nil {
			s.log.Warn("lookup mac", "err", err)
			return known
		}
		if !ok {
			continue
		}
		known++
		isNew, err := s.db.RecordIP(ctx, mac, l.IP.String(), "dhcp", now)
		if err != nil {
			s.log.Warn("record ip", "mac", mac, "err", err)
			continue
		}
		if isNew {
			s.enqueue(Pair{MAC: l.MAC, IP: l.IP})
		}
		if name := cleanHostname(l.Hostname); name != "" {
			s.recordHostname(ctx, mac, name, "dhcp")
		}
	}
	return known
}

func (s *Service) enqueue(p Pair) {
	if len(s.probers) == 0 || p.IP.IsLoopback() {
		return
	}
	select {
	case s.queue <- p:
	default:
		s.log.Debug("probe queue full, dropping", "ip", p.IP)
	}
}

// Run starts the local-neigh reader, the periodic re-probe and the probe worker.
func (s *Service) Run(ctx context.Context) {
	if s.enabled["local-neigh"] {
		go s.every(ctx, time.Minute, func() {
			pairs, err := ReadLocalNeigh()
			if err != nil {
				s.log.Debug("local-neigh", "err", err)
				return
			}
			s.ObserveARP(ctx, pairs, "local-neigh")
		})
	}
	if len(s.probers) == 0 {
		return
	}
	go s.every(ctx, time.Minute, func() { s.reprobe(ctx) })
	s.worker(ctx)
}

func (s *Service) every(ctx context.Context, d time.Duration, fn func()) {
	fn()
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn()
		}
	}
}

// reprobe queues every IP seen recently that was not probed within the interval.
func (s *Service) reprobe(ctx context.Context) {
	active, err := s.db.ActiveIPs(ctx, time.Now().Add(-s.cfg.ProbeInterval))
	if err != nil {
		s.log.Warn("active ips", "err", err)
		return
	}
	for _, a := range active {
		ip, err := netip.ParseAddr(a.IP)
		if err != nil {
			continue
		}
		s.mu.Lock()
		due := time.Since(s.lastProbe[ip]) >= s.cfg.ProbeInterval
		s.mu.Unlock()
		if !due {
			continue
		}
		if mac, err := macaddr.Parse(a.MAC); err == nil {
			s.enqueue(Pair{MAC: mac, IP: ip})
		}
	}
}

func (s *Service) worker(ctx context.Context) {
	rate := s.cfg.ProbeRate
	if rate <= 0 {
		rate = 20
	}
	limiter := time.NewTicker(time.Second / time.Duration(rate))
	defer limiter.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case p := <-s.queue:
			s.mu.Lock()
			s.lastProbe[p.IP] = time.Now()
			s.mu.Unlock()
			for _, pr := range s.probers {
				select {
				case <-ctx.Done():
					return
				case <-limiter.C:
				}
				name, err := pr.Probe(ctx, p.IP)
				if err != nil || name == "" {
					continue
				}
				s.recordHostname(ctx, p.MAC.String(), cleanHostname(name), pr.Name())
			}
		}
	}
}

func (s *Service) recordHostname(ctx context.Context, mac, name, source string) {
	if name == "" || strings.EqualFold(name, "localhost") {
		return
	}
	isNew, err := s.db.RecordHostname(ctx, mac, name, source, store.Now())
	if err != nil {
		s.log.Warn("record hostname", "mac", mac, "err", err)
		return
	}
	if !isNew {
		return
	}
	s.log.Info("hostname found", "mac", mac, "hostname", name, "source", source)
	s.suggestMerges(ctx, mac, name)
}

// suggestMerges proposes merging MACs that had the same hostname in the
// active event. It never merges automatically: names like DESKTOP-ABC123 or
// PC are not unique, and randomized MACs can mislead.
func (s *Service) suggestMerges(ctx context.Context, mac, name string) {
	ev, err := s.db.ActiveEvent(ctx)
	if err != nil {
		return
	}
	others, err := s.db.MACsWithHostname(ctx, name, mac, ev.StartedAt)
	if err != nil {
		s.log.Warn("merge lookup", "err", err)
		return
	}
	dev, err := s.db.DeviceOfMAC(ctx, mac)
	if err != nil {
		return
	}
	for _, other := range others {
		od, err := s.db.DeviceOfMAC(ctx, other)
		if err != nil || od == dev {
			continue
		}
		created, err := s.db.CreateMergeSuggestion(ctx, mac, other, "hostname:"+name, ev.ID)
		if err == nil && created {
			s.log.Info("merge suggested", "mac_a", mac, "mac_b", other, "hostname", name)
		}
	}
}

// cleanHostname trims whitespace, NUL padding and a trailing dot.
func cleanHostname(s string) string {
	s = strings.TrimSpace(strings.TrimRight(s, "\x00 "))
	s = strings.TrimSuffix(s, ".")
	if len(s) > 253 {
		s = s[:253]
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}
