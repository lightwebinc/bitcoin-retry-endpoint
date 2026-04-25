// Package cache provides a modular cache backend for storing
// multicast BSV transaction frames with configurable TTL.
package cache

import "time"

// Cache defines the interface for frame storage backends.
type Cache interface {
	// Store stores a frame value under the given key with the specified TTL.
	// key is a 32-byte composite: SenderID (16B) + SequenceID (8B) + SeqNum (8B).
	// value is the raw frame bytes (108-byte v2 header + payload).
	Store(key []byte, value []byte, ttl time.Duration) error

	// Retrieve retrieves the frame value for the given key.
	// Returns nil if the key does not exist or has expired.
	Retrieve(key []byte) ([]byte, error)

	// Delete removes the key from the cache.
	Delete(key []byte) error

	// Close releases any resources held by the cache backend.
	Close() error
}
