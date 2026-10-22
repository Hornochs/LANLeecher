package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// DHCPSource is a Kea server LANLeecher reads leases from.
type DHCPSource struct {
	ID   int64
	Name string
	URL  string
	// Kind is "agent" (Kea Control Agent, Kea < 3), "dhcp4" or "dhcp6"
	// (HTTP control socket of the server itself, Kea >= 3).
	Kind  string
	DHCP4 bool
	DHCP6 bool
	// Username for HTTP basic authentication.
	Username string
	// SecretsEnc holds the encrypted password and client key.
	SecretsEnc         []byte
	CAPEM              string
	ClientCertPEM      string
	PinnedSHA256       string
	PinnedCertPEM      string
	InsecureSkipVerify bool
	PollInterval       int
	Enabled            bool
	LastPollAt         *time.Time
	LastPollOK         bool
	LastError          string
	// CertProblem is "", "untrusted" or "changed".
	CertProblem      string
	PresentedCertPEM string
	KeaVersion       string
	LeaseCount       int
	CreatedAt        time.Time
}

const dhcpCols = `id, name, url, kind, dhcp4, dhcp6, username, secrets_enc, ca_pem, client_cert_pem, pinned_sha256,
	pinned_cert_pem, insecure_skip_verify, poll_interval, enabled, last_poll_at, last_poll_ok, last_error, cert_problem,
	presented_cert_pem, kea_version, lease_count, created_at`

func scanDHCPSource(row interface{ Scan(...any) error }) (*DHCPSource, error) {
	var s DHCPSource
	var last sql.NullInt64
	var created int64
	err := row.Scan(&s.ID, &s.Name, &s.URL, &s.Kind, &s.DHCP4, &s.DHCP6, &s.Username, &s.SecretsEnc, &s.CAPEM,
		&s.ClientCertPEM, &s.PinnedSHA256, &s.PinnedCertPEM, &s.InsecureSkipVerify, &s.PollInterval, &s.Enabled, &last,
		&s.LastPollOK, &s.LastError, &s.CertProblem, &s.PresentedCertPEM, &s.KeaVersion, &s.LeaseCount, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.LastPollAt = fromNullUnix(last)
	s.CreatedAt = fromUnix(created)
	return &s, nil
}

// ListDHCPSources returns all DHCP sources by name.
func (db *DB) ListDHCPSources(ctx context.Context) ([]*DHCPSource, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+dhcpCols+` FROM dhcp_sources ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*DHCPSource
	for rows.Next() {
		s, err := scanDHCPSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetDHCPSource returns one DHCP source.
func (db *DB) GetDHCPSource(ctx context.Context, id int64) (*DHCPSource, error) {
	return scanDHCPSource(db.sql.QueryRowContext(ctx, `SELECT `+dhcpCols+` FROM dhcp_sources WHERE id = ?`, id))
}

// CreateDHCPSource inserts s and sets s.ID.
func (db *DB) CreateDHCPSource(ctx context.Context, s *DHCPSource) error {
	s.CreatedAt = Now()
	res, err := db.sql.ExecContext(ctx, `INSERT INTO dhcp_sources (name, url, kind, dhcp4, dhcp6, username, secrets_enc,
		ca_pem, client_cert_pem, insecure_skip_verify, poll_interval, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.Name, s.URL, s.Kind, boolInt(s.DHCP4), boolInt(s.DHCP6), s.Username, s.SecretsEnc, s.CAPEM, s.ClientCertPEM,
		boolInt(s.InsecureSkipVerify), s.PollInterval, boolInt(s.Enabled), unix(s.CreatedAt))
	if err != nil {
		return err
	}
	s.ID, err = res.LastInsertId()
	return err
}

// UpdateDHCPSource saves the editable fields. A changed URL drops the pinned
// certificate: it belonged to the old server.
func (db *DB) UpdateDHCPSource(ctx context.Context, s *DHCPSource) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE dhcp_sources SET name = ?, kind = ?, dhcp4 = ?, dhcp6 = ?, username = ?,
		secrets_enc = ?, ca_pem = ?, client_cert_pem = ?, insecure_skip_verify = ?, poll_interval = ?, enabled = ?,
		pinned_sha256 = CASE WHEN url = ? THEN pinned_sha256 ELSE '' END,
		pinned_cert_pem = CASE WHEN url = ? THEN pinned_cert_pem ELSE '' END,
		url = ? WHERE id = ?`,
		s.Name, s.Kind, boolInt(s.DHCP4), boolInt(s.DHCP6), s.Username, s.SecretsEnc, s.CAPEM, s.ClientCertPEM,
		boolInt(s.InsecureSkipVerify), s.PollInterval, boolInt(s.Enabled), s.URL, s.URL, s.URL, s.ID)
	return err
}

// DeleteDHCPSource removes a DHCP source. Recorded IPs and hostnames stay.
func (db *DB) DeleteDHCPSource(ctx context.Context, id int64) error {
	_, err := db.sql.ExecContext(ctx, `DELETE FROM dhcp_sources WHERE id = ?`, id)
	return err
}

// DHCPStatus is the outcome of a poll or connection test.
type DHCPStatus struct {
	OK          bool
	Error       string
	CertProblem string
	// PresentedCertPEM is stored whenever a certificate was seen, so an admin
	// can review and trust exactly that certificate.
	PresentedCertPEM string
	KeaVersion       string
	LeaseCount       int
}

// SetDHCPStatus records the outcome of a poll.
func (db *DB) SetDHCPStatus(ctx context.Context, id int64, st DHCPStatus, at time.Time) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE dhcp_sources SET last_poll_at = ?, last_poll_ok = ?, last_error = ?,
		cert_problem = ?, presented_cert_pem = CASE WHEN ? = '' THEN presented_cert_pem ELSE ? END,
		kea_version = CASE WHEN ? = '' THEN kea_version ELSE ? END,
		lease_count = CASE WHEN ? THEN ? ELSE lease_count END WHERE id = ?`,
		unix(at), boolInt(st.OK), st.Error, st.CertProblem, st.PresentedCertPEM, st.PresentedCertPEM,
		st.KeaVersion, st.KeaVersion, boolInt(st.OK), st.LeaseCount, id)
	return err
}

// PinDHCPCertificate trusts exactly one server certificate for a source.
func (db *DB) PinDHCPCertificate(ctx context.Context, id int64, sha256Hex, certPEM string) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE dhcp_sources SET pinned_sha256 = ?, pinned_cert_pem = ?, cert_problem = ''
		WHERE id = ?`, sha256Hex, certPEM, id)
	return err
}

// UnpinDHCPCertificate revokes the trusted certificate of a source.
func (db *DB) UnpinDHCPCertificate(ctx context.Context, id int64) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE dhcp_sources SET pinned_sha256 = '', pinned_cert_pem = '' WHERE id = ?`, id)
	return err
}
