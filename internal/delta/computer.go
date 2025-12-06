package delta

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

// Computer computes deltas between blobs using content-defined chunking.
type Computer struct {
	chunker Chunker
}

// NewComputer creates a new delta computer with the given chunker.
func NewComputer(chunker Chunker) *Computer {
	return &Computer{
		chunker: chunker,
	}
}

// NewComputerDefault creates a delta computer with default FastCDC chunker.
func NewComputerDefault() *Computer {
	return NewComputer(NewFastCDCDefault())
}

// Compute implements DeltaComputer interface.
func (c *Computer) Compute(ctx context.Context, base, target io.Reader) (*Delta, error) {
	// Chunk both base and target
	baseChunks, err := c.chunker.ChunkAll(ctx, base)
	if err != nil {
		return nil, fmt.Errorf("failed to chunk base: %w", err)
	}

	targetChunks, err := c.chunker.ChunkAll(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("failed to chunk target: %w", err)
	}

	return c.ComputeFromChunks(ctx, baseChunks, targetChunks)
}

// ComputeFromChunks implements DeltaComputer interface.
func (c *Computer) ComputeFromChunks(ctx context.Context, baseChunks, targetChunks []Chunk) (*Delta, error) {
	// Build index of base chunks
	baseIndex := NewMemoryIndex()
	for _, chunk := range baseChunks {
		baseIndex.Add(chunk)
	}

	// Compute hashes for source and base
	sourceHash := computeChunksHash(targetChunks)
	baseHash := computeChunksHash(baseChunks)

	var instructions []Instruction
	var totalSize int64
	var deltaSize int64
	var insertOffset int64 // Offset in delta data store

	targetOffset := int64(0)

	for _, targetChunk := range targetChunks {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if baseChunk := baseIndex.Lookup(targetChunk.Hash); baseChunk != nil {
			// Chunk exists in base - emit copy instruction
			instructions = append(instructions, Instruction{
				Type:         InstructionCopy,
				SourceOffset: baseChunk.Offset,
				TargetOffset: targetOffset,
				Length:       targetChunk.Size,
			})
		} else {
			// New chunk - emit insert instruction
			instructions = append(instructions, Instruction{
				Type:         InstructionInsert,
				SourceOffset: insertOffset,
				TargetOffset: targetOffset,
				Length:       targetChunk.Size,
			})
			insertOffset += targetChunk.Size
			deltaSize += targetChunk.Size
		}

		targetOffset += targetChunk.Size
		totalSize += targetChunk.Size
	}

	// Calculate savings ratio
	var savingsRatio float64
	if totalSize > 0 {
		savingsRatio = 1.0 - float64(deltaSize)/float64(totalSize)
	}

	return &Delta{
		SourceHash:   sourceHash,
		BaseHash:     baseHash,
		Instructions: instructions,
		TotalSize:    totalSize,
		DeltaSize:    deltaSize,
		SavingsRatio: savingsRatio,
	}, nil
}

// ExtractDeltaData extracts the insert data from target based on delta instructions.
// This is the data that needs to be stored alongside the delta.
func (c *Computer) ExtractDeltaData(ctx context.Context, target io.Reader, delta *Delta) ([]byte, error) {
	// Read all target data
	targetData, err := io.ReadAll(target)
	if err != nil {
		return nil, fmt.Errorf("failed to read target: %w", err)
	}

	// Calculate total insert size
	var insertSize int64
	for _, inst := range delta.Instructions {
		if inst.Type == InstructionInsert {
			insertSize += inst.Length
		}
	}

	// Extract insert data in order
	result := make([]byte, 0, insertSize)
	for _, inst := range delta.Instructions {
		if inst.Type == InstructionInsert {
			start := inst.TargetOffset
			end := start + inst.Length
			if end > int64(len(targetData)) {
				return nil, fmt.Errorf("instruction exceeds target size")
			}
			result = append(result, targetData[start:end]...)
		}
	}

	return result, nil
}

// computeChunksHash computes an overall hash for a list of chunks.
// This is used as the blob identifier.
func computeChunksHash(chunks []Chunk) string {
	hasher := sha256.New()
	for _, chunk := range chunks {
		hasher.Write([]byte(chunk.Hash))
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// MemoryIndex is an in-memory implementation of ChunkIndex.
type MemoryIndex struct {
	chunks map[string]*Chunk
}

// NewMemoryIndex creates a new in-memory chunk index.
func NewMemoryIndex() *MemoryIndex {
	return &MemoryIndex{
		chunks: make(map[string]*Chunk),
	}
}

// Add implements ChunkIndex interface.
func (m *MemoryIndex) Add(chunk Chunk) {
	m.chunks[chunk.Hash] = &chunk
}

// AddAll implements ChunkIndex interface.
func (m *MemoryIndex) AddAll(chunks []Chunk) {
	for _, chunk := range chunks {
		m.Add(chunk)
	}
}

// Lookup implements ChunkIndex interface.
func (m *MemoryIndex) Lookup(hash string) *Chunk {
	return m.chunks[hash]
}

// Exists implements ChunkIndex interface.
func (m *MemoryIndex) Exists(hash string) bool {
	_, ok := m.chunks[hash]
	return ok
}

// Size implements ChunkIndex interface.
func (m *MemoryIndex) Size() int {
	return len(m.chunks)
}

// Ensure Computer implements DeltaComputer
var _ DeltaComputer = (*Computer)(nil)

// Ensure MemoryIndex implements ChunkIndex
var _ ChunkIndex = (*MemoryIndex)(nil)

// Applier reconstructs blobs by applying deltas to base blobs.
type Applier struct{}

// NewApplier creates a new delta applier.
func NewApplier() *Applier {
	return &Applier{}
}

// Apply implements DeltaApplier interface.
func (a *Applier) Apply(ctx context.Context, base io.ReadSeeker, delta *Delta, deltaData io.Reader) (io.Reader, error) {
	// Read all delta data into memory for random access
	insertData, err := io.ReadAll(deltaData)
	if err != nil {
		return nil, fmt.Errorf("failed to read delta data: %w", err)
	}

	// Pre-allocate result buffer
	result := make([]byte, delta.TotalSize)

	insertOffset := int64(0)

	for _, inst := range delta.Instructions {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		switch inst.Type {
		case InstructionCopy:
			// Seek to source offset in base
			if _, err := base.Seek(inst.SourceOffset, io.SeekStart); err != nil {
				return nil, fmt.Errorf("failed to seek in base: %w", err)
			}

			// Read from base into result
			if _, err := io.ReadFull(base, result[inst.TargetOffset:inst.TargetOffset+inst.Length]); err != nil {
				return nil, fmt.Errorf("failed to read from base: %w", err)
			}

		case InstructionInsert:
			// Copy from insert data
			end := insertOffset + inst.Length
			if end > int64(len(insertData)) {
				return nil, fmt.Errorf("insert data exhausted")
			}
			copy(result[inst.TargetOffset:], insertData[insertOffset:end])
			insertOffset = end
		}
	}

	return bytes.NewReader(result), nil
}

// Ensure Applier implements DeltaApplier
var _ DeltaApplier = (*Applier)(nil)
