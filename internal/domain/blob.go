// Package domain contains the core business entities for Alexander Storage.
package domain

import (
	"path/filepath"
	"time"
)

// BlobType represents the type of blob storage.
type BlobType string

const (
	// BlobTypeSingle is a regular single-file blob.
	BlobTypeSingle BlobType = "single"

	// BlobTypeComposite is a blob composed of multiple parts (multipart uploads).
	// Instead of concatenating parts, we store references to part blobs.
	BlobTypeComposite BlobType = "composite"

	// BlobTypeDelta is a blob stored as a delta from a base blob.
	// Used for versioning to save storage space.
	BlobTypeDelta BlobType = "delta"
)

// EncryptionScheme represents the encryption algorithm used.
type EncryptionScheme string

const (
	// EncryptionSchemeNone means no encryption.
	EncryptionSchemeNone EncryptionScheme = ""

	// EncryptionSchemeAESGCM is AES-256-GCM encryption (legacy).
	EncryptionSchemeAESGCM EncryptionScheme = "aes-256-gcm"

	// EncryptionSchemeChaCha is ChaCha20-Poly1305 streaming encryption.
	EncryptionSchemeChaCha EncryptionScheme = "chacha20-poly1305-stream"
)

// PartReference represents a reference to a part blob in composite blobs.
type PartReference struct {
	// PartIndex is the 0-based index of this part in the composite.
	PartIndex int `json:"part_index"`

	// ContentHash is the SHA-256 hash of the part content.
	ContentHash string `json:"content_hash"`

	// Offset is the byte offset where this part starts in the logical blob.
	Offset int64 `json:"offset"`

	// Size is the size of this part in bytes.
	Size int64 `json:"size"`
}

// DeltaInstruction represents an instruction for reconstructing a blob from a base.
type DeltaInstruction struct {
	// Type is "copy" (from base) or "insert" (new data).
	Type string `json:"type"`

	// For "copy": offset and length in base blob.
	// For "insert": offset in delta data and length of new data.
	Offset int64 `json:"offset"`
	Length int64 `json:"length"`
}

// Blob represents a content-addressable storage entry.
// Blobs are stored by their SHA-256 hash, enabling deduplication.
// Multiple objects can reference the same blob.
type Blob struct {
	// ContentHash is the SHA-256 hash of the content (64 hex characters).
	// This serves as the primary key and storage identifier.
	ContentHash string `json:"content_hash"`

	// Size is the size of the blob in bytes.
	Size int64 `json:"size"`

	// StoragePath is the path where the blob is stored on disk.
	// Format: /{base}/{first2chars}/{next2chars}/{fullhash}
	// Example: /data/ab/cd/abcdef1234567890...
	// For composite blobs, this may be empty (parts stored separately).
	StoragePath string `json:"storage_path"`

	// RefCount is the number of objects referencing this blob.
	// When RefCount reaches 0, the blob can be garbage collected.
	RefCount int32 `json:"ref_count"`

	// BlobType indicates how the blob data is stored.
	// "single" = regular file, "composite" = part references, "delta" = base + diff
	BlobType BlobType `json:"blob_type"`

	// IsEncrypted indicates whether the blob is stored encrypted (SSE-S3).
	// New blobs are always encrypted. Old blobs may be unencrypted (mixed mode).
	IsEncrypted bool `json:"is_encrypted"`

	// EncryptionScheme indicates which encryption algorithm is used.
	// Empty string means no encryption or legacy detection needed.
	EncryptionScheme EncryptionScheme `json:"encryption_scheme,omitempty"`

	// EncryptionIV is the initialization vector used for encryption.
	// For AES-GCM: 12-byte IV stored as base64.
	// For ChaCha20: base nonce stored as base64.
	// Nil for unencrypted blobs.
	EncryptionIV *string `json:"encryption_iv,omitempty"`

	// PartReferences holds references to parts for composite blobs.
	// Only populated when BlobType is "composite".
	PartReferences []PartReference `json:"part_references,omitempty"`

	// DeltaBaseHash is the content hash of the base blob for delta blobs.
	// Only populated when BlobType is "delta".
	DeltaBaseHash *string `json:"delta_base_hash,omitempty"`

	// DeltaInstructions holds the instructions for reconstructing from base.
	// Only populated when BlobType is "delta".
	DeltaInstructions []DeltaInstruction `json:"delta_instructions,omitempty"`

	// CreatedAt is the timestamp when the blob was first stored.
	CreatedAt time.Time `json:"created_at"`

	// LastAccessed is the timestamp when the blob was last read.
	LastAccessed time.Time `json:"last_accessed"`
}

