package audit

import (
	"context"
	"testing"

	"github.com/shuakami/hush/internal/store"
)

func newTestLogger(t *testing.T) *Logger {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(st)
}

func TestAuditChainHappyPath(t *testing.T) {
	l := newTestLogger(t)
	ctx := context.Background()

	for _, action := range []string{"a", "b", "c"} {
		if _, err := l.Append(ctx, "actor", action, "tgt", map[string]interface{}{"x": 1}); err != nil {
			t.Fatal(err)
		}
	}
	bad, err := l.Verify(ctx)
	if err != nil {
		t.Fatalf("verify err: %v (record: %+v)", err, bad)
	}
	if bad != nil {
		t.Fatalf("unexpected bad record: %+v", bad)
	}
	tail, err := l.Tail(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 3 {
		t.Fatalf("tail length %d", len(tail))
	}
}

func TestAuditDetectsTamper(t *testing.T) {
	l := newTestLogger(t)
	ctx := context.Background()
	for _, action := range []string{"a", "b", "c"} {
		if _, err := l.Append(ctx, "actor", action, "tgt", nil); err != nil {
			t.Fatal(err)
		}
	}
	// Mutate the second row's target. Hash should fail.
	if _, err := l.st.DB().Exec(`UPDATE audit SET target = 'evil' WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	bad, err := l.Verify(ctx)
	if err == nil {
		t.Fatal("expected verify failure after tamper, got nil")
	}
	if bad == nil || bad.ID != 2 {
		t.Fatalf("expected tamper at record 2, got %+v", bad)
	}
}
