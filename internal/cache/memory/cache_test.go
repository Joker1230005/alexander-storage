package memory

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/prn-tf/alexander-storage/internal/repository"
)

func TestCache_SetAndGet(t *testing.T) {
	cache := NewCache()
	defer cache.Stop()

	ctx := context.Background()
	key := "test-key"
	value := []byte("test-value")

	// Set value
	err := cache.Set(ctx, key, value, time.Minute)
	require.NoError(t, err)

	// Get value
	result, err := cache.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, value, result)
}

func TestCache_GetMiss(t *testing.T) {
	cache := NewCache()
	defer cache.Stop()

	ctx := context.Background()

	// Get non-existent key
	_, err := cache.Get(ctx, "non-existent")
	assert.ErrorIs(t, err, repository.ErrCacheMiss)
}

func TestCache_Expiration(t *testing.T) {
	cache := NewCache()
	defer cache.Stop()

	ctx := context.Background()
	key := "test-key"
	value := []byte("test-value")

	// Set value with short TTL
	err := cache.Set(ctx, key, value, 50*time.Millisecond)
	require.NoError(t, err)

	// Should exist immediately
	_, err = cache.Get(ctx, key)
	require.NoError(t, err)

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Should be expired
	_, err = cache.Get(ctx, key)
	assert.ErrorIs(t, err, repository.ErrCacheMiss)
}

func TestCache_Delete(t *testing.T) {
	cache := NewCache()
	defer cache.Stop()

	ctx := context.Background()
	key := "test-key"
	value := []byte("test-value")

	// Set value
	err := cache.Set(ctx, key, value, time.Minute)
	require.NoError(t, err)

	// Delete value
	err = cache.Delete(ctx, key)
	require.NoError(t, err)

	// Should not exist
	_, err = cache.Get(ctx, key)
	assert.ErrorIs(t, err, repository.ErrCacheMiss)
}

func TestCache_DeleteNonExistent(t *testing.T) {
	cache := NewCache()
	defer cache.Stop()

	ctx := context.Background()

	// Delete non-existent key should not error
	err := cache.Delete(ctx, "non-existent")
	require.NoError(t, err)
}

func TestCache_Exists(t *testing.T) {
	cache := NewCache()
	defer cache.Stop()

	ctx := context.Background()
	key := "test-key"
	value := []byte("test-value")

	// Check non-existent key
	exists, err := cache.Exists(ctx, key)
	require.NoError(t, err)
	assert.False(t, exists)

	// Set value
	err = cache.Set(ctx, key, value, time.Minute)
	require.NoError(t, err)

	// Check existing key
	exists, err = cache.Exists(ctx, key)
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestCache_ExistsExpired(t *testing.T) {
	cache := NewCache()
	defer cache.Stop()

	ctx := context.Background()
	key := "test-key"
	value := []byte("test-value")

	// Set value with short TTL
	err := cache.Set(ctx, key, value, 50*time.Millisecond)
	require.NoError(t, err)

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Should not exist after expiration
	exists, err := cache.Exists(ctx, key)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestCache_Overwrite(t *testing.T) {
	cache := NewCache()
	defer cache.Stop()

	ctx := context.Background()
	key := "test-key"
	value1 := []byte("value1")
	value2 := []byte("value2")

	// Set first value
	err := cache.Set(ctx, key, value1, time.Minute)
	require.NoError(t, err)

	// Overwrite with second value
	err = cache.Set(ctx, key, value2, time.Minute)
	require.NoError(t, err)

	// Should get second value
	result, err := cache.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, value2, result)
}

func TestCache_ValueImmutability(t *testing.T) {
	cache := NewCache()
	defer cache.Stop()

	ctx := context.Background()
	key := "test-key"
	value := []byte("test-value")

	// Set value
	err := cache.Set(ctx, key, value, time.Minute)
	require.NoError(t, err)

	// Modify original value
	value[0] = 'X'

	// Cached value should be unchanged
	result, err := cache.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, []byte("test-value"), result)

	// Modify returned value
	result[0] = 'Y'

	// Cached value should still be unchanged
	result2, err := cache.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, []byte("test-value"), result2)
}

func TestCache_MultipleKeys(t *testing.T) {
	cache := NewCache()
	defer cache.Stop()

	ctx := context.Background()

	// Set multiple values
	for i := 0; i < 100; i++ {
		key := string(rune('a' + i%26))
		value := []byte{byte(i)}
		err := cache.Set(ctx, key, value, time.Minute)
		require.NoError(t, err)
	}

	// Get first few
	for i := 0; i < 10; i++ {
		key := string(rune('a' + i))
		result, err := cache.Get(ctx, key)
		require.NoError(t, err)
		// Value should be the last one set for this key
		assert.NotEmpty(t, result)
	}
}

func TestCache_NoExpiry(t *testing.T) {
	cache := NewCache()
	defer cache.Stop()

	ctx := context.Background()
	key := "test-key"
	value := []byte("test-value")

	// Set value with zero TTL (no expiry)
	err := cache.Set(ctx, key, value, 0)
	require.NoError(t, err)

	// Should exist after some time
	time.Sleep(100 * time.Millisecond)

	result, err := cache.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, value, result)
}

func TestCache_Stop(t *testing.T) {
	cache := NewCache()

	ctx := context.Background()

	// Set a value
	err := cache.Set(ctx, "key", []byte("value"), time.Minute)
	require.NoError(t, err)

	// Stop the cache
	cache.Stop()

	// Multiple stops should be safe
	cache.Stop()
}

func TestCache_ImplementsInterface(t *testing.T) {
	// Compile-time check that Cache implements repository.Cache
	var _ repository.Cache = (*Cache)(nil)
}
