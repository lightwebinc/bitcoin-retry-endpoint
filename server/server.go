// Package server implements the UDP NACK receiver for bitcoin-retry-endpoint.
package server

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/lightwebinc/bitcoin-shard-common/frame"

	"github.com/lightwebinc/bitcoin-retry-endpoint/cache"
	"github.com/lightwebinc/bitcoin-retry-endpoint/metrics"
	"github.com/lightwebinc/bitcoin-retry-endpoint/ratelimit"
)

const NACKSize = 72 // bytes

// Server receives NACK requests and coordinates retransmissions.
type Server struct {
	port        int
	cache       cache.Cache
	rateLimiter *ratelimit.Limiter
	rec         *metrics.Recorder
	retransmit  Retransmitter
	workers     int
	debug       bool
	log         *slog.Logger
}

// Retransmitter is the interface for retransmitting cached frames.
type Retransmitter interface {
	Retransmit(raw []byte, txID [32]byte) error
}

// New constructs a Server.
func New(
	port int,
	cache cache.Cache,
	rateLimiter *ratelimit.Limiter,
	rec *metrics.Recorder,
	retransmit Retransmitter,
	workers int,
	debug bool,
) *Server {
	return &Server{
		port:        port,
		cache:       cache,
		rateLimiter: rateLimiter,
		rec:         rec,
		retransmit:  retransmit,
		workers:     workers,
		debug:       debug,
		log:         slog.Default().With("component", "server"),
	}
}

// Run starts the UDP server with a worker pool.
func (s *Server) Run(ctx context.Context) error {
	conn, err := net.ListenPacket("udp6", fmt.Sprintf("[::]:%d", s.port))
	if err != nil {
		return fmt.Errorf("server: listen: %w", err)
	}
	defer func() { _ = conn.Close() }()

	s.log.Info("NACK server listening", "port", s.port, "workers", s.workers)

	if s.rec != nil {
		s.rec.WorkerReady()
		defer s.rec.WorkerDone()
	}

	type nackRequest struct {
		data  []byte
		srcIP net.IP
	}

	// Worker pool for parallel request handling.
	requests := make(chan nackRequest, 100)
	var wg sync.WaitGroup

	// Start workers.
	for i := 0; i < s.workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case req, ok := <-requests:
					if !ok {
						return
					}
					s.processNACK(workerID, req.data, req.srcIP)
				}
			}
		}(i)
	}

	buf := make([]byte, NACKSize)
	for {
		select {
		case <-ctx.Done():
			close(requests)
			wg.Wait()
			return nil
		default:
			_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, src, err := conn.ReadFrom(buf)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				if ctx.Err() != nil {
					close(requests)
					wg.Wait()
					return nil
				}
				s.log.Error("read error", "err", err, "src", src)
				continue
			}
			if n != NACKSize {
				s.log.Warn("invalid NACK size", "len", n, "src", src)
				continue
			}

			// Extract source IP.
			var srcIP net.IP
			if udpAddr, ok := src.(*net.UDPAddr); ok {
				srcIP = udpAddr.IP
			}
			if srcIP == nil {
				srcIP = net.IPv6unspecified
			}

			// Copy the datagram for the worker.
			datagram := make([]byte, NACKSize)
			copy(datagram, buf[:n])

			select {
			case requests <- nackRequest{data: datagram, srcIP: srcIP}:
			case <-ctx.Done():
				close(requests)
				wg.Wait()
				return nil
			}
		}
	}
}

func (s *Server) processNACK(workerID int, datagram []byte, srcIP net.IP) {
	if s.rec != nil {
		s.rec.NACKRequest()
	}

	// Validate NACK format.
	if err := validateNACK(datagram); err != nil {
		s.log.Debug("invalid NACK", "err", err)
		return
	}

	// Extract fields.
	txID := extractTxID(datagram)
	senderID := extractSenderID(datagram)
	sequenceID := extractSequenceID(datagram)
	seqNum := extractSeqNum(datagram)

	// Rate limiting.
	allowed, level := s.rateLimiter.Allow(srcIP, senderID, sequenceID)
	if !allowed {
		if s.rec != nil {
			s.rec.RateLimitDrop(string(level))
		}
		if s.debug {
			s.log.Debug("rate limited", "level", level)
		}
		return
	}

	// Build cache key.
	key := make([]byte, 32)
	copy(key[0:16], senderID[:])
	binary.BigEndian.PutUint64(key[16:24], sequenceID)
	binary.BigEndian.PutUint64(key[24:32], seqNum)

	// Retrieve from cache.
	raw, err := s.cache.Retrieve(key)
	if err != nil {
		if s.rec != nil {
			s.rec.CacheError()
		}
		s.log.Error("cache retrieve error", "err", err)
		return
	}
	if raw == nil {
		if s.rec != nil {
			s.rec.CacheMiss()
		}
		if s.debug {
			s.log.Debug("cache miss", "seq", seqNum)
		}
		return
	}

	if s.rec != nil {
		s.rec.CacheHit()
	}

	// Retransmit.
	if err := s.retransmit.Retransmit(raw, txID); err != nil {
		s.log.Error("retransmit error", "err", err)
		return
	}

	if s.rec != nil {
		s.rec.Retransmit()
	}

	if s.debug {
		s.log.Debug("retransmitted frame", "txid", fmt.Sprintf("%x", txID[:8]), "seq", seqNum)
	}
}

// validateNACK checks the NACK datagram format.
func validateNACK(datagram []byte) error {
	if len(datagram) != NACKSize {
		return fmt.Errorf("invalid NACK size: %d", len(datagram))
	}

	// Check magic (bytes 0-3).
	magic := binary.BigEndian.Uint32(datagram[0:4])
	if magic != frame.MagicBSV {
		return fmt.Errorf("invalid magic: 0x%08X", magic)
	}

	// Check protocol version (bytes 4-5).
	proto := binary.BigEndian.Uint16(datagram[4:6])
	if proto != frame.ProtoVer {
		return fmt.Errorf("invalid protocol version: %d", proto)
	}

	// Check message type (byte 6) - should be 0x10 for NACK.
	msgType := datagram[6]
	if msgType != 0x10 {
		return fmt.Errorf("invalid message type: 0x%02X", msgType)
	}

	return nil
}

// extractTxID extracts the TxID from a NACK datagram (bytes 8-40).
func extractTxID(datagram []byte) [32]byte {
	var txID [32]byte
	copy(txID[:], datagram[8:40])
	return txID
}

// extractSenderID extracts the SenderID from a NACK datagram (bytes 48-64).
func extractSenderID(datagram []byte) [16]byte {
	var senderID [16]byte
	copy(senderID[:], datagram[48:64])
	return senderID
}

// extractSequenceID extracts the SequenceID from a NACK datagram (bytes 64-72).
func extractSequenceID(datagram []byte) uint64 {
	return binary.BigEndian.Uint64(datagram[64:72])
}

// extractSeqNum extracts the SeqNum from a NACK datagram (bytes 40-48).
func extractSeqNum(datagram []byte) uint64 {
	return binary.BigEndian.Uint64(datagram[40:48])
}
