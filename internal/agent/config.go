// Package agent is the LANLeecher host agent: it collects byte counters per
// MAC on a Linux host or Proxmox VE node and sends them to LANLeecher.
package agent

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultConfigPath is where the packages install the configuration.
const DefaultConfigPath = "/etc/lanleecher/agent.conf"

// Config is /etc/lanleecher/agent.conf (YAML).
type Config struct {
	// Server is the LANLeecher URL, as in its public_url (https://…). The
	// agent posts to <server>/api/v1/agent/report.
	Server string `yaml:"server"`
	// Token is the agent token (llk_…) from Admin → Agents. Alternatively
	// TokenFile names a file that holds it.
	Token     string `yaml:"token"`
	TokenFile string `yaml:"token_file"`
	// Interval between two reports (5s … 10m).
	Interval time.Duration `yaml:"interval"`
	// Mode selects the collector: auto (Proxmox VE if /etc/pve/local exists,
	// else Linux), linux or proxmox.
	Mode string `yaml:"mode"`
	// Interfaces filters interface names with shell globs (tap*, eno1, …).
	Interfaces struct {
		Include []string `yaml:"include"`
		Exclude []string `yaml:"exclude"`
	} `yaml:"interfaces"`
	TLS struct {
		// CAFile adds a CA (or the server's self-signed certificate) to the
		// system roots. Not needed behind a reverse proxy with a valid
		// certificate.
		CAFile string `yaml:"ca_file"`
		// PinnedSHA256 accepts exactly this server certificate (hex SHA-256,
		// colons allowed), e.g. LANLeecher's built-in TLS with a self-signed
		// certificate. Admin → Agents shows it.
		PinnedSHA256 string `yaml:"pinned_sha256"`
		// InsecureSkipVerify accepts any certificate. Only for tests.
		InsecureSkipVerify bool `yaml:"insecure_skip_verify"`
	} `yaml:"tls"`
	// AllowInsecureHTTP permits an http:// server (token and data in clear
	// text). Only for tests.
	AllowInsecureHTTP bool `yaml:"allow_insecure_http"`
	// Timeout of one request.
	Timeout time.Duration `yaml:"timeout"`
}

// LoadConfig reads and validates a configuration file. Warnings are
// non-fatal findings (insecure options, readable secrets) to log.
func LoadConfig(path string) (*Config, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	var warnings []string
	if c.Token != "" {
		if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o004 != 0 {
			warnings = append(warnings, path+" contains the token and is readable by everyone; use chmod 640")
		}
	}
	if c.TokenFile != "" {
		raw, err := os.ReadFile(c.TokenFile)
		if err != nil {
			return nil, nil, fmt.Errorf("token_file: %w", err)
		}
		if fi, err := os.Stat(c.TokenFile); err == nil && fi.Mode().Perm()&0o004 != 0 {
			warnings = append(warnings, c.TokenFile+" is readable by everyone; use chmod 640")
		}
		if c.Token != "" {
			return nil, nil, errors.New("set token or token_file, not both")
		}
		c.Token = strings.TrimSpace(string(raw))
	}
	w, err := c.validate()
	return &c, append(warnings, w...), err
}

func (c *Config) validate() ([]string, error) {
	var warnings []string
	if c.Interval == 0 {
		c.Interval = 15 * time.Second
	}
	if c.Timeout == 0 {
		c.Timeout = 10 * time.Second
	}
	if c.Mode == "" {
		c.Mode = "auto"
	}
	switch {
	case c.Interval < 5*time.Second || c.Interval > 10*time.Minute:
		return nil, errors.New("interval must be between 5s and 10m")
	case c.Timeout < time.Second || c.Timeout > time.Minute:
		return nil, errors.New("timeout must be between 1s and 1m")
	case c.Mode != "auto" && c.Mode != "linux" && c.Mode != "proxmox":
		return nil, fmt.Errorf("mode must be auto, linux or proxmox, not %q", c.Mode)
	case !strings.HasPrefix(c.Token, "llk_") || len(c.Token) < 20 || strings.ContainsAny(c.Token, " \t\r\n"):
		return nil, errors.New("token missing or not an LANLeecher token (llk_…)")
	}
	u, err := url.Parse(strings.TrimSpace(c.Server))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("server must be a URL like https://lanleecher.example, not %q", c.Server)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !c.AllowInsecureHTTP {
			return nil, errors.New("server uses http://: the token and the data would travel in clear text; " +
				"use https:// or set allow_insecure_http: true (tests only)")
		}
		warnings = append(warnings, "allow_insecure_http is on: token and data travel unencrypted")
	default:
		return nil, fmt.Errorf("server must use https:// (scheme %q)", u.Scheme)
	}
	if c.TLS.InsecureSkipVerify {
		warnings = append(warnings, "tls.insecure_skip_verify is on: any certificate is accepted (man-in-the-middle possible)")
	}
	if c.TLS.InsecureSkipVerify && c.TLS.PinnedSHA256 != "" {
		return nil, errors.New("set tls.pinned_sha256 or tls.insecure_skip_verify, not both")
	}
	c.Server = strings.TrimRight(u.String(), "/")
	return warnings, nil
}

// Endpoint is the report URL.
func (c *Config) Endpoint() string { return c.Server + "/api/v1/agent/report" }
