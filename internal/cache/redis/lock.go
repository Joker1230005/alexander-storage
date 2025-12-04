package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/prn-tf/alexander-storage/internal/repository"
)

// DistributedLock implements repository.DistributedLock using Redis.
type DistributedLock struct {
	client *Client
	// token is used to verify lock ownership for release/extend operations
	tokens map[string]string
}

// NewDistributedLock creates a new Redis distributed lock.
func NewDistributedLock(client *Client) repository.DistributedLock {
	return &DistributedLock{
		client: client,
		tokens: make(map[string]string),
	}
}

// generateToken creates a unique token for lock ownership.
func generateToken() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// Acquire attempts to acquire a lock.
// Returns true if the lock was acquired, false if it's held by another process.
func (l *DistributedLock) Acquire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		ttl = defaultLockTTL
	}

	lockKey := prefixLock + key
	token := generateToken()

	// Try to acquire lock using SETNX
	success, err := l.client.client.SetNX(ctx, lockKey, token, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("failed to acquire lock: %w", err)
	}

	if success {
		// Store token for later release/extend
		l.tokens[key] = token
		l.client.logger.Debug().
			Str("key", key).
			Dur("ttl", ttl).
			Msg("lock acquired")
	}

	return success, nil
}

// AcquireWithRetry attempts to acquire a lock with retries.
func (l *DistributedLock) AcquireWithRetry(ctx context.Context, key string, ttl time.Duration, maxRetries int, retryDelay time.Duration) (bool, error) {
	for i := 0; i <= maxRetries; i++ {
		acquired, err := l.Acquire(ctx, key, ttl)
		if err != nil {
			return false, err
		}
		if acquired {
			return true, nil
		}

		// Don't sleep on the last attempt
		if i < maxRetries {
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			case <-time.After(retryDelay):
				// Continue to next attempt
			}
		}
	}
	return false, nil
}

// Release releases a lock.
// Returns true if the lock was released, false if it wasn't held.
func (l *DistributedLock) Release(ctx context.Context, key string) (bool, error) {
	lockKey := prefixLock + key
	token, exists := l.tokens[key]
	if !exists {
		// We don't have a token, can't verify ownership
		// Just try to delete (unsafe but necessary for interface compliance)
		result, err := l.client.client.Del(ctx, lockKey).Result()
		if err != nil {
			return false, fmt.Errorf("failed to release lock: %w", err)
		}
		return result > 0, nil
	}

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
		return false, fmt.Errorf("failed to release lock: %w", err)
	}

	if result > 0 {
		delete(l.tokens, key)
		l.client.logger.Debug().
			Str("key", key).
			Msg("lock released")
		return true, nil
	}

	return false, nil
}

// Extend extends the TTL of a held lock.
// Returns true if the lock was extended, false if it's not held.
func (l *DistributedLock) Extend(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	lockKey := prefixLock + key
	token, exists := l.tokens[key]
	if !exists {
		return false, nil
	}

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
		return false, fmt.Errorf("failed to extend lock: %w", err)
	}

	if result > 0 {
		l.client.logger.Debug().
			Str("key", key).
			Dur("ttl", ttl).
			Msg("lock extended")
		return true, nil
	}

	return false, nil
}

// IsHeld checks if a lock is held.
func (l *DistributedLock) IsHeld(ctx context.Context, key string) (bool, error) {
	lockKey := prefixLock + key

	result, err := l.client.client.Exists(ctx, lockKey).Result()
	if err != nil {
		return false, fmt.Errorf("failed to check lock: %w", err)
	}

	return result > 0, nil
}

// Ensure DistributedLock implements repository.DistributedLock
var _ repository.DistributedLock = (*DistributedLock)(nil)
