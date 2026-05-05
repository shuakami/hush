// Package store wraps the on-disk SQLite database. All access goes through
// strongly-typed methods; callers never write SQL.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned by lookup methods when nothing matches.
var ErrNotFound = errors.New("not found")

// Store owns the SQLite handle.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the database at <dir>/hush.db and applies migrations.
func Open(dir string) (*Store, error) {
	path := filepath.Join(dir, "hush.db")
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // sqlite write contention; readers proxied through this conn
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the raw handle for advanced cases (audit chain reads, tests).
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate() error {
	ctx := context.Background()
	for i, stmt := range schema {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate step %d: %w", i, err)
		}
	}
	return nil
}

// schema is applied in order on every boot. Statements must be idempotent.
var schema = []string{
	`CREATE TABLE IF NOT EXISTS secrets (
		name           TEXT PRIMARY KEY,
		ciphertext     BLOB NOT NULL,
		dek_wrapped    BLOB NOT NULL,
		nonce          BLOB NOT NULL,
		version        INTEGER NOT NULL DEFAULT 1,
		created_at     INTEGER NOT NULL,
		updated_at     INTEGER NOT NULL,
		rotated_at     INTEGER,
		metadata_json  TEXT NOT NULL DEFAULT '{}'
	)`,
	`CREATE TABLE IF NOT EXISTS hosts (
		name           TEXT PRIMARY KEY,
		display_name   TEXT NOT NULL DEFAULT '',
		transport      TEXT NOT NULL,                   -- ssh | sdjz-relay
		address        TEXT NOT NULL DEFAULT '',
		port           INTEGER NOT NULL DEFAULT 0,
		os             TEXT NOT NULL DEFAULT 'linux',   -- linux | windows
		ssh_user       TEXT NOT NULL DEFAULT '',
		auth_kind      TEXT NOT NULL DEFAULT '',        -- password | key | none
		auth_secret    TEXT NOT NULL DEFAULT '',        -- secret name reference
		jump_via       TEXT NOT NULL DEFAULT '',        -- another host name
		relay_secret   TEXT NOT NULL DEFAULT '',        -- secret name holding sdjz token
		tags_json      TEXT NOT NULL DEFAULT '[]',
		metadata_json  TEXT NOT NULL DEFAULT '{}',
		created_at     INTEGER NOT NULL,
		updated_at     INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS hosts_transport_idx ON hosts(transport)`,
	`CREATE TABLE IF NOT EXISTS api_keys (
		id             TEXT PRIMARY KEY,
		name           TEXT NOT NULL,
		key_hash       BLOB NOT NULL,
		scopes_json    TEXT NOT NULL DEFAULT '[]',
		created_at     INTEGER NOT NULL,
		last_used_at   INTEGER,
		expires_at     INTEGER,
		revoked_at     INTEGER
	)`,
	`CREATE INDEX IF NOT EXISTS api_keys_name_idx ON api_keys(name)`,
	`CREATE TABLE IF NOT EXISTS audit (
		id             INTEGER PRIMARY KEY AUTOINCREMENT,
		ts             INTEGER NOT NULL,
		actor          TEXT NOT NULL,
		action         TEXT NOT NULL,
		target         TEXT NOT NULL DEFAULT '',
		details_json   TEXT NOT NULL DEFAULT '{}',
		prev_hash      BLOB NOT NULL,
		hash           BLOB NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS audit_ts_idx ON audit(ts)`,
	`CREATE INDEX IF NOT EXISTS audit_actor_idx ON audit(actor)`,
}
