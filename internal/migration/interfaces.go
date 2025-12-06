// Package migration provides data migration utilities for Alexander Storage.
// It supports background migration and lazy fallback for schema/format upgrades.
package migration

import (
	"context"
	"time"

	"github.com/prn-tf/alexander-storage/internal/domain"
)

// MigrationType identifies different migration types.
type MigrationType string

const (
	// MigrationEncryption migrates blobs from unencrypted to encrypted.
	MigrationEncryption MigrationType = "encryption"

	// MigrationEncryptionScheme migrates between encryption schemes.
	MigrationEncryptionScheme MigrationType = "encryption_scheme"

	// MigrationComposite migrates concatenated multipart to composite blobs.
	MigrationComposite MigrationType = "composite"

	// MigrationDelta migrates versioned blobs to delta format.
	MigrationDelta MigrationType = "delta"

	// MigrationCDC migrates blobs to CDC-chunked format for dedup.
	MigrationCDC MigrationType = "cdc_chunking"
)

// Status represents the migration status of a blob.
type Status string

const (
	// StatusPending indicates migration has not started.
	StatusPending Status = "pending"

	// StatusInProgress indicates migration is in progress.
	StatusInProgress Status = "in_progress"

	// StatusCompleted indicates migration completed successfully.
	StatusCompleted Status = "completed"

	// StatusFailed indicates migration failed.
	StatusFailed Status = "failed"

	// StatusSkipped indicates migration was skipped (e.g., already migrated).
	StatusSkipped Status = "skipped"
)

// Progress tracks the migration progress of a specific blob.
type Progress struct {
	// MigrationType is the type of migration.
	MigrationType MigrationType `json:"migration_type"`

	// ContentHash is the blob identifier.
	ContentHash string `json:"content_hash"`

	// Status is the current migration status.
	Status Status `json:"status"`

	// StartedAt is when migration started (nil if not started).
	StartedAt *time.Time `json:"started_at,omitempty"`

	// CompletedAt is when migration completed (nil if not completed).
	CompletedAt *time.Time `json:"completed_at,omitempty"`

	// ErrorMessage contains error details if failed.
	ErrorMessage *string `json:"error_message,omitempty"`

	// RetryCount is the number of retry attempts.
	RetryCount int `json:"retry_count"`
}

// Strategy defines how to migrate blobs of a specific type.
type Strategy interface {
	// Type returns the migration type this strategy handles.
	Type() MigrationType

	// ShouldMigrate checks if a blob needs migration.
	ShouldMigrate(ctx context.Context, blob *domain.Blob) (bool, error)

	// Migrate performs the migration for a single blob.
	// Returns the updated blob (may be same blob if no changes to metadata).
	Migrate(ctx context.Context, blob *domain.Blob) (*domain.Blob, error)

	// Validate verifies a migrated blob is correct.
	Validate(ctx context.Context, blob *domain.Blob) error
}

// Worker performs background migration of blobs.
type Worker interface {
	// Start starts the background migration worker.
	Start(ctx context.Context) error

	// Stop stops the background migration worker.
	Stop() error

	// RunOnce performs a single migration batch.
	RunOnce(ctx context.Context) (*BatchResult, error)

	// GetStatus returns the current migration status.
	GetStatus(ctx context.Context) (*WorkerStatus, error)

	// RegisterStrategy registers a migration strategy.
	RegisterStrategy(strategy Strategy)

	// SetBatchSize sets the number of blobs to process per batch.
	SetBatchSize(size int)

	// SetInterval sets the interval between batches.
	SetInterval(interval time.Duration)
}

