// Package ratelimit provides two-level rate limiting for NACK requests:
// per-IP token bucket and per-LookupSeq sliding window.
//
// BRC-124 removed SenderID and SequenceID (uint32) from the wire format;
// the only NACK-level identifier is LookupSeq (uint64 XXH64 hash).
package ratelimit

import (
	"net"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Level represents the rate limiting level.
type Level string

const (
	LevelIP       Level = "ip"
	LevelSequence Level = "sequence"
)

// Limiter provides two-level rate limiting.
type Limiter struct {
	ipLimiter       *ipLimiter
	sequenceLimiter *sequenceLimiter
}

// Config holds rate limiting configuration.
type Config struct {
	IPRate         float64       // Tokens per second
	IPBurst        int           // Burst size
	SenderRate     float64       // Requests per window
	SenderWindow   time.Duration // Sliding window duration
	SequenceMax    int           // Max requests per SequenceID per SequenceWindow
	SequenceWindow time.Duration // Sliding window duration for SequenceID limiter
}

// New constructs a new rate limiter.
func New(cfg Config) *Limiter {
	if cfg.SequenceWindow <= 0 {
		// Default to 1 minute if unset — matches SenderWindow default and
		// prevents the legacy "counter never resets" behaviour.
		cfg.SequenceWindow = time.Minute
	}
	return &Limiter{
		ipLimiter:       newIPLimiter(cfg.IPRate, cfg.IPBurst),
		sequenceLimiter: newSequenceLimiter(cfg.SequenceMax, cfg.SequenceWindow),
	}
}

// Allow checks if the NACK request should be allowed.
// srcIP is the listener source address; lookupSeq is the LookupSeq field from
// the NACK datagram (CurSeq or PrevSeq hash, BRC-124).
// Returns (true, "") if allowed, (false, level) if rate limited.
func (r *Limiter) Allow(srcIP net.IP, lookupSeq uint64) (bool, Level) {
	if !r.ipLimiter.Allow(srcIP) {
		return false, LevelIP
	}
	if !r.sequenceLimiter.Allow(lookupSeq) {
		return false, LevelSequence
	}
	return true, ""
}

// ipLimiter provides token bucket rate limiting per source IP.
type ipLimiter struct {
	mu    sync.Mutex
	limit map[string]*rate.Limiter
	rate  rate.Limit
	burst int
}

func newIPLimiter(tokensPerSec float64, burst int) *ipLimiter {
	return &ipLimiter{
		limit: make(map[string]*rate.Limiter),
		rate:  rate.Limit(tokensPerSec),
		burst: burst,
	}
}

func (r *ipLimiter) Allow(ip net.IP) bool {
	key := ip.String()
	r.mu.Lock()
	limiter, ok := r.limit[key]
	if !ok {
		limiter = rate.NewLimiter(r.rate, r.burst)
		r.limit[key] = limiter
	}
	r.mu.Unlock()
	return limiter.Allow()
}

// sequenceLimiter provides sliding-window rate limiting per LookupSeq (uint64).
//
// The legacy implementation used a monotonic counter per SequenceID with no
// expiration. That caused long-lived flows to eventually exhaust the counter
// and drop every subsequent NACK for that flow, without any way to recover
// short of restarting the process. The sliding-window form bounds memory and
// self-heals: a SequenceID that has been quiet for [window] is re-admitted
// at full capacity.
type sequenceLimiter struct {
	mu     sync.Mutex
	seqs   map[uint64]*sequenceEntry
	max    int
	window time.Duration
}

type sequenceEntry struct {
	timestamps []time.Time
}

func newSequenceLimiter(max int, window time.Duration) *sequenceLimiter {
	return &sequenceLimiter{
		seqs:   make(map[uint64]*sequenceEntry),
		max:    max,
		window: window,
	}
}

func (r *sequenceLimiter) Allow(lookupSeq uint64) bool {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.seqs[lookupSeq]
	if !ok {
		entry = &sequenceEntry{timestamps: make([]time.Time, 0, r.max)}
		r.seqs[lookupSeq] = entry
	}

	// Drop timestamps outside the window. Expected working set is small
	// because the window is short relative to typical flow bursts.
	cutoff := now.Add(-r.window)
	validIdx := 0
	for _, ts := range entry.timestamps {
		if ts.After(cutoff) {
			entry.timestamps[validIdx] = ts
			validIdx++
		}
	}
	entry.timestamps = entry.timestamps[:validIdx]

	if len(entry.timestamps) >= r.max {
		return false
	}
	entry.timestamps = append(entry.timestamps, now)
	return true
}
