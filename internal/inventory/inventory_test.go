package inventory

import (
	"context"
	"errors"
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

func TestUpsertAndGetSSH(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	h := &Host{
		Name: "hk1", Transport: TransportSSH, Address: "1.2.3.4", Port: 22,
		SSHUser: "root", AuthKind: AuthKindPassword, AuthSecret: "hk1-pw",
		Tags: []string{"hk", "prod"},
	}
	if err := m.Upsert(ctx, h); err != nil {
		t.Fatal(err)
	}
	got, err := m.Get(ctx, "hk1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Address != "1.2.3.4" || got.SSHUser != "root" {
		t.Fatalf("wrong host: %+v", got)
	}
	if len(got.Tags) != 2 {
		t.Fatalf("tags: %v", got.Tags)
	}
}

func TestRequireAddressForSSH(t *testing.T) {
	m := newTestManager(t)
	err := m.Upsert(context.Background(), &Host{Name: "x", Transport: TransportSSH})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestSDJZRelayValidation(t *testing.T) {
	m := newTestManager(t)
	err := m.Upsert(context.Background(), &Host{Name: "nmg", Transport: TransportSDJZRelay})
	if err == nil {
		t.Fatal("expected error: missing relay_secret")
	}

	if err := m.Upsert(context.Background(), &Host{
		Name: "nmg", Transport: TransportSDJZRelay, RelaySecret: "nmg-token",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestResolveTag(t *testing.T) {
	m := newTestManager(t)
	ctx := context.Background()
	for _, n := range []string{"a", "b"} {
		_ = m.Upsert(ctx, &Host{
			Name: n, Transport: TransportSSH, Address: "1.1.1.1", Port: 22,
			SSHUser: "root", AuthKind: AuthKindPassword, AuthSecret: "x", Tags: []string{"hk"},
		})
	}
	hosts, err := m.ResolveTag(ctx, "hk")
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 2 {
		t.Fatalf("want 2 by tag, got %d", len(hosts))
	}
	hosts, err = m.ResolveTag(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0].Name != "a" {
		t.Fatalf("expected exact name match for 'a', got %+v", hosts)
	}
	if _, err := m.ResolveTag(ctx, "missing"); err == nil {
		t.Fatal("expected ErrHostNotFound")
	} else if !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("expected ErrHostNotFound, got %v", err)
	}
}
