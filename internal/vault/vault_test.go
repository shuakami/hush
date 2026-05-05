package vault

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/shuakami/hush/internal/store"
)

func newTestVault(t *testing.T) *Vault {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("HUSH_TEST_KEK", base64.StdEncoding.EncodeToString(key)); err != nil {
		t.Fatal(err)
	}
	v, err := Open(st, "env", "HUSH_TEST_KEK")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestVaultRoundTrip(t *testing.T) {
	v := newTestVault(t)
	ctx := context.Background()

	if _, err := v.Put(ctx, "DB_URL", []byte("postgres://user:pw@host/db"), map[string]string{"env": "prod"}); err != nil {
		t.Fatal(err)
	}
	val, _, err := v.Get(ctx, "DB_URL")
	if err != nil {
		t.Fatal(err)
	}
	if string(val) != "postgres://user:pw@host/db" {
		t.Fatalf("got %q", val)
	}
	meta, err := v.Head(ctx, "DB_URL")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Version != 1 {
		t.Fatalf("version=%d", meta.Version)
	}

	// Rotate.
	if _, err := v.Put(ctx, "DB_URL", []byte("newvalue"), nil); err != nil {
		t.Fatal(err)
	}
	val, _, _ = v.Get(ctx, "DB_URL")
	if string(val) != "newvalue" {
		t.Fatalf("rotated value got %q", val)
	}
	meta, _ = v.Head(ctx, "DB_URL")
	if meta.Version != 2 {
		t.Fatalf("version after rotate = %d", meta.Version)
	}
}

func TestVaultPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "kek")

	st1, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	v1, err := Open(st1, "file", keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v1.Put(context.Background(), "A", []byte("secret-one"), nil); err != nil {
		t.Fatal(err)
	}
	_ = st1.Close()

	st2, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	v2, err := Open(st2, "file", keyPath)
	if err != nil {
		t.Fatal(err)
	}
	val, _, err := v2.Get(context.Background(), "A")
	if err != nil {
		t.Fatal(err)
	}
	if string(val) != "secret-one" {
		t.Fatalf("got %q after reopen", val)
	}
}

func TestVaultListAndDelete(t *testing.T) {
	v := newTestVault(t)
	ctx := context.Background()
	for _, n := range []string{"a", "b", "c"} {
		if _, err := v.Put(ctx, n, []byte("v-"+n), nil); err != nil {
			t.Fatal(err)
		}
	}
	list, err := v.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("want 3 secrets, got %d", len(list))
	}
	if err := v.Delete(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	list, _ = v.List(ctx)
	if len(list) != 2 {
		t.Fatalf("want 2 after delete, got %d", len(list))
	}
}
