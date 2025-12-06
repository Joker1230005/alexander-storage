// Package filesystem provides a filesystem-based blob storage backend.
package filesystem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/rs/zerolog"

	"github.com/prn-tf/alexander-storage/internal/storage"
)

const (
	// shardCount is the number of lock shards (256 = one per first byte of hash).
	shardCount = 256
)

// shardedLock provides fine-grained locking based on content hash.
// Instead of a global lock, we use 256 independent locks (one per hash prefix).
// This allows concurrent operations on different blobs.
type shardedLock struct {
	locks [shardCount]sync.RWMutex
}

// lockForHash returns the shard index for a given content hash.
func (sl *shardedLock) shardIndex(contentHash string) int {
	if len(contentHash) < 2 {
		return 0
	}
	// Use first byte of hash (2 hex chars) to determine shard
	b, err := hex.DecodeString(contentHash[:2])
	if err != nil || len(b) == 0 {
		return 0
	}
	return int(b[0])
}

// Lock acquires write lock for the given hash shard.
func (sl *shardedLock) Lock(contentHash string) {
	sl.locks[sl.shardIndex(contentHash)].Lock()
}

// Unlock releases write lock for the given hash shard.
func (sl *shardedLock) Unlock(contentHash string) {
	sl.locks[sl.shardIndex(contentHash)].Unlock()
}

// RLock acquires read lock for the given hash shard.
func (sl *shardedLock) RLock(contentHash string) {
	sl.locks[sl.shardIndex(contentHash)].RLock()
}

// RUnlock releases read lock for the given hash shard.
func (sl *shardedLock) RUnlock(contentHash string) {
	sl.locks[sl.shardIndex(contentHash)].RUnlock()
}

// LockAll acquires write locks on all shards (for global operations).
func (sl *shardedLock) LockAll() {
	for i := range sl.locks {
		sl.locks[i].Lock()
	}
}

// UnlockAll releases write locks on all shards.
func (sl *shardedLock) UnlockAll() {
	for i := range sl.locks {
		sl.locks[i].Unlock()
	}
}

// RLockAll acquires read locks on all shards (for global reads).
func (sl *shardedLock) RLockAll() {
	for i := range sl.locks {
		sl.locks[i].RLock()
	}
}

// RUnlockAll releases read locks on all shards.
func (sl *shardedLock) RUnlockAll() {
	for i := range sl.locks {
		sl.locks[i].RUnlock()
	}
}

// Storage implements storage.Backend using the local filesystem.
// Uses sharded locking for high-concurrency blob operations.
type Storage struct {
	dataDir    string
	tempDir    string
	pathConfig storage.PathConfig
	logger     zerolog.Logger
	shards     shardedLock
	tempMu     sync.Mutex // Only for temp file creation
}

// Config holds configuration for the filesystem storage.
type Config struct {
	DataDir string
	TempDir string
}

// NewStorage creates a new filesystem storage backend.
func NewStorage(cfg Config, logger zerolog.Logger) (*Storage, error) {
	// Ensure directories exist
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}
	if err := os.MkdirAll(cfg.TempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Convert to absolute paths
	dataDir, err := filepath.Abs(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for data dir: %w", err)
	}
	tempDir, err := filepath.Abs(cfg.TempDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for temp dir: %w", err)
	}

	logger.Info().
		Str("data_dir", dataDir).
		Str("temp_dir", tempDir).
		Msg("filesystem storage initialized")

	return &Storage{
		dataDir:    dataDir,
		tempDir:    tempDir,
		pathConfig: storage.DefaultPathConfig(dataDir),
		logger:     logger,
	}, nil
}

// Store stores content from the reader and returns the content hash.
// The content is first written to a temp file, then moved to its final location.
// Uses per-hash sharded locking to allow concurrent uploads of different blobs.
func (s *Storage) Store(ctx context.Context, reader io.Reader, size int64) (string, error) {
	// Phase 1: Write to temp file without holding any hash lock
	// Only use temp mutex briefly to create temp file
	s.tempMu.Lock()
	tempFile, err := os.CreateTemp(s.tempDir, "upload-*")
	s.tempMu.Unlock()
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tempPath := tempFile.Name()

	// Ensure cleanup on error
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tempPath)
		}
	}()

	// Create streaming hasher
	hasher := sha256.New()

	// Wrap reader to compute hash while copying
	teeReader := io.TeeReader(reader, hasher)

	// Copy content to temp file (no lock needed - temp file is unique)
	written, err := io.Copy(tempFile, teeReader)
	if err != nil {
		_ = tempFile.Close()
		return "", fmt.Errorf("failed to write to temp file: %w", err)
	}

	if err := tempFile.Close(); err != nil {
		return "", fmt.Errorf("failed to close temp file: %w", err)
	}

	// Verify size if provided
	if size > 0 && written != size {
		return "", fmt.Errorf("size mismatch: expected %d, got %d", size, written)
	}

	// Get the content hash
	contentHash := hex.EncodeToString(hasher.Sum(nil))

	// Phase 2: Now that we know the hash, acquire the specific shard lock
	s.shards.Lock(contentHash)
	defer s.shards.Unlock(contentHash)

	// Generate storage path based on hash
	fullPath := storage.ComputePath(s.pathConfig, contentHash)

	// Check if blob already exists (deduplication)
	if _, err := os.Stat(fullPath); err == nil {
		// Blob already exists, just remove temp file
		_ = os.Remove(tempPath)
		s.logger.Debug().
			Str("content_hash", contentHash).
			Msg("blob already exists, skipping storage")
		success = true
		return contentHash, nil
	}

	// Create target directory
	targetDir := filepath.Dir(fullPath)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create target directory: %w", err)
	}

	// Move temp file to final location
	if err := os.Rename(tempPath, fullPath); err != nil {
		// If rename fails (cross-device), fall back to copy
		if err := copyFile(tempPath, fullPath); err != nil {
			return "", fmt.Errorf("failed to move file to storage: %w", err)
		}
		_ = os.Remove(tempPath)
	}

	s.logger.Debug().
		Str("content_hash", contentHash).
		Str("storage_path", fullPath).
		Int64("size", written).
		Msg("blob stored successfully")

	success = true
	return contentHash, nil
}

