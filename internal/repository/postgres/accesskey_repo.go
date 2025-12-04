package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/prn-tf/alexander-storage/internal/domain"
	"github.com/prn-tf/alexander-storage/internal/repository"
)

// accessKeyRepository implements repository.AccessKeyRepository.
type accessKeyRepository struct {
	db *DB
}

// NewAccessKeyRepository creates a new PostgreSQL access key repository.
func NewAccessKeyRepository(db *DB) repository.AccessKeyRepository {
	return &accessKeyRepository{db: db}
}

// Create creates a new access key.
func (r *accessKeyRepository) Create(ctx context.Context, key *domain.AccessKey) error {
	query := `
		INSERT INTO access_keys (user_id, access_key_id, encrypted_secret, description, status, created_at, expires_at, last_used_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id
	`

	err := r.db.Pool.QueryRow(ctx, query,
		key.UserID,
		key.AccessKeyID,
		key.EncryptedSecret,
		key.Description,
		key.Status,
		key.CreatedAt,
		key.ExpiresAt,
		key.LastUsedAt,
	).Scan(&key.ID)

	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: access key ID already exists", domain.ErrAccessKeyNotFound)
		}
		return fmt.Errorf("failed to create access key: %w", err)
	}

	return nil
}

// GetByID retrieves an access key by ID.
func (r *accessKeyRepository) GetByID(ctx context.Context, id int64) (*domain.AccessKey, error) {
	query := `
		SELECT id, user_id, access_key_id, encrypted_secret, description, status, created_at, expires_at, last_used_at
		FROM access_keys
		WHERE id = $1
	`

	key := &domain.AccessKey{}
	err := r.db.Pool.QueryRow(ctx, query, id).Scan(
		&key.ID,
		&key.UserID,
		&key.AccessKeyID,
		&key.EncryptedSecret,
		&key.Description,
		&key.Status,
		&key.CreatedAt,
		&key.ExpiresAt,
		&key.LastUsedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrAccessKeyNotFound
		}
		return nil, fmt.Errorf("failed to get access key by ID: %w", err)
	}

	return key, nil
}

// GetByAccessKeyID retrieves an access key by access key ID (20-char identifier).
func (r *accessKeyRepository) GetByAccessKeyID(ctx context.Context, accessKeyID string) (*domain.AccessKey, error) {
	query := `
		SELECT id, user_id, access_key_id, encrypted_secret, description, status, created_at, expires_at, last_used_at
		FROM access_keys
		WHERE access_key_id = $1
	`

	key := &domain.AccessKey{}
	err := r.db.Pool.QueryRow(ctx, query, accessKeyID).Scan(
		&key.ID,
		&key.UserID,
		&key.AccessKeyID,
		&key.EncryptedSecret,
		&key.Description,
		&key.Status,
		&key.CreatedAt,
		&key.ExpiresAt,
		&key.LastUsedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrAccessKeyNotFound
		}
		return nil, fmt.Errorf("failed to get access key by access key ID: %w", err)
	}

	return key, nil
}

// GetActiveByAccessKeyID retrieves an active, non-expired access key.
func (r *accessKeyRepository) GetActiveByAccessKeyID(ctx context.Context, accessKeyID string) (*domain.AccessKey, error) {
	query := `
		SELECT id, user_id, access_key_id, encrypted_secret, description, status, created_at, expires_at, last_used_at
		FROM access_keys
		WHERE access_key_id = $1 
			AND status = $2 
			AND (expires_at IS NULL OR expires_at > $3)
	`

	key := &domain.AccessKey{}
	err := r.db.Pool.QueryRow(ctx, query, accessKeyID, domain.AccessKeyStatusActive, time.Now().UTC()).Scan(
		&key.ID,
		&key.UserID,
		&key.AccessKeyID,
		&key.EncryptedSecret,
		&key.Description,
		&key.Status,
		&key.CreatedAt,
		&key.ExpiresAt,
		&key.LastUsedAt,
	)

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrAccessKeyNotFound
		}
		return nil, fmt.Errorf("failed to get active access key: %w", err)
	}

	return key, nil
}

