// Package audit writes append-only, hash-chained audit records.
//
// Each record's hash = SHA-256(prev_hash || canonical(record)). Tampering with
// any record causes the chain to break; export commands can verify in one pass.
package audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/shuakami/hush/internal/store"
)

// Record is one row of the audit log.
type Record struct {
	ID       int64                  `json:"id"`
	TS       time.Time              `json:"ts"`
	Actor    string                 `json:"actor"`
	Action   string                 `json:"action"`
	Target   string                 `json:"target,omitempty"`
	Details  map[string]interface{} `json:"details,omitempty"`
	PrevHash string                 `json:"prev_hash"`
	Hash     string                 `json:"hash"`
}

// Logger writes audit records.
type Logger struct {
	mu sync.Mutex
	st *store.Store
}

// New returns a Logger bound to a store.
func New(st *store.Store) *Logger { return &Logger{st: st} }

// Append writes a new record. It looks up the latest hash, computes the new
// chain hash, and inserts in a single statement.
func (l *Logger) Append(ctx context.Context, actor, action, target string, details map[string]interface{}) (*Record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	var prev []byte
	row := l.st.DB().QueryRowContext(ctx,
		`SELECT hash FROM audit ORDER BY id DESC LIMIT 1`)
	if err := row.Scan(&prev); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// Genesis block uses 32 zero bytes.
	if len(prev) == 0 {
		prev = make([]byte, 32)
	}

	if details == nil {
		details = map[string]interface{}{}
	}
	detailsJSON, _ := canonicalJSON(details)

	now := time.Now().UTC()
	tsUnix := now.Unix()
	canonical := append([]byte{}, prev...)
	canonical = append(canonical, []byte(strconv.FormatInt(tsUnix, 10))...)
	canonical = append(canonical, []byte(actor)...)
	canonical = append(canonical, []byte(action)...)
	canonical = append(canonical, []byte(target)...)
	canonical = append(canonical, detailsJSON...)
	sum := sha256.Sum256(canonical)

	res, err := l.st.DB().ExecContext(ctx,
		`INSERT INTO audit(ts, actor, action, target, details_json, prev_hash, hash) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		tsUnix, actor, action, target, string(detailsJSON), prev, sum[:])
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &Record{
		ID:       id,
		TS:       time.Unix(tsUnix, 0).UTC(),
		Actor:    actor,
		Action:   action,
		Target:   target,
		Details:  details,
		PrevHash: hex.EncodeToString(prev),
		Hash:     hex.EncodeToString(sum[:]),
	}, nil
}

// Tail returns the most recent N records (oldest first).
func (l *Logger) Tail(ctx context.Context, n int) ([]Record, error) {
	if n <= 0 {
		n = 100
	}
	rows, err := l.st.DB().QueryContext(ctx,
		`SELECT id, ts, actor, action, target, details_json, prev_hash, hash FROM audit ORDER BY id DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var r Record
		var ts int64
		var details string
		var prev, hash []byte
		if err := rows.Scan(&r.ID, &ts, &r.Actor, &r.Action, &r.Target, &details, &prev, &hash); err != nil {
			return nil, err
		}
		r.TS = time.Unix(ts, 0).UTC()
		r.Details = map[string]interface{}{}
		_ = json.Unmarshal([]byte(details), &r.Details)
		r.PrevHash = hex.EncodeToString(prev)
		r.Hash = hex.EncodeToString(hash)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Reverse so oldest is first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// Verify walks the entire chain and returns the first record that breaks it,
// or nil if the chain is intact.
func (l *Logger) Verify(ctx context.Context) (*Record, error) {
	rows, err := l.st.DB().QueryContext(ctx,
		`SELECT id, ts, actor, action, target, details_json, prev_hash, hash FROM audit ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	expectedPrev := make([]byte, 32)
	for rows.Next() {
		var r Record
		var ts int64
		var details string
		var prev, hash []byte
		if err := rows.Scan(&r.ID, &ts, &r.Actor, &r.Action, &r.Target, &details, &prev, &hash); err != nil {
			return nil, err
		}
		if string(prev) != string(expectedPrev) {
			r.PrevHash = hex.EncodeToString(prev)
			r.Hash = hex.EncodeToString(hash)
			return &r, errors.New("prev_hash mismatch")
		}
		canonical := append([]byte{}, prev...)
		canonical = append(canonical, []byte(strconv.FormatInt(ts, 10))...)
		canonical = append(canonical, []byte(r.Actor)...)
		canonical = append(canonical, []byte(r.Action)...)
		canonical = append(canonical, []byte(r.Target)...)
		canonical = append(canonical, []byte(details)...)
		sum := sha256.Sum256(canonical)
		if string(sum[:]) != string(hash) {
			r.PrevHash = hex.EncodeToString(prev)
			r.Hash = hex.EncodeToString(hash)
			return &r, errors.New("hash mismatch")
		}
		expectedPrev = hash
	}
	return nil, rows.Err()
}

// canonicalJSON marshals with sorted keys so hashing is reproducible.
func canonicalJSON(m map[string]interface{}) ([]byte, error) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]byte, 0, 64)
	out = append(out, '{')
	for i, k := range keys {
		if i > 0 {
			out = append(out, ',')
		}
		kb, _ := json.Marshal(k)
		out = append(out, kb...)
		out = append(out, ':')
		vb, err := json.Marshal(m[k])
		if err != nil {
			return nil, err
		}
		out = append(out, vb...)
	}
	out = append(out, '}')
	return out, nil
}
