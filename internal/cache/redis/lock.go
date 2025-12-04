package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/prn-tf/alexander-storage/internal/repository"
)

// DistributedLock implements repository.DistributedLock using Redis.
type DistributedLock struct {
	client *Client
}

// NewDistributedLock creates a new Redis distributed lock.
func NewDistributedLock(client *Client) repository.DistributedLock {
	return &DistributedLock{client: client}
}

// Lock acquires a distributed lock with the given key.
// Returns a token that must be used to unlock.
func (l *DistributedLock) Lock(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = defaultLockTTL
	}

	lockKey := prefixLock + key
	token := uuid.New().String()

	// Try to acquire lock using SETNX
	success, err := l.client.client.SetNX(ctx, lockKey, token, ttl).Result()
	if err != nil {
		return "", fmt.Errorf("failed to acquire lock: %w", err)
	}

	if !success {
		return "", repository.ErrLockNotAcquired
	}

	l.client.logger.Debug().
		Str("key", key).
		Str("token", token).
		Dur("ttl", ttl).
		Msg("lock acquired")

	return token, nil
}

// Unlock releases a distributed lock.
// The token must match the one returned by Lock.
func (l *DistributedLock) Unlock(ctx context.Context, key, token string) error {
	lockKey := prefixLock + key

	// Use Lua script to ensure we only delete if we own the lock
	script := `
		if redis.call("GET", KEYS[1]) == ARGV[1] then
			return redis.call("DEL", KEYS[1])
		else
			return 0
		end
	`

	result, err := l.client.client.Eval(ctx, script, []string{lockKey}, token).Int64()
	if err != nil {
		return fmt.Errorf("failed to release lock: %w", err)
	}

	if result == 0 {
		return repository.ErrLockNotOwned
	}

	l.client.logger.Debug().
		Str("key", key).
		Msg("lock released")

	return nil
}

// Extend extends the TTL of a lock.
// The token must match the one returned by Lock.
func (l *DistributedLock) Extend(ctx context.Context, key, token string, ttl time.Duration) error {
	lockKey := prefixLock + key

	// Use Lua script to extend only if we own the lock
	script := `
		if redis.call("GET", KEYS[1]) == ARGV[1] then
			return redis.call("PEXPIRE", KEYS[1], ARGV[2])
		else
			return 0
		end
	`

	result, err := l.client.client.Eval(ctx, script, []string{lockKey}, token, ttl.Milliseconds()).Int64()
	if err != nil {
		return fmt.Errorf("failed to extend lock: %w", err)
	}

	if result == 0 {
		return repository.ErrLockNotOwned
	}

	l.client.logger.Debug().
		Str("key", key).
		Dur("ttl", ttl).
		Msg("lock extended")

	return nil
}

// IsLocked checks if a lock is held.
func (l *DistributedLock) IsLocked(ctx context.Context, key string) (bool, error) {
	lockKey := prefixLock + key

	result, err := l.client.client.Exists(ctx, lockKey).Result()
	if err != nil {
		return false, fmt.Errorf("failed to check lock: %w", err)
	}

	return result > 0, nil
}

// Ensure DistributedLock implements repository.DistributedLock
var _ repository.DistributedLock = (*DistributedLock)(nil)
