package redis

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func newCache(t *testing.T) (*Cache, *miniredis.Miniredis) {
	t.Helper()
	s, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	c, err := New(s.Addr(), "bre:test:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, s
}

func TestNew_PingFailure(t *testing.T) {
	if _, err := New("127.0.0.1:1", "x:"); err == nil {
		t.Error("expected error for unreachable Redis")
	}
}

func TestStoreRetrieve(t *testing.T) {
	c, _ := newCache(t)
	if err := c.Store([]byte("k"), []byte("v"), time.Minute); err != nil {
		t.Fatal(err)
	}
	got, err := c.Retrieve([]byte("k"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v" {
		t.Errorf("got %q want v", got)
	}
}

func TestRetrieve_Missing(t *testing.T) {
	c, _ := newCache(t)
	got, err := c.Retrieve([]byte("absent"))
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil for missing key, got %q", got)
	}
}

func TestDelete(t *testing.T) {
	c, _ := newCache(t)
	_ = c.Store([]byte("k"), []byte("v"), time.Minute)
	if err := c.Delete([]byte("k")); err != nil {
		t.Fatal(err)
	}
	got, _ := c.Retrieve([]byte("k"))
	if got != nil {
		t.Errorf("expected deleted, got %q", got)
	}
}

func TestSetNX_Atomicity(t *testing.T) {
	c, _ := newCache(t)
	ok, err := c.SetNX([]byte("k"), []byte("first"), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("first SetNX should succeed")
	}
	ok, err = c.SetNX([]byte("k"), []byte("second"), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("second SetNX must fail (key exists)")
	}
	got, _ := c.Retrieve([]byte("k"))
	if string(got) != "first" {
		t.Errorf("value should remain 'first', got %q", got)
	}
}

func TestStoreTTL(t *testing.T) {
	c, m := newCache(t)
	if err := c.Store([]byte("k"), []byte("v"), 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	// Advance miniredis clock past TTL.
	m.FastForward(200 * time.Millisecond)
	got, err := c.Retrieve([]byte("k"))
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected expired key, got %q", got)
	}
}

func TestKeyPrefix(t *testing.T) {
	c, m := newCache(t)
	_ = c.Store([]byte("foo"), []byte("v"), time.Minute)
	// Verify the prefix was applied.
	if v, err := m.Get("bre:test:foo"); err != nil || v != "v" {
		t.Errorf("prefix mismatch: %q err=%v", v, err)
	}
}

func TestClose(t *testing.T) {
	c, _ := newCache(t)
	if err := c.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}
