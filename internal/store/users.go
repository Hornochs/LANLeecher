package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// User roles, from least to most privileged.
const (
	RoleViewer   = "viewer"
	RoleOperator = "operator"
	RoleAdmin    = "admin"
)

// RoleLevel orders roles for permission checks.
func RoleLevel(role string) int {
	switch role {
	case RoleViewer:
		return 1
	case RoleOperator:
		return 2
	case RoleAdmin:
		return 3
	}
	return 0
}

// User is a web user.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Role         string
	Language     string
	Disabled     bool
	CreatedAt    time.Time
	LastLogin    *time.Time
}

// Can reports whether the user has at least the given role.
func (u *User) Can(role string) bool { return u != nil && RoleLevel(u.Role) >= RoleLevel(role) }

const userCols = `id, username, password_hash, role, language, disabled, created_at, last_login`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var created int64
	var last sql.NullInt64
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Language, &u.Disabled, &created, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.CreatedAt = fromUnix(created)
	u.LastLogin = fromNullUnix(last)
	return &u, nil
}

// CreateUser inserts a user.
func (db *DB) CreateUser(ctx context.Context, u *User) error {
	if RoleLevel(u.Role) == 0 {
		return errors.New("invalid role")
	}
	if u.Language == "" {
		u.Language = "en"
	}
	u.CreatedAt = Now()
	res, err := db.sql.ExecContext(ctx, `INSERT INTO users (username, password_hash, role, language, created_at)
		VALUES (?, ?, ?, ?, ?)`, strings.TrimSpace(u.Username), u.PasswordHash, u.Role, u.Language, unix(u.CreatedAt))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return errors.New("username already exists")
		}
		return err
	}
	u.ID, err = res.LastInsertId()
	return err
}

// UserByName returns a user by username (case-insensitive).
func (db *DB) UserByName(ctx context.Context, name string) (*User, error) {
	return scanUser(db.sql.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username = ?`, strings.TrimSpace(name)))
}

// UserByID returns a user.
func (db *DB) UserByID(ctx context.Context, id int64) (*User, error) {
	return scanUser(db.sql.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

// ListUsers returns all users.
func (db *DB) ListUsers(ctx context.Context) ([]*User, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CountAdmins returns the number of enabled admins.
func (db *DB) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin' AND disabled = 0`).Scan(&n)
	return n, err
}

// UpdateUser saves role, language and disabled state.
func (db *DB) UpdateUser(ctx context.Context, id int64, role, language string, disabled bool) error {
	if RoleLevel(role) == 0 {
		return errors.New("invalid role")
	}
	_, err := db.sql.ExecContext(ctx, `UPDATE users SET role = ?, language = ?, disabled = ? WHERE id = ?`,
		role, language, boolInt(disabled), id)
	if err == nil && disabled {
		_, err = db.sql.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id)
	}
	return err
}

// SetUserLanguage changes the UI language of a user.
func (db *DB) SetUserLanguage(ctx context.Context, id int64, language string) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE users SET language = ? WHERE id = ?`, language, id)
	return err
}

// SetPassword replaces the password hash and ends all sessions of the user.
func (db *DB) SetPassword(ctx context.Context, id int64, hash string) error {
	return db.InTx(ctx, func(tx *Tx) error {
		if _, err := tx.tx.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, hash, id); err != nil {
			return err
		}
		_, err := tx.tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id)
		return err
	})
}

// DeleteUser removes a user.
func (db *DB) DeleteUser(ctx context.Context, id int64) error {
	_, err := db.sql.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	return err
}

// TouchLogin records a successful login.
func (db *DB) TouchLogin(ctx context.Context, id int64) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE users SET last_login = ? WHERE id = ?`, unix(Now()), id)
	return err
}

// Session is a login session. ID is the SHA-256 hash of the cookie value.
type Session struct {
	ID        string
	UserID    int64
	CSRFToken string
	ExpiresAt time.Time
}

// CreateSession stores a session.
func (db *DB) CreateSession(ctx context.Context, s *Session) error {
	_, err := db.sql.ExecContext(ctx, `INSERT INTO sessions (id, user_id, csrf_token, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		s.ID, s.UserID, s.CSRFToken, unix(Now()), unix(s.ExpiresAt))
	return err
}

// GetSession returns a valid session with its user.
func (db *DB) GetSession(ctx context.Context, id string) (*Session, *User, error) {
	var s Session
	var exp int64
	err := db.sql.QueryRowContext(ctx, `SELECT id, user_id, csrf_token, expires_at FROM sessions WHERE id = ? AND expires_at > ?`,
		id, unix(Now())).Scan(&s.ID, &s.UserID, &s.CSRFToken, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	s.ExpiresAt = fromUnix(exp)
	u, err := db.UserByID(ctx, s.UserID)
	if err != nil {
		return nil, nil, err
	}
	if u.Disabled {
		return nil, nil, ErrNotFound
	}
	return &s, u, nil
}

// ExtendSession moves the expiry of a session.
func (db *DB) ExtendSession(ctx context.Context, id string, expires time.Time) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE sessions SET expires_at = ? WHERE id = ?`, unix(expires), id)
	return err
}

