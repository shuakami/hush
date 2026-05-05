// Package vault implements the Hush envelope-encryption layer.
//
// Each secret is encrypted with a freshly generated 256-bit DEK using
// AES-256-GCM. The DEK is then wrapped with the master KEK (also AES-256-GCM)
// and stored next to the ciphertext. Plaintext secrets exist only for the
// duration of an Open() call.
//
// KEK source is pluggable: env var or file. KMS-backed KEKs are a v0.2 item.
package vault

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/shuakami/hush/internal/store"
)

// ErrSecretNotFound is returned by Get when no secret exists with the given name.
var ErrSecretNotFound = errors.New("secret not found")

// Secret is the public-facing record (no plaintext leaves Vault unless asked).
type Secret struct {
	Name      string            `json:"name"`
	Version   int               `json:"version"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	RotatedAt *time.Time        `json:"rotated_at,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// Vault is the secret manager.
type Vault struct {
	mu  sync.RWMutex
	st  *store.Store
	kek []byte // 32 bytes
}

// Open mounts the vault, deriving / loading the master KEK.
//
// kekKind:
//
//	"env":  kekValue is the *name* of an env var holding base64(32 bytes)
//	"file": kekValue is a path to a file holding base64(32 bytes); created on first boot.
func Open(st *store.Store, kekKind, kekValue string) (*Vault, error) {
	kek, err := loadOrInitKEK(kekKind, kekValue)
	if err != nil {
		return nil, err
	}
	return &Vault{st: st, kek: kek}, nil
}

func loadOrInitKEK(kind, value string) ([]byte, error) {
	switch kind {
	case "env":
		raw := os.Getenv(value)
		if raw == "" {
			return nil, fmt.Errorf("env %s is empty; set it to base64(32 bytes)", value)
		}
		return decodeKey(raw)
	case "file":
		body, err := os.ReadFile(value)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("read kek file: %w", err)
			}
			// First boot: generate one.
			key := make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				return nil, err
			}
			enc := []byte(base64.StdEncoding.EncodeToString(key))
			if err := os.WriteFile(value, enc, 0o600); err != nil {
				return nil, fmt.Errorf("write kek file: %w", err)
			}
			return key, nil
		}
		return decodeKey(string(body))
	default:
		return nil, fmt.Errorf("unsupported kek kind %q", kind)
	}
}

func decodeKey(s string) ([]byte, error) {
	dec, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("decode kek: %w", err)
	}
	if len(dec) != 32 {
		return nil, fmt.Errorf("kek must be 32 bytes, got %d", len(dec))
	}
	return dec, nil
}

// Put creates or replaces a secret. Setting an existing secret bumps version.
func (v *Vault) Put(ctx context.Context, name string, plaintext []byte, metadata map[string]string) (*Secret, error) {
	if name == "" {
		return nil, errors.New("secret name required")
	}
	if len(plaintext) == 0 {
		return nil, errors.New("secret value required")
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	// Generate fresh DEK.
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return nil, err
	}

	ciphertext, nonce, err := encryptAESGCM(dek, plaintext)
	if err != nil {
		return nil, err
	}

	dekWrapped, _, err := encryptAESGCMWithFixedNonce(v.kek, dek, nonce)
	if err != nil {
		return nil, err
	}

	if metadata == nil {
		metadata = map[string]string{}
	}
	metaJSON, _ := json.Marshal(metadata)
	now := time.Now().Unix()

	row := v.st.DB().QueryRowContext(ctx,
		`SELECT version FROM secrets WHERE name = ?`, name)
	var existingVersion int
	err = row.Scan(&existingVersion)
	switch {
	case err == nil:
		if _, err := v.st.DB().ExecContext(ctx,
			`UPDATE secrets SET ciphertext=?, dek_wrapped=?, nonce=?, version=version+1, updated_at=?, rotated_at=?, metadata_json=? WHERE name=?`,
			ciphertext, dekWrapped, nonce, now, now, string(metaJSON), name); err != nil {
			return nil, err
		}
	case errors.Is(err, sql.ErrNoRows):
		if _, err := v.st.DB().ExecContext(ctx,
			`INSERT INTO secrets(name, ciphertext, dek_wrapped, nonce, version, created_at, updated_at, metadata_json) VALUES (?, ?, ?, ?, 1, ?, ?, ?)`,
			name, ciphertext, dekWrapped, nonce, now, now, string(metaJSON)); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	return v.head(ctx, name)
}

// Get returns plaintext + metadata.
func (v *Vault) Get(ctx context.Context, name string) ([]byte, *Secret, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	var ct, dekWrapped, nonce []byte
	var version int
	var createdAt, updatedAt int64
	var rotatedAt sql.NullInt64
	var metaJSON string
	row := v.st.DB().QueryRowContext(ctx,
		`SELECT ciphertext, dek_wrapped, nonce, version, created_at, updated_at, rotated_at, metadata_json FROM secrets WHERE name = ?`, name)
	if err := row.Scan(&ct, &dekWrapped, &nonce, &version, &createdAt, &updatedAt, &rotatedAt, &metaJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrSecretNotFound
		}
		return nil, nil, err
	}

	dek, err := decryptAESGCMWithFixedNonce(v.kek, dekWrapped, nonce)
	if err != nil {
		return nil, nil, fmt.Errorf("unwrap dek: %w", err)
	}
	plaintext, err := decryptAESGCM(dek, ct, nonce)
	if err != nil {
		return nil, nil, fmt.Errorf("decrypt: %w", err)
	}

	meta := map[string]string{}
	_ = json.Unmarshal([]byte(metaJSON), &meta)

	s := &Secret{
		Name:      name,
		Version:   version,
		CreatedAt: time.Unix(createdAt, 0),
		UpdatedAt: time.Unix(updatedAt, 0),
		Metadata:  meta,
	}
	if rotatedAt.Valid {
		t := time.Unix(rotatedAt.Int64, 0)
		s.RotatedAt = &t
	}
	return plaintext, s, nil
}

