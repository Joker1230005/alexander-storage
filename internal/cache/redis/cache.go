// Package redis provides Redis-based caching and distributed locking.
package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/prn-tf/alexander-storage/internal/config"
	"github.com/prn-tf/alexander-storage/internal/repository"
)

// Client wraps Redis client with additional functionality.
type Client struct {
	client *redis.Client
	logger zerolog.Logger
}

// NewClient creates a new Redis client.
func NewClient(ctx context.Context, cfg config.RedisConfig, logger zerolog.Logger) (*Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:        cfg.Addr(),
		Password:    cfg.Password,
		DB:          cfg.DB,
		PoolSize:    cfg.PoolSize,
		DialTimeout: cfg.DialTimeout,
	})

	// Verify connection
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	logger.Info().
		Str("addr", cfg.Addr()).
		Int("db", cfg.DB).
		Msg("connected to Redis")

	return &Client{
		client: client,
		logger: logger,
	}, nil
}

// Close closes the Redis connection.
func (c *Client) Close() error {
	c.logger.Info().Msg("closing Redis connection")
	return c.client.Close()
}

// Health checks the Redis connection health.
func (c *Client) Health(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

// Cache prefix constants
const (
	prefixAccessKey = "access_key:"
	prefixBucket    = "bucket:"
	prefixObject    = "object:"
	prefixUser      = "user:"
	prefixLock      = "lock:"
)

// Default TTLs
const (
	defaultCacheTTL = 5 * time.Minute
	defaultLockTTL  = 30 * time.Second
)

// Cache implements repository.Cache using Redis.
type Cache struct {
	client *Client
	ttl    time.Duration
}

// NewCache creates a new Redis cache.
func NewCache(client *Client, ttl time.Duration) repository.Cache {
	if ttl <= 0 {
		ttl = defaultCacheTTL
	}
	return &Cache{
		client: client,
		ttl:    ttl,
	}
}

// Get retrieves a value from the cache.
func (c *Cache) Get(ctx context.Context, key string) ([]byte, error) {
	val, err := c.client.client.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, repository.ErrCacheMiss
		}
		return nil, fmt.Errorf("failed to get from cache: %w", err)
	}
	return val, nil
}

// Set stores a value in the cache.
func (c *Cache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = c.ttl
	}
	if err := c.client.client.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("failed to set in cache: %w", err)
	}
	return nil
}

// SetNX sets a value only if the key doesn't exist.
func (c *Cache) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		ttl = c.ttl
	}
	result, err := c.client.client.SetNX(ctx, key, value, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("failed to setnx in cache: %w", err)
	}
	return result, nil
}

// Delete removes a value from the cache.
func (c *Cache) Delete(ctx context.Context, key string) error {
	if err := c.client.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("failed to delete from cache: %w", err)
	}
	return nil
}

// Exists checks if a key exists.
func (c *Cache) Exists(ctx context.Context, key string) (bool, error) {
	result, err := c.client.client.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("failed to check key existence: %w", err)
	}
	return result > 0, nil
}

// Expire sets or updates the TTL for a key.
func (c *Cache) Expire(ctx context.Context, key string, ttl time.Duration) error {
	if err := c.client.client.Expire(ctx, key, ttl).Err(); err != nil {
		return fmt.Errorf("failed to set expiration: %w", err)
	}
	return nil
}

// TTL returns the remaining TTL for a key.
func (c *Cache) TTL(ctx context.Context, key string) (time.Duration, error) {
	ttl, err := c.client.client.TTL(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("failed to get TTL: %w", err)
	}
	// Redis returns -2 if key doesn't exist, -1 if no TTL set
	return ttl, nil
}

// GetMulti retrieves multiple values by keys.
func (c *Cache) GetMulti(ctx context.Context, keys []string) (map[string][]byte, error) {
	if len(keys) == 0 {
		return make(map[string][]byte), nil
	}

	vals, err := c.client.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get multiple keys: %w", err)
	}

	result := make(map[string][]byte, len(keys))
	for i, val := range vals {
		if val != nil {
			switch v := val.(type) {
			case string:
				result[keys[i]] = []byte(v)
			case []byte:
				result[keys[i]] = v
			}
		}
	}
	return result, nil
}

// SetMulti stores multiple values.
func (c *Cache) SetMulti(ctx context.Context, items map[string][]byte, ttl time.Duration) error {
	if len(items) == 0 {
		return nil
	}
	if ttl <= 0 {
		ttl = c.ttl
	}

	pipe := c.client.client.Pipeline()
	for key, value := range items {
		pipe.Set(ctx, key, value, ttl)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to set multiple keys: %w", err)
	}
	return nil
}

// DeleteMulti removes multiple values.
func (c *Cache) DeleteMulti(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	if err := c.client.client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("failed to delete multiple keys: %w", err)
	}
	return nil
}

// Increment atomically increments an integer value.
func (c *Cache) Increment(ctx context.Context, key string, delta int64) (int64, error) {
	result, err := c.client.client.IncrBy(ctx, key, delta).Result()
	if err != nil {
		return 0, fmt.Errorf("failed to increment key: %w", err)
	}
	return result, nil
}

// Decrement atomically decrements an integer value.
func (c *Cache) Decrement(ctx context.Context, key string, delta int64) (int64, error) {
	result, err := c.client.client.DecrBy(ctx, key, delta).Result()
	if err != nil {
		return 0, fmt.Errorf("failed to decrement key: %w", err)
	}
	return result, nil
}

// DeletePattern removes values matching a pattern from the cache.
func (c *Cache) DeletePattern(ctx context.Context, pattern string) error {
	iter := c.client.client.Scan(ctx, 0, pattern, 0).Iterator()
	for iter.Next(ctx) {
		if err := c.client.client.Del(ctx, iter.Val()).Err(); err != nil {
			c.client.logger.Warn().Err(err).Str("key", iter.Val()).Msg("failed to delete key")
		}
	}
	if err := iter.Err(); err != nil {
		return fmt.Errorf("failed to scan cache: %w", err)
	}
	return nil
}

// GetJSON retrieves and unmarshals a JSON value from the cache.
func (c *Cache) GetJSON(ctx context.Context, key string, dest interface{}) error {
	data, err := c.Get(ctx, key)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, dest); err != nil {
		return fmt.Errorf("failed to unmarshal cached value: %w", err)
	}
	return nil
}

// SetJSON marshals and stores a JSON value in the cache.
func (c *Cache) SetJSON(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal value: %w", err)
	}
	return c.Set(ctx, key, data, ttl)
}

// AccessKeyKey returns the cache key for an access key.
func AccessKeyKey(accessKeyID string) string {
	return prefixAccessKey + accessKeyID
}

// BucketKey returns the cache key for a bucket.
func BucketKey(bucketName string) string {
	return prefixBucket + bucketName
}

// UserKey returns the cache key for a user.
func UserKey(userID string) string {
	return prefixUser + userID
}

// Ensure Cache implements repository.Cache
var _ repository.Cache = (*Cache)(nil)
