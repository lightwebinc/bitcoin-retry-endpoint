// Package config loads and validates runtime configuration for
// bitcoin-retry-endpoint. Parameters are accepted from CLI flags first;
// environment variables serve as fallbacks; hard-coded defaults apply when
// neither is present.
package config

import (
	"flag"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Scopes maps a human-readable scope name to the two-byte big-endian IPv6
// multicast prefix. See RFC 4291 §2.7.
var Scopes = map[string]uint16{
	"link":   0xFF02,
	"site":   0xFF05,
	"org":    0xFF08,
	"global": 0xFF0E,
}

// Config holds all runtime parameters for the retry endpoint.
type Config struct {
	// Ingress (multicast receive)
	MCIface       string   // NIC for multicast ingress
	ListenPort    int      // Multicast listen port
	ShardBits     uint     // Number of txid prefix bits used as the group key (1–24)
	NumGroups     uint32   // Derived: 1 << ShardBits
	MCScope       string   // Human name; one of the keys in Scopes
	MCPrefix      uint16   // Derived from MCScope — upper 16 bits of the IPv6 group address
	MCBaseAddr    string   // Base IPv6 address for assigned address space (bytes 2-12)
	MCMiddleBytes [11]byte // Derived from MCBaseAddr — bytes 2-12 of multicast address

	// Cache
	CacheBackend string // "redis" or "memory"
	RedisAddr    string // Redis server address (e.g., "localhost:6379")
	CacheTTL     time.Duration
	CacheMaxKeys int // Maximum number of keys in cache (0 = no limit)

	// Server (NACK receive)
	NACKPort    int // NACK listen port (default 9300)
	NACKWorkers int // Worker goroutines for NACK processing

	// Retransmit
	EgressIfaces []string      // NIC names for multicast egress
	EgressPort   int           // Destination UDP port for retransmitted frames
	DedupWindow  time.Duration // Deduplication window (default 60s)

	// Rate limiting
	RLIPRate       float64       // IP rate limit (tokens per second)
	RLIPBurst      int           // IP burst size
	RLSenderRate   float64       // SenderID rate limit (requests per window)
	RLSenderWindow time.Duration // SenderID sliding window duration
	RLSequenceMax  int           // Max requests per SequenceID lifetime

	// Runtime
	NumWorkers   int           // Worker goroutines for multicast ingress (always 1)
	Debug        bool          // Enable per-packet debug logging
	DrainTimeout time.Duration // Pre-drain delay before closing sockets

	// Observability
	MetricsAddr  string        // HTTP bind address for /metrics, /healthz, /readyz
	InstanceID   string        // OTel service.instance.id
	OTLPEndpoint string        // gRPC OTLP endpoint (empty = disabled)
	OTLPInterval time.Duration // OTLP push interval
}

// Load parses flags and environment variables, validates all values, and
// returns a populated Config. It calls flag.Parse internally; callers
// must not call flag.Parse separately.
func Load() (*Config, error) {
	c := &Config{}

	flag.StringVar(&c.MCIface, "mc-iface", envStr("MC_IFACE", "eth0"),
		"NIC for multicast ingress")
	flag.IntVar(&c.ListenPort, "listen-port", envInt("LISTEN_PORT", 9001),
		"multicast listen port")

	flag.StringVar(&c.CacheBackend, "cache-backend", envStr("CACHE_BACKEND", "memory"),
		"cache backend: redis | memory")
	flag.StringVar(&c.RedisAddr, "redis-addr", envStr("REDIS_ADDR", "localhost:6379"),
		"Redis server address")
	flag.DurationVar(&c.CacheTTL, "cache-ttl", envDuration("CACHE_TTL", 10*time.Minute),
		"cache TTL for frames")
	flag.IntVar(&c.CacheMaxKeys, "cache-max-keys", envInt("CACHE_MAX_KEYS", 0),
		"maximum number of keys in cache (0 = no limit)")

	flag.IntVar(&c.NACKPort, "nack-port", envInt("NACK_PORT", 9300),
		"NACK listen port")
	flag.IntVar(&c.NACKWorkers, "nack-workers", envInt("NACK_WORKERS", runtime.NumCPU()),
		"NACK worker goroutines")

	egressFlag := flag.String("egress-iface", envStr("EGRESS_IFACE", "eth0"),
		"comma-separated NIC names for multicast egress")
	flag.IntVar(&c.EgressPort, "egress-port", envInt("EGRESS_PORT", 9001),
		"destination UDP port for retransmitted frames")
	flag.DurationVar(&c.DedupWindow, "dedup-window", envDuration("DEDUP_WINDOW", 60*time.Second),
		"retransmission deduplication window")

	flag.Float64Var(&c.RLIPRate, "rl-ip-rate", envFloat("RL_IP_RATE", 100),
		"IP rate limit (tokens per second)")
	flag.IntVar(&c.RLIPBurst, "rl-ip-burst", envInt("RL_IP_BURST", 10),
		"IP rate limit burst size")
	flag.Float64Var(&c.RLSenderRate, "rl-sender-rate", envFloat("RL_SENDER_RATE", 50),
		"SenderID rate limit (requests per window)")
	flag.DurationVar(&c.RLSenderWindow, "rl-sender-window", envDuration("RL_SENDER_WINDOW", time.Minute),
		"SenderID sliding window duration")
	flag.IntVar(&c.RLSequenceMax, "rl-sequence-max", envInt("RL_SEQUENCE_MAX", 100),
		"max requests per SequenceID lifetime")

	flag.StringVar(&c.MCScope, "scope", envStr("MC_SCOPE", "site"),
		"multicast scope: link | site | org | global")
	flag.StringVar(&c.MCBaseAddr, "mc-base-addr", envStr("MC_BASE_ADDR", ""),
		"base IPv6 address for assigned multicast address space (bytes 2-12)")

	flag.BoolVar(&c.Debug, "debug", envBool("DEBUG", false),
		"enable per-packet debug logging")
	flag.DurationVar(&c.DrainTimeout, "drain-timeout", envDuration("DRAIN_TIMEOUT", 0),
		"pre-drain delay before closing sockets")

	flag.StringVar(&c.MetricsAddr, "metrics-addr", envStr("METRICS_ADDR", ":9400"),
		"HTTP bind address for /metrics, /healthz, /readyz")
	flag.StringVar(&c.InstanceID, "instance", envStr("INSTANCE_ID", ""),
		"OTel service.instance.id (default: hostname)")
	flag.StringVar(&c.OTLPEndpoint, "otlp-endpoint", envStr("OTLP_ENDPOINT", ""),
		"OTLP gRPC endpoint for metric push (empty = disabled)")
	otlpInterval := flag.Duration("otlp-interval", envDuration("OTLP_INTERVAL", 30*time.Second),
		"OTLP push interval")

	shardBitsDefault := uint(envInt("SHARD_BITS", 16))
	bits := flag.Uint("shard-bits", shardBitsDefault,
		"txid prefix bit width used as the shard key (1–24)")

	flag.Parse()

	// Validate shard bit width.
	if *bits < 1 || *bits > 24 {
		return nil, fmt.Errorf("shard-bits must be in [1, 24], got %d", *bits)
	}
	c.ShardBits = *bits
	c.NumGroups = 1 << c.ShardBits
	c.OTLPInterval = *otlpInterval

	// Resolve multicast scope.
	prefix, ok := Scopes[c.MCScope]
	if !ok {
		return nil, fmt.Errorf("unknown scope %q; valid values: link, site, org, global", c.MCScope)
	}
	c.MCPrefix = prefix

	// Parse base IPv6 address for middle bytes if provided.
	if c.MCBaseAddr != "" {
		ip := net.ParseIP(c.MCBaseAddr)
		if ip == nil {
			return nil, fmt.Errorf("invalid base IPv6 address %q", c.MCBaseAddr)
		}
		ip16 := ip.To16()
		if ip16 == nil {
			return nil, fmt.Errorf("base address must be a valid 16-byte IPv6 address, got %q", c.MCBaseAddr)
		}
		if ip.To4() != nil {
			return nil, fmt.Errorf("base address must be IPv6, got IPv4 address %q", c.MCBaseAddr)
		}
		copy(c.MCMiddleBytes[:], ip16[2:13])
	} else {
		for i := range c.MCMiddleBytes {
			c.MCMiddleBytes[i] = 0
		}
	}

	// Validate cache backend.
	if c.CacheBackend != "redis" && c.CacheBackend != "memory" {
		return nil, fmt.Errorf("cache-backend must be 'redis' or 'memory', got %q", c.CacheBackend)
	}

	// Ingress is always single worker (SO_REUSEPORT multicast duplication).
	c.NumWorkers = 1

	// Default NACK workers to NumCPU if set to zero.
	if c.NACKWorkers <= 0 {
		c.NACKWorkers = runtime.NumCPU()
	}

	// Parse and validate egress interfaces.
	for _, name := range strings.Split(*egressFlag, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, err := net.InterfaceByName(name); err != nil {
			return nil, fmt.Errorf("egress interface %q not found: %w", name, err)
		}
		c.EgressIfaces = append(c.EgressIfaces, name)
	}
	if len(c.EgressIfaces) == 0 {
		return nil, fmt.Errorf("at least one egress interface must be specified via -egress-iface")
	}

	return c, nil
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
