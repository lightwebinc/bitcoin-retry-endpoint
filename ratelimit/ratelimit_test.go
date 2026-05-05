package ratelimit

import (
	"net"
	"testing"
	"time"
)

func TestIPRateLimit(t *testing.T) {
	l := New(Config{
		IPRate:      2,
		IPBurst:     2,
		SequenceMax: 100,
	})

	ip := net.ParseIP("::1")

	// First two should pass (burst).
	for i := uint64(0); i < 2; i++ {
		ok, _ := l.Allow(ip, i)
		if !ok {
			t.Fatalf("request %d should be allowed within burst", i)
		}
	}

	// Third should be dropped.
	ok, level := l.Allow(ip, 99)
	if ok {
		t.Fatal("expected IP rate limit to drop request")
	}
	if level != LevelIP {
		t.Fatalf("expected level %q, got %q", LevelIP, level)
	}
}

func TestSequenceRateLimit(t *testing.T) {
	l := New(Config{
		IPRate:      1000,
		IPBurst:     1000,
		SequenceMax: 3,
	})

	ip := net.ParseIP("::1")
	const seqID = uint64(0xdeadbeef01020304)

	// First three requests for same seqID should pass.
	for i := 0; i < 3; i++ {
		ok, _ := l.Allow(ip, seqID)
		if !ok {
			t.Fatalf("request %d should be allowed within sequence max", i)
		}
	}

	// Fourth should be dropped.
	ok, level := l.Allow(ip, seqID)
	if ok {
		t.Fatal("expected sequence rate limit to drop request")
	}
	if level != LevelSequence {
		t.Fatalf("expected level %q, got %q", LevelSequence, level)
	}
}

func TestDifferentSequencesIndependent(t *testing.T) {
	l := New(Config{
		IPRate:      1000,
		IPBurst:     1000,
		SequenceMax: 1,
	})

	ip := net.ParseIP("::1")

	// Each unique LookupSeq hash gets its own counter.
	for i := uint64(0); i < 5; i++ {
		ok, _ := l.Allow(ip, i<<32|0xdeadbeef)
		if !ok {
			t.Fatalf("seqID %d should be allowed (first request)", i)
		}
	}
}

// TestSequenceLimiterSlidingWindow guards against regressing to the legacy
// monotonic-counter behaviour: once a SequenceID's window elapses, new
// requests must be admitted again rather than permanently blocked.
func TestSequenceLimiterSlidingWindow(t *testing.T) {
	l := New(Config{
		IPRate:         1000,
		IPBurst:        1000,
		SequenceMax:    2,
		SequenceWindow: 50 * time.Millisecond,
	})

	ip := net.ParseIP("::1")
	const seqID = uint64(0xaabbccdd11223344)

	// Fill the window.
	for i := 0; i < 2; i++ {
		if ok, _ := l.Allow(ip, seqID); !ok {
			t.Fatalf("request %d should be allowed within window", i)
		}
	}
	// Third in the same window must be dropped.
	if ok, level := l.Allow(ip, seqID); ok || level != LevelSequence {
		t.Fatalf("expected sequence drop, got ok=%v level=%q", ok, level)
	}

	// After the window elapses the limiter must self-heal.
	time.Sleep(75 * time.Millisecond)
	if ok, _ := l.Allow(ip, seqID); !ok {
		t.Fatal("expected sequence limiter to admit requests after window expiry")
	}
}

// TestSequenceLimiterDefaultWindow verifies that a zero-value SequenceWindow
// is replaced with a sane default instead of locking the limiter open/closed.
func TestSequenceLimiterDefaultWindow(t *testing.T) {
	l := New(Config{
		IPRate:      1000,
		IPBurst:     1000,
		SequenceMax: 1,
		// SequenceWindow deliberately left zero.
	})
	ip := net.ParseIP("::1")
	const seqID = uint64(0x0102030405060708)
	if ok, _ := l.Allow(ip, seqID); !ok {
		t.Fatal("first request must pass regardless of window default")
	}
	if ok, level := l.Allow(ip, seqID); ok || level != LevelSequence {
		t.Fatalf("second request must be sequence-limited; got ok=%v level=%q", ok, level)
	}
}