// Head returns metadata for a secret without exposing plaintext.
func (v *Vault) Head(ctx context.Context, name string) (*Secret, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.head(ctx, name)
}

func (v *Vault) head(ctx context.Context, name string) (*Secret, error) {
	var version int
	var createdAt, updatedAt int64
	var rotatedAt sql.NullInt64
	var metaJSON string
	row := v.st.DB().QueryRowContext(ctx,
		`SELECT version, created_at, updated_at, rotated_at, metadata_json FROM secrets WHERE name = ?`, name)
	if err := row.Scan(&version, &createdAt, &updatedAt, &rotatedAt, &metaJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSecretNotFound
		}
		return nil, err
	}
	meta := map[string]string{}
	_ = json.Unmarshal([]byte(metaJSON), &meta)
	s := &Secret{
		Name:      name,
		Version:   version,
		CreatedAt: time.Unix(createdAt, 0),
		UpdatedAt: time.Unix(updatedAt, 0),
		Metadata:  meta,
	}
	if rotatedAt.Valid {
		t := time.Unix(rotatedAt.Int64, 0)
		s.RotatedAt = &t
	}
	return s, nil
}

// List returns metadata for all secrets, ordered by name.
func (v *Vault) List(ctx context.Context) ([]Secret, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	rows, err := v.st.DB().QueryContext(ctx,
		`SELECT name, version, created_at, updated_at, rotated_at, metadata_json FROM secrets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Secret
	for rows.Next() {
		var s Secret
		var ts1, ts2 int64
		var rot sql.NullInt64
		var meta string
		if err := rows.Scan(&s.Name, &s.Version, &ts1, &ts2, &rot, &meta); err != nil {
			return nil, err
		}
		s.CreatedAt = time.Unix(ts1, 0)
		s.UpdatedAt = time.Unix(ts2, 0)
		if rot.Valid {
			t := time.Unix(rot.Int64, 0)
			s.RotatedAt = &t
		}
		s.Metadata = map[string]string{}
		_ = json.Unmarshal([]byte(meta), &s.Metadata)
		out = append(out, s)
	}
	return out, rows.Err()
}

// Delete removes a secret. Returns ErrSecretNotFound if it doesn't exist.
func (v *Vault) Delete(ctx context.Context, name string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	res, err := v.st.DB().ExecContext(ctx, `DELETE FROM secrets WHERE name = ?`, name)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrSecretNotFound
	}
	return nil
}

// ---- crypto primitives -----------------------------------------------------

func encryptAESGCM(key, plaintext []byte) (ciphertext, nonce []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	return ct, nonce, nil
}

func decryptAESGCM(key, ciphertext, nonce []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func encryptAESGCMWithFixedNonce(key, plaintext, nonce []byte) (ciphertext, _ []byte, err error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, nil, fmt.Errorf("nonce size mismatch: got %d want %d", len(nonce), gcm.NonceSize())
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	return ct, nonce, nil
}

func decryptAESGCMWithFixedNonce(key, ciphertext, nonce []byte) ([]byte, error) {
	return decryptAESGCM(key, ciphertext, nonce)
}
