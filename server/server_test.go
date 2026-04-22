package server

import (
	"encoding/binary"
	"testing"

	"github.com/lightwebinc/bitcoin-shard-common/frame"
)

func buildNACK(msgType byte, txID [32]byte, shardSeqNum uint64, senderID [16]byte, sequenceID uint64) []byte {
	buf := make([]byte, NACKSize)
	binary.BigEndian.PutUint32(buf[0:4], frame.MagicBSV)
	binary.BigEndian.PutUint16(buf[4:6], frame.ProtoVer)
	buf[6] = msgType
	buf[7] = 0
	copy(buf[8:40], txID[:])
	binary.BigEndian.PutUint64(buf[40:48], shardSeqNum)
	copy(buf[48:64], senderID[:])
	binary.BigEndian.PutUint64(buf[64:72], sequenceID)
	return buf
}

func TestValidateNACK_valid(t *testing.T) {
	var txID [32]byte
	buf := buildNACK(0x10, txID, 1, [16]byte{}, 2)
	if err := validateNACK(buf); err != nil {
		t.Fatalf("expected valid NACK, got error: %v", err)
	}
}

func TestValidateNACK_badMagic(t *testing.T) {
	var txID [32]byte
	buf := buildNACK(0x10, txID, 1, [16]byte{}, 2)
	buf[0] = 0xFF
	if err := validateNACK(buf); err == nil {
		t.Fatal("expected error for bad magic")
	}
}

func TestValidateNACK_badMsgType(t *testing.T) {
	var txID [32]byte
	buf := buildNACK(0xFF, txID, 1, [16]byte{}, 2)
	if err := validateNACK(buf); err == nil {
		t.Fatal("expected error for invalid msg type")
	}
}

func TestValidateNACK_tooShort(t *testing.T) {
	if err := validateNACK(make([]byte, 10)); err == nil {
		t.Fatal("expected error for short datagram")
	}
}

func TestExtractFields(t *testing.T) {
	var txID [32]byte
	txID[0] = 0xAB

	var senderID [16]byte
	senderID[0] = 0xCD

	const shardSeqNum = uint64(12345)
	const sequenceID = uint64(67890)

	buf := buildNACK(0x10, txID, shardSeqNum, senderID, sequenceID)

	if got := extractTxID(buf); got != txID {
		t.Errorf("TxID mismatch: got %x, want %x", got, txID)
	}
	if got := extractShardSeqNum(buf); got != shardSeqNum {
		t.Errorf("ShardSeqNum mismatch: got %d, want %d", got, shardSeqNum)
	}
	if got := extractSenderID(buf); got != senderID {
		t.Errorf("SenderID mismatch: got %x, want %x", got, senderID)
	}
	if got := extractSequenceID(buf); got != sequenceID {
		t.Errorf("SequenceID mismatch: got %d, want %d", got, sequenceID)
	}
}
