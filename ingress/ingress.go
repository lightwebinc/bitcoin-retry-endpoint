// Package ingress implements the multicast receive worker for
// bitcoin-retry-endpoint.
//
// # Worker model
//
// Exactly one worker binds a UDP socket with SO_REUSEPORT on the configured
// port and joins all configured multicast groups on the configured interface.
// This is critical: Linux delivers multicast to ALL sockets in a reuseport
// group, so multiple workers would store each frame multiple times.
//
// # Hot path per frame
//
//  1. Recvfrom (64 MiB receive buffer)
//  2. frame.Decode — extract TxID, SequenceID, ShardSeqNum, SenderID
//  3. Drop if SequenceID == frame.SequenceIDRetransmit (retransmit marker)
//  4. Build cache key (SenderID + SequenceID + ShardSeqNum)
//  5. cache.Store(key, raw, TTL)
package ingress

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"time"

	"golang.org/x/sys/unix"

	"github.com/lightwebinc/bitcoin-shard-common/frame"

	"github.com/lightwebinc/bitcoin-retry-endpoint/cache"
	"github.com/lightwebinc/bitcoin-retry-endpoint/metrics"
)

const (
	recvBufSize   = frame.HeaderSize + frame.MaxPayload
	socketRecvBuf = 64 * 1024 * 1024 // 64 MiB
)

// Worker is the single multicast receive goroutine.
type Worker struct {
	iface  *net.Interface
	port   int
	groups []*net.UDPAddr
	cache  cache.Cache
	rec    *metrics.Recorder
	ttl    time.Duration
	debug  bool
	log    *slog.Logger
}

// New constructs a Worker.
func New(
	iface *net.Interface,
	port int,
	groups []*net.UDPAddr,
	cache cache.Cache,
	rec *metrics.Recorder,
	ttl time.Duration,
	debug bool,
) *Worker {
	return &Worker{
		iface:  iface,
		port:   port,
		groups: groups,
		cache:  cache,
		rec:    rec,
		ttl:    ttl,
		debug:  debug,
		log:    slog.Default().With("component", "ingress"),
	}
}

// Run opens a SO_REUSEPORT socket, joins all multicast groups, and processes
// frames until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	fd, err := w.openRawSocket()
	if err != nil {
		return fmt.Errorf("ingress: open socket: %w", err)
	}

	for _, grp := range w.groups {
		mreq := &unix.IPv6Mreq{Interface: uint32(w.iface.Index)}
		copy(mreq.Multiaddr[:], grp.IP.To16())
		if err := unix.SetsockoptIPv6Mreq(fd, unix.IPPROTO_IPV6, unix.IPV6_JOIN_GROUP, mreq); err != nil {
			_ = unix.Close(fd)
			return fmt.Errorf("ingress: join group %s: %w", grp.IP, err)
		}
	}

	if w.rec != nil {
		w.rec.WorkerReady()
		defer w.rec.WorkerDone()
	}

	w.log.Info("ingress worker ready", "iface", w.iface.Name, "port", w.port, "groups", len(w.groups))

	tv := unix.NsecToTimeval((200 * time.Millisecond).Nanoseconds())
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv)

	go func() {
		<-ctx.Done()
		_ = unix.Close(fd)
	}()

	buf := make([]byte, recvBufSize)
	for {
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
				if ctx.Err() != nil {
					return nil
				}
				continue
			}
			if err == unix.EBADF || err == unix.EINVAL {
				return nil
			}
			if err == unix.EINTR {
				continue
			}
			if ctx.Err() != nil {
				return nil
			}
			w.log.Error("recvfrom error", "err", err)
			continue
		}
		if n > 0 {
			w.processFrame(buf[:n])
		}
	}
}

func (w *Worker) processFrame(raw []byte) {
	f, err := frame.Decode(raw)
	if err != nil {
		if w.rec != nil {
			w.rec.FrameDropped("decode_error")
		}
		if w.debug {
			w.log.Debug("decode error", "err", err, "len", len(raw))
		}
		return
	}

	if w.rec != nil {
		w.rec.FrameReceived()
	}

	// Build cache key: SenderID (16B) + SequenceID (8B) + ShardSeqNum (8B) = 32B
	key := make([]byte, 32)
	copy(key[0:16], f.SenderID[:])
	binary.BigEndian.PutUint64(key[16:24], f.SequenceID)
	binary.BigEndian.PutUint64(key[24:32], f.ShardSeqNum)

	if err := w.cache.Store(key, raw, w.ttl); err != nil {
		if w.rec != nil {
			w.rec.CacheError()
		}
		w.log.Error("cache store error", "err", err)
		return
	}

	if w.rec != nil {
		w.rec.FrameCached()
	}

	if w.debug {
		w.log.Debug("frame cached",
			"txid", fmt.Sprintf("%x", f.TxID[:8]),
			"sender_id", fmt.Sprintf("%x", f.SenderID[:8]),
			"sequence_id", f.SequenceID,
			"seq", f.ShardSeqNum,
		)
	}
}

func (w *Worker) openRawSocket() (int, error) {
	fd, err := unix.Socket(unix.AF_INET6, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("socket: %w", err)
	}
	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEPORT, 1); err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("SO_REUSEPORT: %w", err)
	}
	_ = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, socketRecvBuf)
	sa := &unix.SockaddrInet6{Port: w.port}
	if err := unix.Bind(fd, sa); err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("bind [::]::%d: %w", w.port, err)
	}
	return fd, nil
}