// NewBlob creates a new Blob with the given hash and size.
// The storage path is computed from the hash using 2-level sharding.
// New blobs are always marked as encrypted (SSE-S3) with ChaCha20.
func NewBlob(contentHash string, size int64, basePath string) *Blob {
	now := time.Now().UTC()
	return &Blob{
		ContentHash:      contentHash,
		Size:             size,
		StoragePath:      ComputeStoragePath(basePath, contentHash),
		RefCount:         1,
		BlobType:         BlobTypeSingle,
		IsEncrypted:      true, // SSE-S3: all new blobs are encrypted
		EncryptionScheme: EncryptionSchemeChaCha,
		CreatedAt:        now,
		LastAccessed:     now,
	}
}

// NewCompositeBlob creates a new composite blob from part references.
func NewCompositeBlob(contentHash string, totalSize int64, parts []PartReference) *Blob {
	now := time.Now().UTC()
	return &Blob{
		ContentHash:      contentHash,
		Size:             totalSize,
		StoragePath:      "", // No physical file for composite
		RefCount:         1,
		BlobType:         BlobTypeComposite,
		IsEncrypted:      true,
		EncryptionScheme: EncryptionSchemeChaCha, // Parts are encrypted
		PartReferences:   parts,
		CreatedAt:        now,
		LastAccessed:     now,
	}
}

// NewDeltaBlob creates a new delta blob from a base blob.
func NewDeltaBlob(contentHash string, size int64, basePath string, baseHash string, instructions []DeltaInstruction) *Blob {
	now := time.Now().UTC()
	return &Blob{
		ContentHash:       contentHash,
		Size:              size,
		StoragePath:       ComputeStoragePath(basePath, contentHash),
		RefCount:          1,
		BlobType:          BlobTypeDelta,
		IsEncrypted:       true,
		EncryptionScheme:  EncryptionSchemeChaCha,
		DeltaBaseHash:     &baseHash,
		DeltaInstructions: instructions,
		CreatedAt:         now,
		LastAccessed:      now,
	}
}

// IsComposite returns true if this is a composite blob.
func (b *Blob) IsComposite() bool {
	return b.BlobType == BlobTypeComposite
}

// IsDelta returns true if this is a delta blob.
func (b *Blob) IsDelta() bool {
	return b.BlobType == BlobTypeDelta
}

// ComputeStoragePath generates the storage path for a blob using 2-level directory sharding.
// This distributes files across directories to avoid filesystem limitations.
//
// Example:
//
//	hash: "abcdef1234567890..."
//	basePath: "/data"
//	result: "/data/ab/cd/abcdef1234567890..."
func ComputeStoragePath(basePath, contentHash string) string {
	if len(contentHash) < 4 {
		return filepath.Join(basePath, contentHash)
	}

	level1 := contentHash[0:2]
	level2 := contentHash[2:4]

	return filepath.Join(basePath, level1, level2, contentHash)
}

// IsOrphan returns true if no objects reference this blob.
func (b *Blob) IsOrphan() bool {
	return b.RefCount <= 0
}

// CanGarbageCollect returns true if the blob is orphaned and old enough.
func (b *Blob) CanGarbageCollect(gracePeriod time.Duration) bool {
	if !b.IsOrphan() {
		return false
	}

	// Don't delete blobs that were just created (might be in-progress upload)
	return time.Since(b.CreatedAt) > gracePeriod
}
