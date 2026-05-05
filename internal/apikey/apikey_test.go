package apikey

import (
	"context"
	"testing"

	"github.com/shuakami/hush/internal/store"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(st)
}

func TestMintAndVerify(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	raw, k, err := m.Mint(ctx, "ci-key", []string{"secret:read", "host:exec"})
	if err != nil {
		t.Fatal(err)
	}
	if raw == "" {
		t.Fatal("raw empty")
	}
	if k.Name != "ci-key" {
		t.Fatal("name wrong")
	}
	got, err := m.Verify(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != k.ID {
		t.Fatal("id mismatch")
	}
	if !got.HasScope("secret:read") {
		t.Fatal("expected secret:read scope")
	}
	if got.HasScope("apikey:write") {
		t.Fatal("did not expect apikey:write scope")
	}
}

func TestRevoke(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	raw, k, err := m.Mint(ctx, "doomed", []string{"*"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Revoke(ctx, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Verify(ctx, raw); err == nil {
		t.Fatal("expected revoked key to fail verify")
	}
}

func TestWildcardScope(t *testing.T) {
	k := &Key{Scopes: []string{"secret:*"}}
	if !k.HasScope("secret:read") {
		t.Fatal("secret:* should match secret:read")
	}
	if !k.HasScope("secret:write") {
		t.Fatal("secret:* should match secret:write")
	}
	if k.HasScope("host:exec") {
		t.Fatal("secret:* should NOT match host:exec")
	}
	full := &Key{Scopes: []string{"*"}}
	if !full.HasScope("anything") {
		t.Fatal("* should match anything")
	}
}
