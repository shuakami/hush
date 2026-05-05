// Package inventory manages the registered host catalog.
//
// A host record is the user-facing alias (e.g. "hk1", "nmg-mac"). Each host
// describes a transport (ssh, sdjz-relay, ...) plus the data needed to reach
// it; credentials are stored *by reference* into the vault, never inline.
package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shuakami/hush/internal/store"
)

// Transport identifies a connection mechanism.
type Transport string

const (
	TransportSSH       Transport = "ssh"        // raw SSH (password or key); jump_via optional
	TransportSDJZRelay Transport = "sdjz-relay" // ssh.sdjz.wiki/c/<TOKEN> over curl
)

// AuthKind selects how SSH transport authenticates.
type AuthKind string

const (
	AuthKindNone     AuthKind = ""
	AuthKindPassword AuthKind = "password"
	AuthKindKey      AuthKind = "key"
)

// Host is a registered server alias.
type Host struct {
	Name        string            `json:"name"`
	DisplayName string            `json:"display_name,omitempty"`
	Transport   Transport         `json:"transport"`
	Address     string            `json:"address,omitempty"`
	Port        int               `json:"port,omitempty"`
	OS          string            `json:"os,omitempty"` // linux | windows
	SSHUser     string            `json:"ssh_user,omitempty"`
	AuthKind    AuthKind          `json:"auth_kind,omitempty"`
	AuthSecret  string            `json:"auth_secret,omitempty"`  // secret-name reference
	JumpVia     string            `json:"jump_via,omitempty"`     // host name
	RelaySecret string            `json:"relay_secret,omitempty"` // secret holding sdjz token
	Tags        []string          `json:"tags,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// ErrHostNotFound is returned when a name doesn't resolve.
var ErrHostNotFound = errors.New("host not found")

// Manager owns the hosts table.
type Manager struct{ st *store.Store }

// New returns a Manager bound to the given store.
func New(st *store.Store) *Manager { return &Manager{st: st} }

// Upsert creates or replaces a host record.
func (m *Manager) Upsert(ctx context.Context, h *Host) error {
	if h.Name == "" {
		return errors.New("host name required")
	}
	if !validName(h.Name) {
		return fmt.Errorf("invalid host name %q (use letters, digits, dash, underscore)", h.Name)
	}
	switch h.Transport {
	case TransportSSH:
		if h.Address == "" {
			return errors.New("ssh transport requires address")
		}
		if h.Port == 0 {
			h.Port = 22
		}
		if h.SSHUser == "" {
			return errors.New("ssh transport requires ssh_user")
		}
		if h.AuthKind == "" {
			return errors.New("ssh transport requires auth_kind")
		}
		if h.AuthSecret == "" {
			return errors.New("ssh transport requires auth_secret (vault reference)")
		}
	case TransportSDJZRelay:
		if h.RelaySecret == "" {
			return errors.New("sdjz-relay transport requires relay_secret (vault reference)")
		}
	default:
		return fmt.Errorf("unknown transport %q", h.Transport)
	}
	if h.OS == "" {
		h.OS = "linux"
	}
	if h.Tags == nil {
		h.Tags = []string{}
	}
	if h.Metadata == nil {
		h.Metadata = map[string]string{}
	}
	tagsJSON, _ := json.Marshal(h.Tags)
	metaJSON, _ := json.Marshal(h.Metadata)
	now := time.Now().Unix()

	_, err := m.st.DB().ExecContext(ctx, `
		INSERT INTO hosts(name, display_name, transport, address, port, os, ssh_user, auth_kind, auth_secret, jump_via, relay_secret, tags_json, metadata_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			display_name=excluded.display_name,
			transport=excluded.transport,
			address=excluded.address,
			port=excluded.port,
			os=excluded.os,
			ssh_user=excluded.ssh_user,
			auth_kind=excluded.auth_kind,
			auth_secret=excluded.auth_secret,
			jump_via=excluded.jump_via,
			relay_secret=excluded.relay_secret,
			tags_json=excluded.tags_json,
			metadata_json=excluded.metadata_json,
			updated_at=excluded.updated_at
	`,
		h.Name, h.DisplayName, string(h.Transport), h.Address, h.Port, h.OS,
		h.SSHUser, string(h.AuthKind), h.AuthSecret, h.JumpVia, h.RelaySecret,
		string(tagsJSON), string(metaJSON), now, now)
	return err
}

// Get returns a host by name.
func (m *Manager) Get(ctx context.Context, name string) (*Host, error) {
	row := m.st.DB().QueryRowContext(ctx, hostSelect+` WHERE name = ?`, name)
	return scanHost(row)
}

// List returns all hosts. If tag is non-empty only hosts carrying that tag are
// returned.
func (m *Manager) List(ctx context.Context, tag string) ([]Host, error) {
	rows, err := m.st.DB().QueryContext(ctx, hostSelect+` ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Host
	for rows.Next() {
		h, err := scanHostRow(rows)
		if err != nil {
			return nil, err
		}
		if tag != "" && !contains(h.Tags, tag) {
			continue
		}
		out = append(out, *h)
	}
	return out, rows.Err()
}

// Delete removes a host.
func (m *Manager) Delete(ctx context.Context, name string) error {
	res, err := m.st.DB().ExecContext(ctx, `DELETE FROM hosts WHERE name = ?`, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrHostNotFound
	}
	return nil
}

// ResolveTag returns all hosts matching either an exact name or a tag.
func (m *Manager) ResolveTag(ctx context.Context, expr string) ([]Host, error) {
	if expr == "" {
		return nil, errors.New("empty selector")
	}
	all, err := m.List(ctx, "")
	if err != nil {
		return nil, err
	}
	var out []Host
	for _, h := range all {
		if h.Name == expr || contains(h.Tags, expr) {
			out = append(out, h)
		}
	}
	if len(out) == 0 {
		return nil, ErrHostNotFound
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ---- helpers ---------------------------------------------------------------

const hostSelect = `SELECT name, display_name, transport, address, port, os, ssh_user, auth_kind, auth_secret, jump_via, relay_secret, tags_json, metadata_json, created_at, updated_at FROM hosts`

type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanHost(r *sql.Row) (*Host, error) {
	h, err := scanHostRow(r)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrHostNotFound
		}
		return nil, err
	}
	return h, nil
}

func scanHostRow(r rowScanner) (*Host, error) {
	var (
		h          Host
		transport  string
		auth       string
		tags, meta string
		ts1, ts2   int64
	)
	if err := r.Scan(&h.Name, &h.DisplayName, &transport, &h.Address, &h.Port, &h.OS,
		&h.SSHUser, &auth, &h.AuthSecret, &h.JumpVia, &h.RelaySecret,
		&tags, &meta, &ts1, &ts2); err != nil {
		return nil, err
	}
	h.Transport = Transport(transport)
	h.AuthKind = AuthKind(auth)
	_ = json.Unmarshal([]byte(tags), &h.Tags)
	_ = json.Unmarshal([]byte(meta), &h.Metadata)
	h.CreatedAt = time.Unix(ts1, 0)
	h.UpdatedAt = time.Unix(ts2, 0)
	return &h, nil
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func validName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return false
		}
	}
	return !strings.HasPrefix(s, "-")
}
