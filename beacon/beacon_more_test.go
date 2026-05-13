package beacon

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestSetRecorder(t *testing.T) {
	s := New(testConfig())
	if s.rec != nil {
		t.Error("rec should be nil by default")
	}
	s.SetRecorder(nil) // safe no-op
	if s.rec != nil {
		t.Error("still nil after SetRecorder(nil)")
	}
}

func TestRun_NoGroups(t *testing.T) {
	cfg := testConfig()
	cfg.Scope = 0x00 // no groups
	s := New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Run(ctx); err != nil {
		t.Errorf("Run with no groups: %v", err)
	}
}

func TestRun_StartsAndStops(t *testing.T) {
	// Pick a real iface for IPV6_MULTICAST_IF.
	ifs, _ := net.Interfaces()
	var iface *net.Interface
	for i := range ifs {
		if ifs[i].Flags&net.FlagLoopback != 0 {
			iface = &ifs[i]
			break
		}
	}
	if iface == nil {
		t.Skip("no iface")
	}
	cfg := testConfig()
	cfg.Iface = iface
	cfg.Interval = 50 * time.Millisecond
	s := New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return")
	}
}

func TestSend_WriteErrorIsLogged(t *testing.T) {
	// Construct a closed UDP conn; send must not panic.
	c, err := net.ListenUDP("udp6", &net.UDPAddr{})
	if err != nil {
		t.Skip(err)
	}
	_ = c.Close()
	s := New(testConfig())
	s.send([]*net.UDPConn{c}, s.buildADVERT())
}

func TestBuildADVERT_NilNACKAddr(t *testing.T) {
	cfg := testConfig()
	cfg.NACKAddr = nil
	s := New(cfg)
	buf := s.buildADVERT()
	// bytes 8..24 should be zero (IPv6 unspecified).
	for i := 8; i < 24; i++ {
		if buf[i] != 0 {
			t.Errorf("byte[%d] = 0x%02X, want 0", i, buf[i])
		}
	}
}
