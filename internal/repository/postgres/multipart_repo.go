package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/prn-tf/alexander-storage/internal/domain"
	"github.com/prn-tf/alexander-storage/internal/repository"
)

// multipartRepository implements repository.MultipartUploadRepository.
type multipartRepository struct {
	db *DB
}

// NewMultipartRepository creates a new PostgreSQL multipart repository.
func NewMultipartRepository(db *DB) repository.MultipartUploadRepository {
	return &multipartRepository{db: db}
}

// Create creates a new multipart upload.
func (r *multipartRepository) Create(ctx context.Context, upload *domain.MultipartUpload) error {
	query := `
		INSERT INTO multipart_uploads (id, bucket_id, key, initiator_id, status, storage_class, metadata, initiated_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	_, err := r.db.Pool.Exec(ctx, query,
		upload.ID,
		upload.BucketID,
		upload.Key,
		upload.InitiatorID,
		upload.Status,
		upload.StorageClass,
		upload.Metadata,
		upload.InitiatedAt,
		upload.ExpiresAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create multipart upload: %w", err)
	}

	return nil
}

// GetByID retrieves a multipart upload by ID.
func (r *multipartRepository) GetByID(ctx context.Context, uploadID uuid.UUID) (*domain.MultipartUpload, error) {
	query := `
		SELECT id, bucket_id, key, initiator_id, status, storage_class, metadata, initiated_at, expires_at, completed_at
		FROM multipart_uploads
		WHERE id = $1
	`

	upload := &domain.MultipartUpload{}
	err := r.db.Pool.QueryRow(ctx, query, uploadID).Scan(
		&upload.ID,
		&upload.BucketID,
		&upload.Key,
		&upload.InitiatorID,
		&upload.Status,
		&upload.StorageClass,
		&upload.Metadata,
		&upload.InitiatedAt,
		&upload.ExpiresAt,
		&upload.CompletedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrMultipartUploadNotFound
		}
		return nil, fmt.Errorf("failed to get multipart upload: %w", err)
	}

	return upload, nil
}

// List returns multipart uploads for a bucket.
func (r *multipartRepository) List(ctx context.Context, bucketID int64, opts repository.MultipartListOptions) (*repository.MultipartListResult, error) {
	maxUploads := opts.MaxUploads
	if maxUploads <= 0 {
		maxUploads = 1000
	}

	query := `
		SELECT id, key, initiated_at, storage_class
		FROM multipart_uploads
		WHERE bucket_id = $1 AND status = $2
			AND ($3 = '' OR key LIKE $3 || '%')
			AND ($4 = '' OR key > $4)
		ORDER BY key ASC, initiated_at ASC
		LIMIT $5
	`

	rows, err := r.db.Pool.Query(ctx, query, bucketID, domain.MultipartStatusInProgress, opts.Prefix, opts.KeyMarker, maxUploads+1)
	if err != nil {
		return nil, fmt.Errorf("failed to list uploads: %w", err)
	}
	defer rows.Close()

	var uploads []*domain.MultipartUploadInfo
	for rows.Next() {
		info := &domain.MultipartUploadInfo{}
		var uploadID uuid.UUID
		err := rows.Scan(
			&uploadID,
			&info.Key,
			&info.Initiated,
			&info.StorageClass,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan upload: %w", err)
		}
		info.UploadID = uploadID.String()
		uploads = append(uploads, info)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating uploads: %w", err)
	}

	result := &repository.MultipartListResult{}

	if len(uploads) > maxUploads {
		result.IsTruncated = true
		result.NextKeyMarker = uploads[maxUploads-1].Key
		result.NextUploadIDMarker = uploads[maxUploads-1].UploadID
		result.Uploads = uploads[:maxUploads]
	} else {
		result.Uploads = uploads
	}

	return result, nil
}

// UpdateStatus updates the status of a multipart upload.
func (r *multipartRepository) UpdateStatus(ctx context.Context, uploadID uuid.UUID, status domain.MultipartStatus) error {
	var query string
	var err error

	if status == domain.MultipartStatusCompleted {
		query = `UPDATE multipart_uploads SET status = $2, completed_at = $3 WHERE id = $1`
		_, err = r.db.Pool.Exec(ctx, query, uploadID, status, time.Now().UTC())
	} else {
		query = `UPDATE multipart_uploads SET status = $2 WHERE id = $1`
		_, err = r.db.Pool.Exec(ctx, query, uploadID, status)
	}

	if err != nil {
		return fmt.Errorf("failed to update upload status: %w", err)
	}

	return nil
}

// Delete deletes a multipart upload.
func (r *multipartRepository) Delete(ctx context.Context, uploadID uuid.UUID) error {
	return r.db.WithTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		// Delete parts first
		_, err := tx.Exec(ctx, `DELETE FROM upload_parts WHERE upload_id = $1`, uploadID)
		if err != nil {
			return fmt.Errorf("failed to delete upload parts: %w", err)
		}

		// Delete upload
		result, err := tx.Exec(ctx, `DELETE FROM multipart_uploads WHERE id = $1`, uploadID)
		if err != nil {
			return fmt.Errorf("failed to delete upload: %w", err)
		}

		if result.RowsAffected() == 0 {
			return domain.ErrMultipartUploadNotFound
		}

		return nil
	})
}

// DeleteExpired deletes expired multipart uploads.
func (r *multipartRepository) DeleteExpired(ctx context.Context) (int64, error) {
	// First delete parts for expired uploads
	_, err := r.db.Pool.Exec(ctx, `
		DELETE FROM upload_parts 
		WHERE upload_id IN (
			SELECT id FROM multipart_uploads 
			WHERE status = $1 AND expires_at < $2
		)
	`, domain.MultipartStatusInProgress, time.Now().UTC())
	if err != nil {
		return 0, fmt.Errorf("failed to delete expired parts: %w", err)
	}

	// Then delete expired uploads
	result, err := r.db.Pool.Exec(ctx, `
		DELETE FROM multipart_uploads 
		WHERE status = $1 AND expires_at < $2
	`, domain.MultipartStatusInProgress, time.Now().UTC())
	if err != nil {
		return 0, fmt.Errorf("failed to delete expired uploads: %w", err)
	}

	return result.RowsAffected(), nil
}

// CreatePart creates a new upload part.
func (r *multipartRepository) CreatePart(ctx context.Context, part *domain.UploadPart) error {
	query := `
		INSERT INTO upload_parts (upload_id, part_number, content_hash, size, etag, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (upload_id, part_number) DO UPDATE
		SET content_hash = EXCLUDED.content_hash, size = EXCLUDED.size, etag = EXCLUDED.etag, created_at = EXCLUDED.created_at
		RETURNING id
	`

	err := r.db.Pool.QueryRow(ctx, query,
		part.UploadID,
		part.PartNumber,
		part.ContentHash,
		part.Size,
		part.ETag,
		part.CreatedAt,
	).Scan(&part.ID)

	if err != nil {
		return fmt.Errorf("failed to create upload part: %w", err)
	}

	return nil
}

// GetPart retrieves a specific part.
func (r *multipartRepository) GetPart(ctx context.Context, uploadID uuid.UUID, partNumber int) (*domain.UploadPart, error) {
	query := `
		SELECT id, upload_id, part_number, content_hash, size, etag, created_at
		FROM upload_parts
		WHERE upload_id = $1 AND part_number = $2
	`

	part := &domain.UploadPart{}
	err := r.db.Pool.QueryRow(ctx, query, uploadID, partNumber).Scan(
		&part.ID,
		&part.UploadID,
		&part.PartNumber,
		&part.ContentHash,
		&part.Size,
		&part.ETag,
		&part.CreatedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPartNotFound
		}
		return nil, fmt.Errorf("failed to get upload part: %w", err)
	}

	return part, nil
}

// ListParts returns all parts for an upload.
func (r *multipartRepository) ListParts(ctx context.Context, uploadID uuid.UUID, opts repository.PartListOptions) (*repository.PartListResult, error) {
	maxParts := opts.MaxParts
	if maxParts <= 0 {
		maxParts = 1000
	}

	query := `
		SELECT part_number, size, etag, created_at
		FROM upload_parts
		WHERE upload_id = $1 AND part_number > $2
		ORDER BY part_number ASC
		LIMIT $3
	`

	rows, err := r.db.Pool.Query(ctx, query, uploadID, opts.PartNumberMarker, maxParts+1)
	if err != nil {
		return nil, fmt.Errorf("failed to list parts: %w", err)
	}
	defer rows.Close()

	var parts []*domain.PartInfo
	for rows.Next() {
		part := &domain.PartInfo{}
		err := rows.Scan(
			&part.PartNumber,
			&part.Size,
			&part.ETag,
			&part.LastModified,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan part: %w", err)
		}
		parts = append(parts, part)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating parts: %w", err)
	}

	result := &repository.PartListResult{}

	if len(parts) > maxParts {
		result.IsTruncated = true
		result.NextPartNumberMarker = parts[maxParts-1].PartNumber
		result.Parts = parts[:maxParts]
	} else {
		result.Parts = parts
	}

	return result, nil
}

// DeleteParts deletes all parts for an upload.
func (r *multipartRepository) DeleteParts(ctx context.Context, uploadID uuid.UUID) error {
	_, err := r.db.Pool.Exec(ctx, `DELETE FROM upload_parts WHERE upload_id = $1`, uploadID)
	if err != nil {
		return fmt.Errorf("failed to delete parts: %w", err)
	}
	return nil
}

// GetPartsForCompletion returns parts in order for completing the upload.
func (r *multipartRepository) GetPartsForCompletion(ctx context.Context, uploadID uuid.UUID, partNumbers []int) ([]*domain.UploadPart, error) {
	query := `
		SELECT id, upload_id, part_number, content_hash, size, etag, created_at
		FROM upload_parts
		WHERE upload_id = $1 AND part_number = ANY($2)
		ORDER BY part_number ASC
	`

	rows, err := r.db.Pool.Query(ctx, query, uploadID, partNumbers)
	if err != nil {
		return nil, fmt.Errorf("failed to get parts for completion: %w", err)
	}
	defer rows.Close()

	var parts []*domain.UploadPart
	for rows.Next() {
		part := &domain.UploadPart{}
		err := rows.Scan(
			&part.ID,
			&part.UploadID,
			&part.PartNumber,
			&part.ContentHash,
			&part.Size,
			&part.ETag,
			&part.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan part: %w", err)
		}
		parts = append(parts, part)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating parts: %w", err)
	}

	return parts, nil
}

// Ensure multipartRepository implements repository.MultipartUploadRepository
var _ repository.MultipartUploadRepository = (*multipartRepository)(nil)
