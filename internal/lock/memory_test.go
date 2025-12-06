package lock

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryLocker_Acquire(t *testing.T) {
	locker := NewMemoryLocker()

	ctx := context.Background()
	key := "test-lock"

	// First acquisition should succeed
	acquired, err := locker.Acquire(ctx, key, 5*time.Second)
	require.NoError(t, err)
	assert.True(t, acquired)

	// Second acquisition should fail (lock is held)
	acquired, err = locker.Acquire(ctx, key, 5*time.Second)
	require.NoError(t, err)
	assert.False(t, acquired)
}

func TestMemoryLocker_Release(t *testing.T) {
	locker := NewMemoryLocker()

	ctx := context.Background()
	key := "test-lock"

	// Acquire lock
	acquired, err := locker.Acquire(ctx, key, 5*time.Second)
	require.NoError(t, err)
	assert.True(t, acquired)

	// Release lock
	released, err := locker.Release(ctx, key)
	require.NoError(t, err)
	assert.True(t, released)

	// Should be able to acquire again
	acquired, err = locker.Acquire(ctx, key, 5*time.Second)
	require.NoError(t, err)
	assert.True(t, acquired)
}

func TestMemoryLocker_Expiration(t *testing.T) {
	locker := NewMemoryLocker()

	ctx := context.Background()
	key := "test-lock"

	// Acquire lock with short TTL
	acquired, err := locker.Acquire(ctx, key, 100*time.Millisecond)
	require.NoError(t, err)
	assert.True(t, acquired)

	// Wait for lock to expire
	time.Sleep(150 * time.Millisecond)

	// Should be able to acquire again after expiration
	acquired, err = locker.Acquire(ctx, key, 5*time.Second)
	require.NoError(t, err)
	assert.True(t, acquired)
}

func TestMemoryLocker_AcquireWithRetry(t *testing.T) {
	locker := NewMemoryLocker()

	ctx := context.Background()
	key := "test-lock"

	// Acquire lock with short TTL
	acquired, err := locker.Acquire(ctx, key, 50*time.Millisecond)
	require.NoError(t, err)
	assert.True(t, acquired)

	// Try to acquire with retry - should succeed after expiration
	acquired, err = locker.AcquireWithRetry(ctx, key, 5*time.Second, 5, 30*time.Millisecond)
	require.NoError(t, err)
	assert.True(t, acquired)
}

func TestMemoryLocker_AcquireWithRetry_MaxRetries(t *testing.T) {
	locker := NewMemoryLocker()

	ctx := context.Background()
	key := "test-lock"

	// Acquire lock with long TTL
	acquired, err := locker.Acquire(ctx, key, 1*time.Hour)
	require.NoError(t, err)
	assert.True(t, acquired)

	// Try to acquire with retry - should fail after max retries
	acquired, err = locker.AcquireWithRetry(ctx, key, 5*time.Second, 2, 10*time.Millisecond)
	require.NoError(t, err)
	assert.False(t, acquired)
}

func TestMemoryLocker_Extend(t *testing.T) {
	locker := NewMemoryLocker()

	ctx := context.Background()
	key := "test-lock"

	// Acquire lock
	acquired, err := locker.Acquire(ctx, key, 100*time.Millisecond)
	require.NoError(t, err)
	assert.True(t, acquired)

	// Extend lock
	extended, err := locker.Extend(ctx, key, 5*time.Second)
	require.NoError(t, err)
	assert.True(t, extended)

	// Wait for original expiration time
	time.Sleep(150 * time.Millisecond)

	// Lock should still be held due to extension
	acquired, err = locker.Acquire(ctx, key, 5*time.Second)
	require.NoError(t, err)
	assert.False(t, acquired)
}

func TestMemoryLocker_IsHeld(t *testing.T) {
	locker := NewMemoryLocker()

	ctx := context.Background()
	key := "test-lock"

	// Check before acquisition
	held, err := locker.IsHeld(ctx, key)
	require.NoError(t, err)
	assert.False(t, held)

	// Acquire lock
	acquired, err := locker.Acquire(ctx, key, 5*time.Second)
	require.NoError(t, err)
	assert.True(t, acquired)

	// Check after acquisition
	held, err = locker.IsHeld(ctx, key)
	require.NoError(t, err)
	assert.True(t, held)

	// Release lock
	released, err := locker.Release(ctx, key)
	require.NoError(t, err)
	assert.True(t, released)

	// Check after release
	held, err = locker.IsHeld(ctx, key)
	require.NoError(t, err)
	assert.False(t, held)
}

func TestMemoryLocker_ContextCancellation(t *testing.T) {
	locker := NewMemoryLocker()

	ctx, cancel := context.WithCancel(context.Background())
	key := "test-lock"

	// Cancel context before acquisition
	cancel()

	// Should return error due to cancelled context
	acquired, err := locker.Acquire(ctx, key, 5*time.Second)
	assert.Error(t, err)
	assert.False(t, acquired)
}

func TestMemoryLocker_ConcurrentAccess(t *testing.T) {
	locker := NewMemoryLocker()

	ctx := context.Background()
	key := "test-lock"

	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	// Try to acquire the same lock from multiple goroutines
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			acquired, err := locker.Acquire(ctx, key, 5*time.Second)
			if err == nil && acquired {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	// Only one goroutine should have acquired the lock
	assert.Equal(t, 1, successCount)
}

func TestMemoryLocker_MultipleLocks(t *testing.T) {
	locker := NewMemoryLocker()

	ctx := context.Background()

	// Acquire multiple different locks
	acquired1, err := locker.Acquire(ctx, "lock1", 5*time.Second)
	require.NoError(t, err)
	assert.True(t, acquired1)

	acquired2, err := locker.Acquire(ctx, "lock2", 5*time.Second)
	require.NoError(t, err)
	assert.True(t, acquired2)

	acquired3, err := locker.Acquire(ctx, "lock3", 5*time.Second)
	require.NoError(t, err)
	assert.True(t, acquired3)

	// All locks should be held
	held1, _ := locker.IsHeld(ctx, "lock1")
	held2, _ := locker.IsHeld(ctx, "lock2")
	held3, _ := locker.IsHeld(ctx, "lock3")

	assert.True(t, held1)
	assert.True(t, held2)
	assert.True(t, held3)
}

func TestNoOpLocker(t *testing.T) {
	locker := NewNoOpLocker()

	ctx := context.Background()
	key := "test-lock"

	// All operations should succeed
	acquired, err := locker.Acquire(ctx, key, 5*time.Second)
	require.NoError(t, err)
	assert.True(t, acquired)

	acquired, err = locker.AcquireWithRetry(ctx, key, 5*time.Second, 3, time.Second)
	require.NoError(t, err)
	assert.True(t, acquired)

	released, err := locker.Release(ctx, key)
	require.NoError(t, err)
	assert.True(t, released)

	extended, err := locker.Extend(ctx, key, 5*time.Second)
	require.NoError(t, err)
	assert.True(t, extended)

	held, err := locker.IsHeld(ctx, key)
	require.NoError(t, err)
	assert.False(t, held)
}
