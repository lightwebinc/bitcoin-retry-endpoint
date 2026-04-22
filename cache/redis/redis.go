// Package redis provides a Redis cache backend for frame storage.
package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Cache is a Redis-backed cache implementation.
type Cache struct {
	client *redis.Client
	prefix string
}

// New constructs a Redis cache backend.
// addr is the Redis server address (e.g., "localhost:6379").
// prefix is the key prefix used for all keys (e.g., "bre:frame:").
func New(addr, prefix string) (*Cache, error) {
	client := redis.NewClient(&redis.Options{
		Addr: addr,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	return &Cache{
		client: client,
		prefix: prefix,
	}, nil
}

// Store stores a frame value under the given key with the specified TTL.
func (c *Cache) Store(key []byte, value []byte, ttl time.Duration) error {
	ctx := context.Background()
	redisKey := c.prefix + string(key)
	return c.client.Set(ctx, redisKey, value, ttl).Err()
}

// Retrieve retrieves the frame value for the given key.
// Returns nil if the key does not exist or has expired.
func (c *Cache) Retrieve(key []byte) ([]byte, error) {
	ctx := context.Background()
	redisKey := c.prefix + string(key)
	val, err := c.client.Get(ctx, redisKey).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return val, nil
}

// Delete removes the key from the cache.
func (c *Cache) Delete(key []byte) error {
	ctx := context.Background()
	redisKey := c.prefix + string(key)
	return c.client.Del(ctx, redisKey).Err()
}

// Close releases the Redis client connection.
func (c *Cache) Close() error {
	return c.client.Close()
}

// SetNX sets the key only if it does not exist (atomic deduplication).
// Returns true if the key was set, false if it already existed.
func (c *Cache) SetNX(key []byte, value []byte, ttl time.Duration) (bool, error) {
	ctx := context.Background()
	redisKey := c.prefix + string(key)
	return c.client.SetNX(ctx, redisKey, value, ttl).Result()
}
