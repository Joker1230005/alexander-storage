// Package delta provides content-defined chunking and delta computation
// for efficient versioning in Alexander Storage.
package delta

import (
	"context"
	"io"
)

// Chunk represents a content-defined chunk of data.
type Chunk struct {
	// Hash is the SHA-256 hash of the chunk content.
	Hash string `json:"hash"`

	// Offset is the byte offset where this chunk starts in the source.
	Offset int64 `json:"offset"`

	// Size is the size of the chunk in bytes.
	Size int64 `json:"size"`

	// Data is the actual chunk data (may be nil if only metadata is needed).
	Data []byte `json:"-"`
}

// Delta represents the difference between two blobs.
type Delta struct {
	// SourceHash is the hash of the source (target) blob being created.
	SourceHash string `json:"source_hash"`

	// BaseHash is the hash of the base blob to apply delta against.
	BaseHash string `json:"base_hash"`

	// Instructions are the ordered list of copy/insert operations.
	Instructions []Instruction `json:"instructions"`

	// TotalSize is the total size of the reconstructed blob.
	TotalSize int64 `json:"total_size"`

	// DeltaSize is the size of the delta data (inserted chunks only).
	DeltaSize int64 `json:"delta_size"`

	// SavingsRatio is the percentage of space saved (1 - delta_size/total_size).
	SavingsRatio float64 `json:"savings_ratio"`
}

// Instruction represents a single delta instruction.
type Instruction struct {
	// Type is "copy" (from base) or "insert" (new data).
	Type InstructionType `json:"type"`

	// For "copy": byte offset in base blob.
	// For "insert": byte offset in delta data store.
	SourceOffset int64 `json:"source_offset"`

	// For "copy": byte offset in target blob.
	// For "insert": byte offset in target blob.
	TargetOffset int64 `json:"target_offset"`

	// Length is the number of bytes for this instruction.
	Length int64 `json:"length"`
}

// InstructionType represents the type of delta instruction.
type InstructionType string

const (
	// InstructionCopy copies bytes from the base blob.
	InstructionCopy InstructionType = "copy"

	// InstructionInsert inserts new bytes not in base.
	InstructionInsert InstructionType = "insert"
)

// Chunker splits content into variable-size chunks using content-defined chunking.
type Chunker interface {
	// Chunk reads from the reader and returns a channel of chunks.
	// The channel is closed when all chunks are emitted or an error occurs.
	Chunk(ctx context.Context, reader io.Reader) (<-chan Chunk, <-chan error)

	// ChunkAll reads all chunks into a slice (for smaller files).
	ChunkAll(ctx context.Context, reader io.Reader) ([]Chunk, error)
}

// DeltaComputer computes the delta between a base and target blob.
type DeltaComputer interface {
	// Compute calculates the delta needed to transform base into target.
	// Returns the delta containing copy/insert instructions.
	Compute(ctx context.Context, base, target io.Reader) (*Delta, error)

	// ComputeFromChunks calculates delta from pre-computed chunk lists.
	// This is more efficient when chunks are already computed/cached.
	ComputeFromChunks(ctx context.Context, baseChunks, targetChunks []Chunk) (*Delta, error)
}

// DeltaApplier reconstructs a blob by applying delta to a base blob.
type DeltaApplier interface {
	// Apply reconstructs the target blob from base + delta.
	// deltaData is a reader for the inserted data referenced by delta instructions.
	Apply(ctx context.Context, base io.ReadSeeker, delta *Delta, deltaData io.Reader) (io.Reader, error)
}

// ChunkIndex is an in-memory index of chunks for fast lookup.
type ChunkIndex interface {
	// Add adds a chunk to the index.
	Add(chunk Chunk)

	// AddAll adds multiple chunks to the index.
	AddAll(chunks []Chunk)

	// Lookup returns the chunk with the given hash, or nil if not found.
	Lookup(hash string) *Chunk

	// Exists returns true if a chunk with the given hash exists.
	Exists(hash string) bool

	// Size returns the number of chunks in the index.
	Size() int
}

// ChunkStore persists chunks for deduplication across blobs.
type ChunkStore interface {
	// Store stores a chunk and returns whether it's new (not deduplicated).
	Store(ctx context.Context, chunk *Chunk) (isNew bool, err error)

	// Get retrieves a chunk by its hash.
	Get(ctx context.Context, hash string) (*Chunk, error)

	// IncrementRef increments the reference count for a chunk.
	IncrementRef(ctx context.Context, hash string) error

	// DecrementRef decrements the reference count and returns the new count.
	DecrementRef(ctx context.Context, hash string) (newCount int, err error)

	// ListOrphans returns chunks with zero references.
	ListOrphans(ctx context.Context, limit int) ([]Chunk, error)
}
