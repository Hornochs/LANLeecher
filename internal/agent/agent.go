package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/lan-dot-party/lanleecher/internal/agent/collect"
	"github.com/lan-dot-party/lanleecher/internal/tlsclient"
)

// Report is the JSON body of POST /api/v1/agent/report (see openapi.yaml).
type Report struct {
	AgentVersion string            `json:"agent_version"`
	Hostname     string            `json:"hostname"`
	Mode         string            `json:"mode"`
	Interfaces   []ReportInterface `json:"interfaces"`
}

// ReportInterface is one interface of a report.
type ReportInterface struct {
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

// Response is LANLeecher's answer.
type Response struct {
	Accepted  int `json:"accepted"`
	Conflicts int `json:"conflicts"`
	Ignored   int `json:"ignored"`
}

// maxInterfaces is the server's limit per report.
const maxInterfaces = 512

// Agent collects and sends.
type Agent struct {
	cfg     *Config
	log     *slog.Logger
	http    *http.Client
	version string
	// Root is the filesystem root for the collectors ("" = /).
	Root string
}

// New creates an agent with an HTTP client that verifies the server as
// configured: system roots (+ tls.ca_file) and hostname by default, a pinned
// certificate, or nothing when insecure_skip_verify is set.
func New(cfg *Config, log *slog.Logger, version string) (*Agent, error) {
	u, err := url.Parse(cfg.Server)
	if err != nil {
		return nil, err
	}
	opts := tlsclient.Options{Host: u.Hostname(), PinnedSHA256: cfg.TLS.PinnedSHA256,
		InsecureSkipVerify: cfg.TLS.InsecureSkipVerify}
	if cfg.TLS.CAFile != "" {
		pem, err := os.ReadFile(cfg.TLS.CAFile)
		if err != nil {
			return nil, fmt.Errorf("tls.ca_file: %w", err)
		}
		opts.CAPEM = string(pem)
	}
	return &Agent{cfg: cfg, log: log, version: version, http: &http.Client{
		Timeout: cfg.Timeout,
		Transport: &http.Transport{
			TLSClientConfig:     tlsclient.Config(opts, nil),
			Proxy:               http.ProxyFromEnvironment,
			MaxIdleConns:        2,
			IdleConnTimeout:     2 * cfg.Interval,
			TLSHandshakeTimeout: cfg.Timeout,
		},
		// Never follow redirects: the token must not go anywhere else.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// Mode returns the collector mode in use ("linux" or "proxmox").
func (a *Agent) Mode() string {
	if a.cfg.Mode == "auto" {
		return collect.Detect(a.collectOptions())
	}
	return a.cfg.Mode
}

func (a *Agent) collectOptions() collect.Options {
	return collect.Options{Root: a.Root, Include: a.cfg.Interfaces.Include, Exclude: a.cfg.Interfaces.Exclude}
}

// Collect reads the counters once.
func (a *Agent) Collect() ([]collect.Interface, error) {
	return collect.Collect(a.Mode(), a.collectOptions())
}

// BuildReport turns collected interfaces into a report.
func (a *Agent) BuildReport(ifs []collect.Interface) Report {
	host, _ := os.Hostname()
	r := Report{AgentVersion: a.version, Hostname: host, Mode: a.Mode(), Interfaces: []ReportInterface{}}
	for _, in := range ifs {
		if len(r.Interfaces) == maxInterfaces {
			a.log.Warn("more interfaces than one report may carry; the rest is left out", "limit", maxInterfaces)
			break
		}
		ri := ReportInterface{MAC: in.MAC.String(), IfName: in.IfName, Kind: in.Kind, Name: in.Name, VMID: in.VMID,
			VLAN: in.VLAN, DownBytes: in.Down, UpBytes: in.Up}
		for _, ip := range in.IPs {
			ri.IPs = append(ri.IPs, ip.String())
		}
		r.Interfaces = append(r.Interfaces, ri)
	}
	return r
}

// StatusError is a non-200 answer of the server.
type StatusError struct {
	Status int
	Detail string
}

func (e *StatusError) Error() string {
	switch e.Status {
	case http.StatusUnauthorized:
		return "the server rejected the token (revoked, expired or wrong): " + e.Detail
	case http.StatusForbidden:
		return "the server refused the report (not an agent token or IP not allowed): " + e.Detail
	}
	return fmt.Sprintf("server answered %d: %s", e.Status, e.Detail)
}

// Send posts one report.
func (a *Agent) Send(ctx context.Context, r Report) (*Response, error) {
	body, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.Endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "lanleecher-agent/"+a.version)
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		var p struct{ Detail, Code string }
		_ = json.Unmarshal(data, &p) // problem+json; the status alone is enough otherwise
		detail := p.Detail
		if detail == "" {
			detail = http.StatusText(resp.StatusCode)
		}
		return nil, &StatusError{Status: resp.StatusCode, Detail: detail}
	}
	var out Response
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("unexpected answer: %w", err)
	}
	return &out, nil
}

// Run collects and sends every interval until ctx ends. Failures are
// logged and retried with a growing pause (at most 5 minutes).
func (a *Agent) Run(ctx context.Context) error {
	a.log.Info("lanleecher-agent started", "server", a.cfg.Server, "mode", a.Mode(), "interval", a.cfg.Interval)
	backoff := time.Duration(0)
	for {
		wait := a.cfg.Interval
		if err := a.once(ctx); err != nil {
			var ce *tlsclient.CertError
			switch {
			case errors.As(err, &ce):
				a.log.Error("server certificate rejected", "err", err)
			default:
				a.log.Error("report failed", "err", err)
			}
			if backoff == 0 {
				backoff = a.cfg.Interval
			} else {
				backoff = min(2*backoff, 5*time.Minute)
			}
			wait = backoff
		} else {
			backoff = 0
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
}

func (a *Agent) once(ctx context.Context) error {
	ifs, err := a.Collect()
	if err != nil {
		return fmt.Errorf("collect: %w", err)
	}
	res, err := a.Send(ctx, a.BuildReport(ifs))
	if err != nil {
		return err
	}
	a.log.Debug("report sent", "interfaces", len(ifs), "accepted", res.Accepted, "conflicts", res.Conflicts,
		"ignored", res.Ignored)
	if res.Conflicts > 0 {
		a.log.Warn("another agent reports some of these MACs; LANLeecher ignores them", "macs", res.Conflicts)
	}
	return nil
}