// BatchResult contains the results of a migration batch.
type BatchResult struct {
	// MigrationType is the type of migration performed.
	MigrationType MigrationType `json:"migration_type"`

	// StartTime is when the batch started.
	StartTime time.Time `json:"start_time"`

	// EndTime is when the batch completed.
	EndTime time.Time `json:"end_time"`

	// Duration is how long the batch took.
	Duration time.Duration `json:"duration"`

	// BlobsProcessed is the number of blobs processed.
	BlobsProcessed int `json:"blobs_processed"`

	// BlobsMigrated is the number of blobs successfully migrated.
	BlobsMigrated int `json:"blobs_migrated"`

	// BlobsSkipped is the number of blobs skipped (already migrated).
	BlobsSkipped int `json:"blobs_skipped"`

	// BlobsFailed is the number of blobs that failed migration.
	BlobsFailed int `json:"blobs_failed"`

	// BytesProcessed is the total bytes processed.
	BytesProcessed int64 `json:"bytes_processed"`

	// Errors contains any errors encountered.
	Errors []string `json:"errors,omitempty"`
}

// WorkerStatus contains the status of the migration worker.
type WorkerStatus struct {
	// Running indicates if the worker is running.
	Running bool `json:"running"`

	// CurrentMigrationType is the type currently being processed.
	CurrentMigrationType *MigrationType `json:"current_migration_type,omitempty"`

	// LastBatchResult is the result of the last batch.
	LastBatchResult *BatchResult `json:"last_batch_result,omitempty"`

	// TotalMigrated is the total blobs migrated across all runs.
	TotalMigrated int64 `json:"total_migrated"`

	// TotalFailed is the total blobs failed across all runs.
	TotalFailed int64 `json:"total_failed"`

	// PendingCounts is the count of pending migrations by type.
	PendingCounts map[MigrationType]int64 `json:"pending_counts,omitempty"`
}

// LazyMigrator provides on-access migration (fallback for unmigrated blobs).
type LazyMigrator interface {
	// MigrateOnAccess migrates a blob when it's accessed.
	// Returns the blob data reader (migrated if necessary).
	MigrateOnAccess(ctx context.Context, blob *domain.Blob) (*domain.Blob, error)

	// RegisterStrategy registers a migration strategy for lazy migration.
	RegisterStrategy(strategy Strategy)
}

// Tracker tracks migration progress in the database.
type Tracker interface {
	// GetProgress gets the migration progress for a blob.
	GetProgress(ctx context.Context, migrationType MigrationType, contentHash string) (*Progress, error)

	// SetProgress sets the migration progress for a blob.
	SetProgress(ctx context.Context, progress *Progress) error

	// ListPending lists blobs pending migration of a specific type.
	ListPending(ctx context.Context, migrationType MigrationType, limit int) ([]*domain.Blob, error)

	// ListFailed lists blobs that failed migration.
	ListFailed(ctx context.Context, migrationType MigrationType, limit int) ([]*Progress, error)

	// MarkCompleted marks a blob as completed for a migration type.
	MarkCompleted(ctx context.Context, migrationType MigrationType, contentHash string) error

	// MarkFailed marks a blob as failed for a migration type.
	MarkFailed(ctx context.Context, migrationType MigrationType, contentHash string, err error) error

	// GetStats returns migration statistics.
	GetStats(ctx context.Context, migrationType MigrationType) (*MigrationStats, error)
}

// MigrationStats contains statistics about a migration type.
type MigrationStats struct {
	// MigrationType is the type of migration.
	MigrationType MigrationType `json:"migration_type"`

	// TotalBlobs is the total number of blobs.
	TotalBlobs int64 `json:"total_blobs"`

	// PendingBlobs is the number of blobs pending migration.
	PendingBlobs int64 `json:"pending_blobs"`

	// CompletedBlobs is the number of blobs successfully migrated.
	CompletedBlobs int64 `json:"completed_blobs"`

	// FailedBlobs is the number of blobs that failed migration.
	FailedBlobs int64 `json:"failed_blobs"`

	// SkippedBlobs is the number of blobs skipped.
	SkippedBlobs int64 `json:"skipped_blobs"`

	// ProgressPercent is the overall progress percentage.
	ProgressPercent float64 `json:"progress_percent"`
}
