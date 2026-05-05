package server

import (
	"encoding/binary"
	"testing"

	"github.com/lightwebinc/bitcoin-shard-common/frame"
)

func buildNACK(msgType byte, lookupType byte, lookupSeq uint64) []byte {
	buf := make([]byte, NACKSize)
	binary.BigEndian.PutUint32(buf[0:4], frame.MagicBSV)
	binary.BigEndian.PutUint16(buf[4:6], frame.ProtoVer)
	buf[6] = msgType
	buf[7] = lookupType
	binary.BigEndian.PutUint64(buf[8:16], lookupSeq)
	// buf[16:24] reserved
	return buf
}

func TestNACKSize_is24(t *testing.T) {
	if NACKSize != 24 {
		t.Errorf("NACKSize = %d, want 24", NACKSize)
	}
}

func TestResponseSize_is16(t *testing.T) {
	if ResponseSize != 16 {
		t.Errorf("ResponseSize = %d, want 16", ResponseSize)
	}
}

func TestValidateNACK_valid_byCurSeq(t *testing.T) {
	buf := buildNACK(msgTypeNACK, lookupByCurSeq, 0xDEADBEEFCAFEBABE)
	if err := validateNACK(buf); err != nil {
		t.Fatalf("expected valid NACK, got error: %v", err)
	}
}

func TestValidateNACK_valid_byPrevSeq(t *testing.T) {
	buf := buildNACK(msgTypeNACK, lookupByPrevSeq, 0x0102030405060708)
	if err := validateNACK(buf); err != nil {
		t.Fatalf("expected valid NACK, got error: %v", err)
	}
}

func TestValidateNACK_badMagic(t *testing.T) {
	buf := buildNACK(msgTypeNACK, lookupByCurSeq, 42)
	buf[0] = 0xFF
	if err := validateNACK(buf); err == nil {
		t.Fatal("expected error for bad magic")
	}
}

func TestValidateNACK_badMsgType(t *testing.T) {
	buf := buildNACK(0xFF, lookupByCurSeq, 42)
	if err := validateNACK(buf); err == nil {
		t.Fatal("expected error for invalid msg type")
	}
}

func TestValidateNACK_tooShort(t *testing.T) {
	if err := validateNACK(make([]byte, NACKSize-1)); err == nil {
		t.Fatal("expected error for short datagram")
	}
}

func TestValidateNACK_parsesLookupSeq(t *testing.T) {
	const want uint64 = 0xAABBCCDDEEFF0011
	buf := buildNACK(msgTypeNACK, lookupByCurSeq, want)
	if err := validateNACK(buf); err != nil {
		t.Fatalf("validate: %v", err)
	}
	got := binary.BigEndian.Uint64(buf[8:16])
	if got != want {
		t.Errorf("LookupSeq = 0x%016X, want 0x%016X", got, want)
	}
}
