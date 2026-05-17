package ingress

import (
	"encoding/binary"
	"sync"
	"testing"
	"time"

	"github.com/lightwebinc/bitcoin-shard-common/frame"
)

// mockCache captures Store calls for assertion.
type mockCache struct {
	mu     sync.Mutex
	stores []storeCall
}

type storeCall struct {
	key []byte
	val []byte
	ttl time.Duration
}

func (m *mockCache) Store(key, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := make([]byte, len(key))
	copy(k, key)
	v := make([]byte, len(value))
	copy(v, value)
	m.stores = append(m.stores, storeCall{key: k, val: v, ttl: ttl})
	return nil
}

func (m *mockCache) Retrieve([]byte) ([]byte, error) { return nil, nil }
func (m *mockCache) Delete([]byte) error             { return nil }
func (m *mockCache) Close() error                    { return nil }

func (m *mockCache) storeCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.stores)
}

func (m *mockCache) storeAt(i int) storeCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stores[i]
}

// buildRaw encodes a BRC-124/BRC-128 frame with the given HashKey, SeqNum, and payload.
func buildRaw(t *testing.T, hashKey, seqNum uint64, payload []byte) []byte {
	t.Helper()
	f := &frame.Frame{
		Version: frame.FrameVerV2,
		HashKey: hashKey,
		SeqNum:  seqNum,
		Payload: payload,
	}
	f.TxID[0] = 0xAB
	buf := make([]byte, frame.HeaderSize+len(payload))
	n, err := frame.Encode(f, buf)
	if err != nil {
		t.Fatalf("frame.Encode: %v", err)
	}
	return buf[:n]
}

func newTestWorker(mc *mockCache) *Worker {
	return &Worker{
		cache: mc,
		ttl:   60 * time.Second,
	}
}

func TestProcessFrame_SingleIndex(t *testing.T) {
	mc := &mockCache{}
	w := newTestWorker(mc)

	hashKey := uint64(0xAABBCCDDEEFF0011)
	seqNum := uint64(0x1122334455667788)
	raw := buildRaw(t, hashKey, seqNum, []byte("tx-payload"))

	w.processFrame(raw)

	if mc.storeCount() != 1 {
		t.Fatalf("expected 1 Store call, got %d", mc.storeCount())
	}

	// Single key: HashKey (8B) || SeqNum (8B) → raw frame
	entry := mc.storeAt(0)
	if len(entry.key) != 16 {
		t.Errorf("key len = %d, want 16", len(entry.key))
	}
	gotHashKey := binary.BigEndian.Uint64(entry.key[0:8])
	if gotHashKey != hashKey {
		t.Errorf("key HashKey = 0x%016X, want 0x%016X", gotHashKey, hashKey)
	}
	gotSeqNum := binary.BigEndian.Uint64(entry.key[8:16])
	if gotSeqNum != seqNum {
		t.Errorf("key SeqNum = 0x%016X, want 0x%016X", gotSeqNum, seqNum)
	}
	if len(entry.val) != len(raw) {
		t.Errorf("value len = %d, want %d", len(entry.val), len(raw))
	}
}

func TestProcessFrame_ZeroSeqNum_Skip(t *testing.T) {
	mc := &mockCache{}
	w := newTestWorker(mc)

	raw := buildRaw(t, 0x1234, 0, []byte("payload"))
	w.processFrame(raw)

	if mc.storeCount() != 0 {
		t.Errorf("expected 0 Store calls for SeqNum=0, got %d", mc.storeCount())
	}
}

func TestProcessFrame_ZeroHashKey_Stored(t *testing.T) {
	mc := &mockCache{}
	w := newTestWorker(mc)

	seqNum := uint64(0xFFEEDDCCBBAA9988)
	raw := buildRaw(t, 0, seqNum, []byte("first-in-flow"))
	w.processFrame(raw)

	if mc.storeCount() != 1 {
		t.Fatalf("expected 1 Store call, got %d", mc.storeCount())
	}
	entry := mc.storeAt(0)
	if len(entry.key) != 16 {
		t.Errorf("key len = %d, want 16", len(entry.key))
	}
}

func TestProcessFrame_DecodeError(t *testing.T) {
	mc := &mockCache{}
	w := newTestWorker(mc)

	w.processFrame([]byte{0xFF, 0xFF}) // too short, bad magic
	if mc.storeCount() != 0 {
		t.Errorf("expected 0 Store calls for corrupt input, got %d", mc.storeCount())
	}
}

func TestProcessFrame_TTLPropagated(t *testing.T) {
	mc := &mockCache{}
	w := newTestWorker(mc)
	w.ttl = 42 * time.Second

	raw := buildRaw(t, 0x1111111111111111, 0x22, nil)
	w.processFrame(raw)

	if mc.storeCount() < 1 {
		t.Fatal("expected at least 1 Store call")
	}
	if mc.storeAt(0).ttl != 42*time.Second {
		t.Errorf("Store TTL = %v, want 42s", mc.storeAt(0).ttl)
	}
}
