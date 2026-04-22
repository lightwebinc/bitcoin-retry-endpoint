// Command bitcoin-retry-endpoint caches multicast BSV transaction frames
// and retransmits them on demand via NACK requests.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/lightwebinc/bitcoin-shard-common/shard"

	"github.com/lightwebinc/bitcoin-retry-endpoint/cache"
	"github.com/lightwebinc/bitcoin-retry-endpoint/cache/memory"
	"github.com/lightwebinc/bitcoin-retry-endpoint/cache/redis"
	"github.com/lightwebinc/bitcoin-retry-endpoint/config"
	"github.com/lightwebinc/bitcoin-retry-endpoint/ingress"
	"github.com/lightwebinc/bitcoin-retry-endpoint/metrics"
	"github.com/lightwebinc/bitcoin-retry-endpoint/ratelimit"
	"github.com/lightwebinc/bitcoin-retry-endpoint/retransmit"
	"github.com/lightwebinc/bitcoin-retry-endpoint/server"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logLevel := slog.LevelInfo
	if cfg.Debug {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	slog.Info("bitcoin-retry-endpoint starting",
		"shard_bits", cfg.ShardBits,
		"num_groups", cfg.NumGroups,
		"scope", cfg.MCScope,
		"mc_port", cfg.ListenPort,
		"nack_port", cfg.NACKPort,
		"cache_backend", cfg.CacheBackend,
		"egress_port", cfg.EgressPort,
		"dedup_window", cfg.DedupWindow,
	)

	// Initialize metrics.
	rec, err := metrics.New(cfg.InstanceID, cfg.NumWorkers, cfg.OTLPEndpoint, cfg.OTLPInterval)
	if err != nil {
		return err
	}

	// Build shard engine.
	engine := shard.New(cfg.MCPrefix, cfg.MCMiddleBytes, cfg.ShardBits)

	// Build cache backend.
	var c cache.Cache
	var redisCache *redis.Cache
	switch cfg.CacheBackend {
	case "redis":
		redisCache, err = redis.New(cfg.RedisAddr, "bre:frame:")
		if err != nil {
			return err
		}
		c = redisCache
	case "memory":
		c = memory.New(cfg.CacheMaxKeys)
	default:
		slog.Warn("unknown cache backend, using memory", "backend", cfg.CacheBackend)
		c = memory.New(cfg.CacheMaxKeys)
	}
	defer c.Close()

	// Build multicast groups for ingress.
	groups, err := buildGroups(cfg, engine)
	if err != nil {
		return err
	}
	slog.Info("multicast groups", "count", len(groups))

	// Resolve ingress interface.
	mcIface, err := net.InterfaceByName(cfg.MCIface)
	if err != nil {
		return err
	}

	// Resolve egress interfaces.
	egressIfaces := make([]*net.Interface, 0, len(cfg.EgressIfaces))
	for _, name := range cfg.EgressIfaces {
		iface, err := net.InterfaceByName(name)
		if err != nil {
			return err
		}
		egressIfaces = append(egressIfaces, iface)
	}

	// Build rate limiter.
	rl := ratelimit.New(ratelimit.Config{
		IPRate:       cfg.RLIPRate,
		IPBurst:      cfg.RLIPBurst,
		SenderRate:   cfg.RLSenderRate,
		SenderWindow: cfg.RLSenderWindow,
		SequenceMax:  cfg.RLSequenceMax,
	})

	// Build retransmitter.
	retrans := retransmit.New(engine, egressIfaces, cfg.EgressPort, cfg.DedupWindow, redisCache, rec, cfg.Debug)
	if err := retrans.Open(); err != nil {
		return err
	}
	defer retrans.Close()

	// Build server.
	srv := server.New(cfg.NACKPort, c, rl, rec, retrans, cfg.NACKWorkers, cfg.Debug)

	// Build ingress worker.
	ing := ingress.New(mcIface, cfg.ListenPort, groups, c, rec, cfg.CacheTTL, cfg.Debug)

	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	// Start metrics server.
	wg.Add(1)
	go func() {
		defer wg.Done()
		rec.Serve(cfg.MetricsAddr, done)
	}()

	// Start ingress worker.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := ing.Run(ctx); err != nil {
			slog.Error("ingress exited with error", "err", err)
		}
	}()

	// Start server.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := srv.Run(ctx); err != nil {
			slog.Error("server exited with error", "err", err)
		}
	}()

	// Wait for signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	slog.Info("shutdown signal received", "signal", sig)

	if cfg.DrainTimeout > 0 {
		rec.SetDraining()
		slog.Info("draining", "timeout", cfg.DrainTimeout)
		time.Sleep(cfg.DrainTimeout)
	}

	cancel()
	close(done)
	wg.Wait()

	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	rec.Shutdown(ctx2)

	slog.Info("shutdown complete")
	return nil
}

func buildGroups(cfg *config.Config, engine *shard.Engine) ([]*net.UDPAddr, error) {
	groups := make([]*net.UDPAddr, cfg.NumGroups)
	for i := uint32(0); i < cfg.NumGroups; i++ {
		addr := engine.Addr(i, cfg.ListenPort)
		groups[i] = addr
	}
	return groups, nil
}
