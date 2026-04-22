package memory

import (
	"testing"
	"time"
)

func TestStoreAndRetrieve(t *testing.T) {
	c := New(0)
	defer func() { _ = c.Close() }()

	key := []byte("key1")
	val := []byte("value1")
	if err := c.Store(key, val, time.Minute); err != nil {
		t.Fatal(err)
	}

	got, err := c.Retrieve(key)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(val) {
		t.Fatalf("got %q, want %q", got, val)
	}
}

func TestMiss(t *testing.T) {
	c := New(0)
	defer func() { _ = c.Close() }()

	got, err := c.Retrieve([]byte("missing"))
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil for missing key, got %q", got)
	}
}

func TestTTLExpiry(t *testing.T) {
	c := New(0)
	defer func() { _ = c.Close() }()

	key := []byte("expiring")
	if err := c.Store(key, []byte("v"), 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)

	got, err := c.Retrieve(key)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil after TTL expiry, got %q", got)
	}
}

func TestDelete(t *testing.T) {
	c := New(0)
	defer func() { _ = c.Close() }()

	key := []byte("toDelete")
	if err := c.Store(key, []byte("v"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(key); err != nil {
		t.Fatal(err)
	}
	got, err := c.Retrieve(key)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil after delete, got %q", got)
	}
}

func TestMaxKeysEviction(t *testing.T) {
	c := New(3)
	defer func() { _ = c.Close() }()

	for i := 0; i < 5; i++ {
		key := []byte{byte(i)}
		if err := c.Store(key, []byte{byte(i)}, time.Minute); err != nil {
			t.Fatal(err)
		}
	}

	c.mu.RLock()
	size := len(c.data)
	c.mu.RUnlock()

	if size > 3 {
		t.Fatalf("expected at most 3 keys after eviction, got %d", size)
	}
}

func TestOverwrite(t *testing.T) {
	c := New(0)
	defer func() { _ = c.Close() }()

	key := []byte("k")
	if err := c.Store(key, []byte("v1"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := c.Store(key, []byte("v2"), time.Minute); err != nil {
		t.Fatal(err)
	}
	got, err := c.Retrieve(key)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v2" {
		t.Fatalf("expected overwrite to v2, got %q", got)
	}
}
