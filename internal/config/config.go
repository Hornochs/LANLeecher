// Package config loads and validates the LANLeecher YAML configuration.
//
// Only process-level settings live here. Switches, ports, users and tokens
// are stored in the database and managed through the web UI.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultPath is used when neither --config nor LANLEECHER_CONFIG is set.
const DefaultPath = "/etc/lanleecher/config.yaml"

// Config is the root of config.yaml.
type Config struct {
	// Listen is the HTTP listen address, e.g. "127.0.0.1:9040".
	Listen string `yaml:"listen"`
	// PublicURL is the externally visible base URL. If it starts with
	// https://, session cookies are marked Secure.
	PublicURL string `yaml:"public_url"`
	// TrustedProxies are the proxy IPs or networks whose X-Real-IP /
	// X-Forwarded-For headers are trusted for client IPs (rate limits, audit log).
	TrustedProxies []string `yaml:"trusted_proxies"`
	// SecretKeyFile is the file with the AES-256 key that encrypts credentials
	// in the database (64 hex characters). It is created if missing.
	SecretKeyFile string `yaml:"secret_key_file"`

	// Storage configures the SQLite database.
	Storage Storage `yaml:"storage"`
	// Log configures logging.
	Log Log `yaml:"log"`
	// Polling holds the polling defaults for switches.
	Polling Polling `yaml:"polling"`
	// Resolver configures IP and hostname discovery.
	Resolver Resolver `yaml:"resolver"`
	// Board configures the beamer view.
	Board Board `yaml:"board"`
	// API configures the REST API.
	API API `yaml:"api"`
	// TLS enables the built-in TLS listener; usually a reverse proxy does this.
	TLS TLS `yaml:"tls"`

	// dir is the directory of the loaded file; relative paths resolve against it.
	dir string
}

// Storage configures the SQLite database.
type Storage struct {
	// Path is the SQLite database file. Its directory is created if missing.
	Path string `yaml:"path"`
}

// Log configures log/slog output.
type Log struct {
	// Level is the minimum log level.
	Level string `yaml:"level" enum:"debug,info,warn,error"`
	// Format is the log output format.
	Format string `yaml:"format" enum:"text,json"`
}

// Polling holds defaults for switches that do not override them.
type Polling struct {
	// Interval is the time between two counter polls of a switch. Switches can
	// override it.
	Interval time.Duration `yaml:"interval"`
	// FDBInterval is the time between two reads of a switch's forwarding table
	// (MAC → port). It is never shorter than polling.interval.
	FDBInterval time.Duration `yaml:"fdb_interval"`
	// Timeout is the timeout of a single SNMP request.
	Timeout time.Duration `yaml:"timeout"`
	// Retries is the number of retries of a single SNMP request.
	Retries int `yaml:"retries"`
	// UplinkMACThreshold marks ports with more MACs than this as "probably
	// uplink".
	UplinkMACThreshold int `yaml:"uplink_mac_threshold"`
}

// Resolver configures IP and hostname discovery.
type Resolver struct {
	// Order lists the enabled resolvers. An empty list turns IP and hostname
	// discovery off.
	Order []string `yaml:"order" enum:"snmp-arp,local-neigh,netbios,mdns,llmnr,dns-ptr"`
	// HostnamePriority ranks hostname sources when choosing the displayed name;
	// earlier sources win.
	HostnamePriority []string `yaml:"hostname_priority" enum:"dhcp,netbios,mdns,llmnr,dns-ptr"`
	// ProbeInterval is how often NetBIOS, mDNS, LLMNR and DNS are asked again for
	// the name of each active IP. New IPs are probed immediately.
	ProbeInterval time.Duration `yaml:"probe_interval"`
	// ProbeRate is the maximum number of name queries per second.
	ProbeRate int `yaml:"probe_rate"`
	// ProbeTimeout is the timeout of a single NetBIOS, mDNS or LLMNR query.
	ProbeTimeout time.Duration `yaml:"probe_timeout"`
}

// Board configures the beamer / scoreboard page.
type Board struct {
	// Enabled turns on the beamer view at /board.
	Enabled bool `yaml:"enabled"`
	// Public makes /board and the board API reachable without login or token.
	Public bool `yaml:"public"`
}

// API configures the REST API.
type API struct {
	// MetricsEnabled serves Prometheus metrics at /api/v1/metrics.
	MetricsEnabled bool `yaml:"metrics_enabled"`
	// RateLimit is the sustained number of API requests per second per token
	// or logged-in user.
	RateLimit float64 `yaml:"rate_limit"`
	// RateBurst is the number of requests a token may send at once.
	RateBurst int `yaml:"rate_burst"`
}

// TLS enables the built-in TLS listener. Usually a reverse proxy does this.
type TLS struct {
	// CertFile is the PEM certificate (chain). Set together with tls.key_file.
	CertFile string `yaml:"cert_file"`
	// KeyFile is the PEM private key.
	KeyFile string `yaml:"key_file"`
}

// Default returns a configuration with all defaults applied.
func Default() *Config {
	c := base()
	c.applyDefaults()
	return c
}

