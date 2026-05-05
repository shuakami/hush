// Package apikey issues, hashes, and verifies API keys.
//
// Format: "hush_<base32(20 bytes)>". Stored as SHA-256(key) so the raw value
// is never persisted. Each key has a name (human-readable) and a list of
// scopes (e.g. "secret:read", "host:exec").
package apikey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shuakami/hush/internal/store"
)

// ErrInvalidKey is returned when verification fails.
var ErrInvalidKey = errors.New("invalid api key")

// Key is the metadata view of an API key (raw value never returned).
type Key struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	Revoked    bool       `json:"revoked"`
}

// Manager owns the api_keys table.
type Manager struct {
	st *store.Store
}

// New returns a Manager bound to the given store.
func New(st *store.Store) *Manager { return &Manager{st: st} }

// Mint generates a new API key, stores its hash, and returns the raw key
// (only time the caller will ever see it).
func (m *Manager) Mint(ctx context.Context, name string, scopes []string) (raw string, k *Key, err error) {
	if name == "" {
		return "", nil, errors.New("api key name required")
	}
	if scopes == nil {
		scopes = []string{}
	}

	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return "", nil, err
	}
	id := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(idBytes))

	keyBytes := make([]byte, 20)
	if _, err := rand.Read(keyBytes); err != nil {
		return "", nil, err
	}
	raw = "hush_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(keyBytes))
	hash := sha256.Sum256([]byte(raw))

	scopeJSON, _ := json.Marshal(scopes)
	now := time.Now().Unix()

	if _, err := m.st.DB().ExecContext(ctx,
		`INSERT INTO api_keys(id, name, key_hash, scopes_json, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, name, hash[:], string(scopeJSON), now); err != nil {
		return "", nil, fmt.Errorf("store api key: %w", err)
	}

	return raw, &Key{
		ID: id, Name: name, Scopes: scopes, CreatedAt: time.Unix(now, 0),
	}, nil
}

// Verify resolves a raw API key to its metadata, or returns ErrInvalidKey.
func (m *Manager) Verify(ctx context.Context, raw string) (*Key, error) {
	if !strings.HasPrefix(raw, "hush_") {
		return nil, ErrInvalidKey
	}
	hash := sha256.Sum256([]byte(raw))

	row := m.st.DB().QueryRowContext(ctx,
		`SELECT id, name, scopes_json, created_at, last_used_at, revoked_at FROM api_keys WHERE key_hash = ?`,
		hash[:])
	var (
		k        Key
		scopes   string
		ts       int64
		lastUsed sql.NullInt64
		revoked  sql.NullInt64
	)
	if err := row.Scan(&k.ID, &k.Name, &scopes, &ts, &lastUsed, &revoked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalidKey
		}
		return nil, err
	}
	if revoked.Valid {
		return nil, ErrInvalidKey
	}
	_ = json.Unmarshal([]byte(scopes), &k.Scopes)
	k.CreatedAt = time.Unix(ts, 0)
	if lastUsed.Valid {
		t := time.Unix(lastUsed.Int64, 0)
		k.LastUsedAt = &t
	}

	go func() {
		_, _ = m.st.DB().Exec(`UPDATE api_keys SET last_used_at = ? WHERE id = ?`,
			time.Now().Unix(), k.ID)
	}()

	return &k, nil
}

// List returns metadata for every (non-revoked) API key.
func (m *Manager) List(ctx context.Context) ([]Key, error) {
	rows, err := m.st.DB().QueryContext(ctx,
		`SELECT id, name, scopes_json, created_at, last_used_at, revoked_at FROM api_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Key
	for rows.Next() {
		var k Key
		var scopes string
		var ts int64
		var lastUsed, revoked sql.NullInt64
		if err := rows.Scan(&k.ID, &k.Name, &scopes, &ts, &lastUsed, &revoked); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(scopes), &k.Scopes)
		k.CreatedAt = time.Unix(ts, 0)
		if lastUsed.Valid {
			t := time.Unix(lastUsed.Int64, 0)
			k.LastUsedAt = &t
		}
		k.Revoked = revoked.Valid
		out = append(out, k)
	}
	return out, rows.Err()
}

// Revoke marks an API key as revoked.
func (m *Manager) Revoke(ctx context.Context, id string) error {
	res, err := m.st.DB().ExecContext(ctx,
		`UPDATE api_keys SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
		time.Now().Unix(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("api key not found or already revoked")
	}
	return nil
}

// HasScope checks whether the key carries (or matches a wildcard for) a scope.
//
// Examples:
//
//	scope "secret:read"   matches "secret:read"
//	scope "secret:*"      matches any "secret:*" check
//	scope "*"             matches anything
func (k *Key) HasScope(want string) bool {
	for _, s := range k.Scopes {
		if s == "*" || s == want {
			return true
		}
		if strings.HasSuffix(s, ":*") && strings.HasPrefix(want, strings.TrimSuffix(s, "*")) {
			return true
		}
	}
	return false
}
