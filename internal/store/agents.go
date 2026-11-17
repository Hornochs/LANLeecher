package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Agent is a host agent with its token.
type Agent struct {
	Token      *APIToken
	Hostname   string
	Mode       string
	Version    string
	RemoteIP   string
	Interfaces int
	LastReport *time.Time
	CreatedAt  time.Time
}

// CreateAgent stores the agent's token (which the caller limits to the
// agent:report scope) and the agent row in one transaction.
func (db *DB) CreateAgent(ctx context.Context, t *APIToken) error {
	return db.InTx(ctx, func(tx *Tx) error {
		t.CreatedAt = Now()
		var createdBy any
		if t.CreatedBy != 0 {
			createdBy = t.CreatedBy
		}
		res, err := tx.tx.ExecContext(ctx, `INSERT INTO api_tokens (name, token_hash, prefix, scopes, allowed_ips,
			created_by, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, t.Name, t.TokenHash, t.Prefix,
			strings.Join(t.Scopes, " "), strings.Join(t.AllowedIPs, " "), createdBy, unix(t.CreatedAt), nullUnix(t.ExpiresAt))
		if err != nil {
			return err
		}
		if t.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		_, err = tx.tx.ExecContext(ctx, `INSERT INTO agents (token_id, created_at) VALUES (?, ?)`, t.ID, unix(t.CreatedAt))
		return err
	})
}

// IsAgentToken reports whether a token belongs to an agent.
func (db *DB) IsAgentToken(ctx context.Context, tokenID int64) (bool, error) {
	var n int
	err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM agents WHERE token_id = ?`, tokenID).Scan(&n)
	return n > 0, err
}

// RecordAgentReport stores what an agent said about itself.
func (db *DB) RecordAgentReport(ctx context.Context, tokenID int64, hostname, mode, version, remoteIP string,
	interfaces int, at time.Time) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE agents SET hostname = ?, mode = ?, version = ?, remote_ip = ?,
		interfaces = ?, last_report = ? WHERE token_id = ?`, hostname, mode, version, remoteIP, interfaces, unix(at), tokenID)
	return err
}

// ListAgents returns all agents, active ones first, newest first.
func (db *DB) ListAgents(ctx context.Context) ([]*Agent, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT a.hostname, a.mode, a.version, a.remote_ip, a.interfaces,
		a.last_report, a.created_at, t.id, t.name, t.token_hash, t.prefix, t.scopes, t.allowed_ips,
		COALESCE(t.created_by, 0), t.created_at, t.expires_at, t.last_used, t.revoked FROM agents a JOIN api_tokens t ON t.id = a.token_id
		ORDER BY t.revoked, a.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Agent
	for rows.Next() {
		var a Agent
		var last sql.NullInt64
		var created int64
		dest := []any{&a.Hostname, &a.Mode, &a.Version, &a.RemoteIP, &a.Interfaces, &last, &created}
		tok, err := scanTokenWith(rows, dest...)
		if err != nil {
			return nil, err
		}
		a.Token = tok
		a.LastReport = fromNullUnix(last)
		a.CreatedAt = fromUnix(created)
		out = append(out, &a)
	}
	return out, rows.Err()
}
