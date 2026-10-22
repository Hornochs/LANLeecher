package kea

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/macaddr"
	"github.com/lan-dot-party/lanleecher/internal/resolver"
	"github.com/lan-dot-party/lanleecher/internal/secret"
	"github.com/lan-dot-party/lanleecher/internal/store"
)

// Secrets are stored encrypted in dhcp_sources.secrets_enc.
type Secrets struct {
	Password     string `json:"password,omitempty"`
	ClientKeyPEM string `json:"client_key_pem,omitempty"`
}

// Sink receives leases (the resolver service).
type Sink interface {
	ObserveDHCP(ctx context.Context, leases []resolver.DHCPLease) int
}

// Manager polls all enabled DHCP sources.
type Manager struct {
	db   *store.DB
	key  *secret.Key
	sink Sink
	log  *slog.Logger

	mu    sync.Mutex
	ctx   context.Context
	loops map[int64]*loop
}

type loop struct {
	cancel      context.CancelFunc
	fingerprint string
}

// NewManager creates the DHCP manager.
func NewManager(db *store.DB, key *secret.Key, sink Sink, log *slog.Logger) *Manager {
	return &Manager{db: db, key: key, sink: sink, log: log.With("component", "dhcp"), loops: map[int64]*loop{}}
}

// Start begins polling.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	m.ctx = ctx
	m.mu.Unlock()
	return m.Reload(ctx)
}

// Reload starts, stops or restarts loops to match the database.
func (m *Manager) Reload(ctx context.Context) error {
	sources, err := m.db.ListDHCPSources(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx == nil {
		return nil
	}
	want := map[int64]*store.DHCPSource{}
	for _, s := range sources {
		if s.Enabled {
			want[s.ID] = s
		}
	}
	for id, l := range m.loops {
		if s, ok := want[id]; !ok || fingerprint(s) != l.fingerprint {
			l.cancel()
			delete(m.loops, id)
		}
	}
	for id, s := range want {
		if _, running := m.loops[id]; running {
			continue
		}
		ctx, cancel := context.WithCancel(m.ctx)
		m.loops[id] = &loop{cancel: cancel, fingerprint: fingerprint(s)}
		go m.run(ctx, s)
	}
	return nil
}

func fingerprint(s *store.DHCPSource) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%v|%v|%s|%s|%s|%v|%d|", s.URL, s.Kind, s.DHCP4, s.DHCP6, s.Username, s.CAPEM,
		s.PinnedSHA256, s.InsecureSkipVerify, s.PollInterval)
	h.Write(s.SecretsEnc)
	h.Write([]byte(s.ClientCertPEM))
	return hex.EncodeToString(h.Sum(nil))
}

// ClientFor builds a client for a source, decrypting its secrets.
func (m *Manager) ClientFor(s *store.DHCPSource) (*Client, error) {
	var sec Secrets
	if len(s.SecretsEnc) > 0 {
		plain, err := m.key.Decrypt(s.SecretsEnc)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(plain, &sec); err != nil {
			return nil, err
		}
	}
	return New(Config{URL: s.URL, Kind: Kind(s.Kind), DHCP4: s.DHCP4, DHCP6: s.DHCP6, Username: s.Username,
		Password: sec.Password, CAPEM: s.CAPEM, ClientCertPEM: s.ClientCertPEM, ClientKeyPEM: sec.ClientKeyPEM,
		PinnedSHA256: s.PinnedSHA256, InsecureSkipVerify: s.InsecureSkipVerify})
}

func (m *Manager) run(ctx context.Context, s *store.DHCPSource) {
	log := m.log.With("source", s.Name)
	interval := time.Duration(s.PollInterval) * time.Second
	if interval < 10*time.Second {
		interval = 30 * time.Second
	}
	if s.InsecureSkipVerify {
		log.Warn("certificate verification is DISABLED for this DHCP source")
	}
	log.Info("dhcp polling started", "url", s.URL, "kind", s.Kind, "interval", interval)
	t := time.NewTicker(interval)
	defer t.Stop()
	lastErr := ""
	for {
		res := m.Poll(ctx, s)
		if ctx.Err() != nil {
			return
		}
		if res.Err != "" && res.Err != lastErr {
			log.Warn("dhcp poll failed", "err", res.Err)
		} else if res.Err == "" && lastErr != "" {
			log.Info("dhcp poll recovered")
		}
		lastErr = res.Err
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Result is the outcome of a poll or connection test.
type Result struct {
	OK         bool
	Err        string
	CertErr    *CertError
	Version    string
	TLSVersion string
	// Leases counts active leases per family.
	Leases map[string]int
	// Known counts leases of MACs seen on a switch port.
	Known int
	// Cert is the server certificate of the connection (nil for plain HTTP).
	Cert *CertInfo
}

// Poll reads all leases of a source, hands them to the sink and stores the
// status. A connection test is the same poll, shown to the admin.
func (m *Manager) Poll(ctx context.Context, s *store.DHCPSource) Result {
	res := Result{Leases: map[string]int{}}
	c, err := m.ClientFor(s)
	if err != nil {
		res.Err = err.Error()
		m.save(ctx, s.ID, res, nil)
		return res
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	res.Version, err = c.Version(ctx)
	var leases []resolver.DHCPLease
	now := time.Now()
	if err == nil {
		for _, fam := range c.Families() {
			err = c.Leases(ctx, fam, func(l Lease) error {
				if !l.Active(now) {
					return nil
				}
				res.Leases[fam]++
				mac, merr := macaddr.Parse(l.HWAddress)
				ip, ierr := netip.ParseAddr(l.IP)
				if merr == nil && ierr == nil {
					leases = append(leases, resolver.DHCPLease{MAC: mac, IP: ip, Hostname: l.Hostname})
				}
				return nil
			})
			if err != nil {
				err = fmt.Errorf("%s: %w", fam, err)
				break
			}
		}
	}
	if chain := c.Presented(); len(chain) > 0 {
		info := Info(chain[0], now)
		res.Cert = &info
	}
	res.TLSVersion = c.TLSVersion()
	if err != nil {
		res.Err = err.Error()
		errors.As(err, &res.CertErr)
	} else {
		res.OK = true
		if m.sink != nil {
			res.Known = m.sink.ObserveDHCP(ctx, leases)
		}
	}
	m.save(ctx, s.ID, res, c)
	return res
}

func (m *Manager) save(ctx context.Context, id int64, res Result, c *Client) {
	st := store.DHCPStatus{OK: res.OK, Error: res.Err, KeaVersion: res.Version}
	for _, n := range res.Leases {
		st.LeaseCount += n
	}
	if res.CertErr != nil {
		st.CertProblem = res.CertErr.Problem
	}
	if c != nil {
		if chain := c.Presented(); len(chain) > 0 {
			st.PresentedCertPEM = EncodePEM(chain[0])
		}
	}
	if err := m.db.SetDHCPStatus(context.WithoutCancel(ctx), id, st, store.Now()); err != nil {
		m.log.Error("save dhcp status", "err", err)
	}
}