// base holds defaults that cannot be told apart from an unset value after
// unmarshalling (booleans that default to true).
func base() *Config {
	return &Config{API: API{MetricsEnabled: true}}
}

func (c *Config) applyDefaults() {
	if c.Listen == "" {
		c.Listen = "127.0.0.1:9040"
	}
	if c.PublicURL == "" {
		c.PublicURL = "http://" + c.Listen
	}
	if c.TrustedProxies == nil {
		c.TrustedProxies = []string{"127.0.0.1", "::1"}
	}
	if c.SecretKeyFile == "" {
		c.SecretKeyFile = "secret.key"
	}
	if c.Storage.Path == "" {
		c.Storage.Path = "data/lanleecher.db"
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Log.Format == "" {
		c.Log.Format = "text"
	}
	if c.Polling.Interval == 0 {
		c.Polling.Interval = 30 * time.Second
	}
	if c.Polling.FDBInterval == 0 {
		c.Polling.FDBInterval = 60 * time.Second
	}
	if c.Polling.Timeout == 0 {
		c.Polling.Timeout = 3 * time.Second
	}
	if c.Polling.Retries == 0 {
		c.Polling.Retries = 1
	}
	if c.Polling.UplinkMACThreshold == 0 {
		c.Polling.UplinkMACThreshold = 10
	}
	if c.Resolver.Order == nil {
		c.Resolver.Order = []string{"snmp-arp", "local-neigh", "netbios", "mdns", "dns-ptr"}
	}
	if c.Resolver.HostnamePriority == nil {
		c.Resolver.HostnamePriority = []string{"dhcp", "netbios", "mdns", "llmnr", "dns-ptr"}
	}
	if c.Resolver.ProbeInterval == 0 {
		c.Resolver.ProbeInterval = 10 * time.Minute
	}
	if c.Resolver.ProbeRate == 0 {
		c.Resolver.ProbeRate = 20
	}
	if c.Resolver.ProbeTimeout == 0 {
		c.Resolver.ProbeTimeout = time.Second
	}
	if c.API.RateLimit == 0 {
		c.API.RateLimit = 10
	}
	if c.API.RateBurst == 0 {
		c.API.RateBurst = 30
	}
}

// Load reads and validates the file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	c := base()
	if err := yaml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	c.dir = filepath.Dir(abs)
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return c, nil
}

// Validate checks value ranges and combinations.
func (c *Config) Validate() error {
	var errs []error
	if u, err := url.Parse(c.PublicURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		errs = append(errs, fmt.Errorf("public_url %q: want http(s)://host", c.PublicURL))
	}
	if c.Polling.Interval < 5*time.Second {
		errs = append(errs, errors.New("polling.interval must be at least 5s"))
	}
	if c.Polling.FDBInterval < c.Polling.Interval {
		errs = append(errs, errors.New("polling.fdb_interval must not be shorter than polling.interval"))
	}
	if (c.TLS.CertFile == "") != (c.TLS.KeyFile == "") {
		errs = append(errs, errors.New("tls.cert_file and tls.key_file must be set together"))
	}
	return errors.Join(errs...)
}

// SecureCookies reports whether session cookies get the Secure flag.
func (c *Config) SecureCookies() bool {
	return strings.HasPrefix(c.PublicURL, "https://")
}

// ResolvePath makes p absolute relative to the config file's directory.
func (c *Config) ResolvePath(p string) string {
	if p == "" || filepath.IsAbs(p) || c.dir == "" {
		return p
	}
	return filepath.Join(c.dir, p)
}

// Example is written by "lanleecher config init".
const Example = `# LANLeecher configuration
# Switches, ports, users and API tokens live in the database
# and are managed in the web UI.

# HTTP listen address. Behind a reverse proxy, bind to localhost only.
listen: 127.0.0.1:9040

# Public URL. With https://, session cookies are marked Secure.
public_url: http://127.0.0.1:9040

# Proxies whose X-Real-IP/X-Forwarded-For headers are trusted.
trusted_proxies: [127.0.0.1, "::1"]

# AES-256 key for credentials in the database (created if missing).
# Relative paths are relative to this file.
secret_key_file: secret.key

storage:
  path: data/lanleecher.db    # SQLite database file

log:
  level: info                 # debug, info, warn, error
  format: text                # text or json

polling:
  interval: 30s               # counter polls, can be overridden per switch
  fdb_interval: 60s           # forwarding table (MAC -> port)
  timeout: 3s
  retries: 1
  uplink_mac_threshold: 10    # more MACs than this: "probably uplink"

resolver:
  order: [snmp-arp, local-neigh, netbios, mdns, dns-ptr]
  hostname_priority: [dhcp, netbios, mdns, llmnr, dns-ptr]
  probe_interval: 10m
  probe_rate: 20              # name queries per second
  probe_timeout: 1s

board:
  enabled: false              # beamer view at /board
  public: false               # reachable without login or token

api:
  metrics_enabled: true       # /api/v1/metrics (Prometheus)
  rate_limit: 10              # requests per second and token
  rate_burst: 30

# Built-in TLS (otherwise use a reverse proxy):
# tls:
#   cert_file: /etc/lanleecher/tls.crt
#   key_file: /etc/lanleecher/tls.key
`