// ListByUserID retrieves all access keys for a user.
func (r *accessKeyRepository) ListByUserID(ctx context.Context, userID int64) ([]*domain.AccessKey, error) {
	query := `
		SELECT id, user_id, access_key_id, encrypted_secret, description, status, created_at, expires_at, last_used_at
		FROM access_keys
		WHERE user_id = $1
		ORDER BY created_at DESC
	`

	rows, err := r.db.Pool.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list access keys: %w", err)
	}
	defer rows.Close()

	var keys []*domain.AccessKey
	for rows.Next() {
		key := &domain.AccessKey{}
		err := rows.Scan(
			&key.ID,
			&key.UserID,
			&key.AccessKeyID,
			&key.EncryptedSecret,
			&key.Description,
			&key.Status,
			&key.CreatedAt,
			&key.ExpiresAt,
			&key.LastUsedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan access key: %w", err)
		}
		keys = append(keys, key)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating access keys: %w", err)
	}

	return keys, nil
}

// Update updates an existing access key.
func (r *accessKeyRepository) Update(ctx context.Context, key *domain.AccessKey) error {
	query := `
		UPDATE access_keys
		SET description = $2, status = $3, expires_at = $4
		WHERE id = $1
	`

	result, err := r.db.Pool.Exec(ctx, query,
		key.ID,
		key.Description,
		key.Status,
		key.ExpiresAt,
	)

	if err != nil {
		return fmt.Errorf("failed to update access key: %w", err)
	}

	if result.RowsAffected() == 0 {
		return domain.ErrAccessKeyNotFound
	}

	return nil
}

// UpdateLastUsed updates the last used timestamp for an access key.
func (r *accessKeyRepository) UpdateLastUsed(ctx context.Context, id int64) error {
	query := `UPDATE access_keys SET last_used_at = $2 WHERE id = $1`

	now := time.Now().UTC()
	result, err := r.db.Pool.Exec(ctx, query, id, now)
	if err != nil {
		return fmt.Errorf("failed to update last used: %w", err)
	}

	if result.RowsAffected() == 0 {
		return domain.ErrAccessKeyNotFound
	}

	return nil
}

// Delete deletes an access key by ID.
func (r *accessKeyRepository) Delete(ctx context.Context, id int64) error {
	query := `DELETE FROM access_keys WHERE id = $1`

	result, err := r.db.Pool.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete access key: %w", err)
	}

	if result.RowsAffected() == 0 {
		return domain.ErrAccessKeyNotFound
	}

	return nil
}

// DeleteByAccessKeyID deletes an access key by access key ID.
func (r *accessKeyRepository) DeleteByAccessKeyID(ctx context.Context, accessKeyID string) error {
	query := `DELETE FROM access_keys WHERE access_key_id = $1`

	result, err := r.db.Pool.Exec(ctx, query, accessKeyID)
	if err != nil {
		return fmt.Errorf("failed to delete access key: %w", err)
	}

	if result.RowsAffected() == 0 {
		return domain.ErrAccessKeyNotFound
	}

	return nil
}

// DeleteExpired deletes all expired access keys.
func (r *accessKeyRepository) DeleteExpired(ctx context.Context) (int64, error) {
	query := `DELETE FROM access_keys WHERE expires_at IS NOT NULL AND expires_at < $1`

	result, err := r.db.Pool.Exec(ctx, query, time.Now().UTC())
	if err != nil {
		return 0, fmt.Errorf("failed to delete expired access keys: %w", err)
	}

	return result.RowsAffected(), nil
}

// Ensure accessKeyRepository implements repository.AccessKeyRepository
var _ repository.AccessKeyRepository = (*accessKeyRepository)(nil)