// Retrieve returns a reader for the blob with the given content hash.
// Uses sharded read lock for the specific hash to allow concurrent reads.
func (s *Storage) Retrieve(ctx context.Context, contentHash string) (io.ReadCloser, error) {
	s.shards.RLock(contentHash)
	defer s.shards.RUnlock(contentHash)

	fullPath := storage.ComputePath(s.pathConfig, contentHash)

	file, err := os.Open(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, storage.ErrBlobNotFound
		}
		return nil, fmt.Errorf("failed to open blob: %w", err)
	}

	return file, nil
}

// RetrieveRange returns a reader for a range of bytes from the blob.
// Uses sharded read lock for the specific hash.
func (s *Storage) RetrieveRange(ctx context.Context, contentHash string, offset, length int64) (io.ReadCloser, error) {
	s.shards.RLock(contentHash)
	defer s.shards.RUnlock(contentHash)

	fullPath := storage.ComputePath(s.pathConfig, contentHash)

	file, err := os.Open(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, storage.ErrBlobNotFound
		}
		return nil, fmt.Errorf("failed to open blob: %w", err)
	}

	// Seek to offset
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("failed to seek to offset: %w", err)
	}

	// Return a limited reader if length is specified
	if length > 0 {
		return &limitedReadCloser{
			reader: io.LimitReader(file, length),
			closer: file,
		}, nil
	}

	return file, nil
}

// Delete removes a blob from storage.
// Uses sharded write lock for the specific hash.
func (s *Storage) Delete(ctx context.Context, contentHash string) error {
	s.shards.Lock(contentHash)
	defer s.shards.Unlock(contentHash)

	fullPath := storage.ComputePath(s.pathConfig, contentHash)

	if err := os.Remove(fullPath); err != nil {
		if os.IsNotExist(err) {
			return storage.ErrBlobNotFound
		}
		return fmt.Errorf("failed to delete blob: %w", err)
	}

	// Try to remove empty parent directories
	s.cleanupEmptyDirs(filepath.Dir(fullPath))

	s.logger.Debug().
		Str("content_hash", contentHash).
		Msg("blob deleted successfully")

	return nil
}

// Exists checks if a blob exists in storage.
// Uses sharded read lock for the specific hash.
func (s *Storage) Exists(ctx context.Context, contentHash string) (bool, error) {
	s.shards.RLock(contentHash)
	defer s.shards.RUnlock(contentHash)

	fullPath := storage.ComputePath(s.pathConfig, contentHash)

	_, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check blob existence: %w", err)
	}

	return true, nil
}

// GetSize returns the size of a blob in bytes.
// Uses sharded read lock for the specific hash.
func (s *Storage) GetSize(ctx context.Context, contentHash string) (int64, error) {
	s.shards.RLock(contentHash)
	defer s.shards.RUnlock(contentHash)

	fullPath := storage.ComputePath(s.pathConfig, contentHash)

	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, storage.ErrBlobNotFound
		}
		return 0, fmt.Errorf("failed to get blob size: %w", err)
	}

	return info.Size(), nil
}

// GetPath returns the storage path for a blob (for database records).
func (s *Storage) GetPath(contentHash string) string {
	return storage.ComputePath(s.pathConfig, contentHash)
}

// GetDataDir returns the data directory path.
func (s *Storage) GetDataDir() string {
	return s.dataDir
}

// GetTempDir returns the temp directory path.
func (s *Storage) GetTempDir() string {
	return s.tempDir
}

// cleanupEmptyDirs removes empty parent directories up to the data directory.
func (s *Storage) cleanupEmptyDirs(dir string) {
	for dir != s.dataDir && dir != "" {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			break
		}
		if err := os.Remove(dir); err != nil {
			break
		}
		dir = filepath.Dir(dir)
	}
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

// limitedReadCloser wraps a limited reader with a closer.
type limitedReadCloser struct {
	reader io.Reader
	closer io.Closer
}

func (l *limitedReadCloser) Read(p []byte) (int, error) {
	return l.reader.Read(p)
}

func (l *limitedReadCloser) Close() error {
	return l.closer.Close()
}

// HealthCheck verifies the storage backend is accessible.
// Does not require hash locks as it only checks directory accessibility.
func (s *Storage) HealthCheck(ctx context.Context) error {
	// Check data directory is accessible
	if _, err := os.Stat(s.dataDir); err != nil {
		return fmt.Errorf("data directory not accessible: %w", err)
	}

	// Check temp directory is accessible
	if _, err := os.Stat(s.tempDir); err != nil {
		return fmt.Errorf("temp directory not accessible: %w", err)
	}

	// Try to create and remove a test file
	testPath := filepath.Join(s.tempDir, ".health-check")
	if err := os.WriteFile(testPath, []byte("ok"), 0644); err != nil {
		return fmt.Errorf("failed to write test file: %w", err)
	}
	if err := os.Remove(testPath); err != nil {
		return fmt.Errorf("failed to remove test file: %w", err)
	}

	return nil
}

// Ensure Storage implements storage.Backend
var _ storage.Backend = (*Storage)(nil)