// DeleteSession ends a session.
func (db *DB) DeleteSession(ctx context.Context, id string) error {
	_, err := db.sql.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// PurgeSessions deletes expired sessions.
func (db *DB) PurgeSessions(ctx context.Context) error {
	_, err := db.sql.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, unix(Now()))
	return err
}

// APIToken is a bearer token for the REST API.
type APIToken struct {
	ID         int64
	Name       string
	TokenHash  string
	Prefix     string
	Scopes     []string
	AllowedIPs []string
	CreatedBy  int64
	CreatedAt  time.Time
	ExpiresAt  *time.Time
	LastUsed   *time.Time
	Revoked    bool
}

// HasScope reports whether the token carries scope.
func (t *APIToken) HasScope(scope string) bool {
	for _, s := range t.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

const tokenCols = `id, name, token_hash, prefix, scopes, allowed_ips, COALESCE(created_by, 0), created_at, expires_at, last_used, revoked`

func scanToken(row interface{ Scan(...any) error }) (*APIToken, error) {
	var t APIToken
	var scopes, ips string
	var created int64
	var exp, used sql.NullInt64
	err := row.Scan(&t.ID, &t.Name, &t.TokenHash, &t.Prefix, &scopes, &ips, &t.CreatedBy, &created, &exp, &used, &t.Revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.Scopes = strings.Fields(scopes)
	t.AllowedIPs = strings.Fields(ips)
	t.CreatedAt = fromUnix(created)
	t.ExpiresAt = fromNullUnix(exp)
	t.LastUsed = fromNullUnix(used)
	return &t, nil
}

// CreateAPIToken stores a token.
func (db *DB) CreateAPIToken(ctx context.Context, t *APIToken) error {
	t.CreatedAt = Now()
	var createdBy any
	if t.CreatedBy != 0 {
		createdBy = t.CreatedBy
	}
	res, err := db.sql.ExecContext(ctx, `INSERT INTO api_tokens (name, token_hash, prefix, scopes, allowed_ips, created_by,
		created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, t.Name, t.TokenHash, t.Prefix, strings.Join(t.Scopes, " "),
		strings.Join(t.AllowedIPs, " "), createdBy, unix(t.CreatedAt), nullUnix(t.ExpiresAt))
	if err != nil {
		return err
	}
	t.ID, err = res.LastInsertId()
	return err
}

// APITokenByHash returns a token by hash.
func (db *DB) APITokenByHash(ctx context.Context, hash string) (*APIToken, error) {
	return scanToken(db.sql.QueryRowContext(ctx, `SELECT `+tokenCols+` FROM api_tokens WHERE token_hash = ?`, hash))
}

// ListAPITokens returns all tokens.
func (db *DB) ListAPITokens(ctx context.Context) ([]*APIToken, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+tokenCols+` FROM api_tokens ORDER BY revoked, created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*APIToken
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeAPIToken disables a token immediately.
func (db *DB) RevokeAPIToken(ctx context.Context, id int64) error {
	_, err := db.sql.ExecContext(ctx, `UPDATE api_tokens SET revoked = 1 WHERE id = ?`, id)
	return err
}

// TouchAPIToken updates last_used (at most once per minute per token).
func (db *DB) TouchAPIToken(ctx context.Context, id int64) error {
	now := unix(Now())
	_, err := db.sql.ExecContext(ctx, `UPDATE api_tokens SET last_used = ? WHERE id = ? AND (last_used IS NULL OR last_used < ?)`,
		now, id, now-60)
	return err
}

// AuditEntry is one line of the audit log.
type AuditEntry struct {
	ID      int64
	UserID  int64
	Actor   string
	Action  string
	Target  string
	Details string
	IP      string
	At      time.Time
}

// Audit writes an audit log entry. Errors are returned but rarely fatal.
func (db *DB) Audit(ctx context.Context, e AuditEntry) error {
	var uid any
	if e.UserID != 0 {
		uid = e.UserID
	}
	_, err := db.sql.ExecContext(ctx, `INSERT INTO audit_log (user_id, actor, action, target, details, ip, at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, uid, e.Actor, e.Action, e.Target, e.Details, e.IP, unix(Now()))
	return err
}

// ListAudit returns the newest audit entries.
func (db *DB) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT id, COALESCE(user_id, 0), actor, action, target, details, ip, at
		FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var at int64
		if err := rows.Scan(&e.ID, &e.UserID, &e.Actor, &e.Action, &e.Target, &e.Details, &e.IP, &at); err != nil {
			return nil, err
		}
		e.At = fromUnix(at)
		out = append(out, e)
	}
	return out, rows.Err()
}
