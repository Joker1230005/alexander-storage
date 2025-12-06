// Package crypto provides cryptographic utilities for Alexander Storage.
package crypto

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"crypto/sha256"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

const (
	// ChaChaChunkSize is the default chunk size for streaming encryption (16MB).
	ChaChaChunkSize = 16 * 1024 * 1024

	// ChaChaKeySize is the key size for ChaCha20-Poly1305 (32 bytes).
	ChaChaKeySize = chacha20poly1305.KeySize

	// ChaChaNonceSize is the nonce size for ChaCha20-Poly1305 (12 bytes).
	ChaChaNonceSize = chacha20poly1305.NonceSize

	// ChaChaOverhead is the authentication tag overhead per chunk (16 bytes).
	ChaChaOverhead = chacha20poly1305.Overhead

	// ChaChaHeaderSize is the chunk header size (4 bytes for size + 12 bytes for nonce).
	ChaChaHeaderSize = 4 + ChaChaNonceSize

	// ChaChaEncryptionScheme is the identifier for this encryption scheme.
	ChaChaEncryptionScheme = "chacha20-poly1305-stream"
)

var (
	// ErrInvalidChunk indicates a corrupted or tampered chunk.
	ErrInvalidChunk = errors.New("invalid or corrupted chunk")

	// ErrChunkTooLarge indicates chunk size exceeds maximum.
	ErrChunkTooLarge = errors.New("chunk size exceeds maximum")

	// ErrChaChaDecryptionFailed indicates authentication failed during decryption.
	ErrChaChaDecryptionFailed = errors.New("chacha decryption failed: authentication error")
)

// ChaChaStreamEncryptor provides streaming encryption using ChaCha20-Poly1305.
// Each chunk is independently encrypted with a derived nonce, allowing:
// - Streaming without loading entire file into memory
// - Random access to chunks
// - Authenticated encryption with integrity verification
type ChaChaStreamEncryptor struct {
	masterKey []byte
	chunkSize int
}

// NewChaChaStreamEncryptor creates a new streaming encryptor.
// masterKey must be 32 bytes (256 bits).
func NewChaChaStreamEncryptor(masterKey []byte) (*ChaChaStreamEncryptor, error) {
	if len(masterKey) != ChaChaKeySize {
		return nil, fmt.Errorf("master key must be %d bytes, got %d", ChaChaKeySize, len(masterKey))
	}

	return &ChaChaStreamEncryptor{
		masterKey: masterKey,
		chunkSize: ChaChaChunkSize,
	}, nil
}

// SetChunkSize allows customizing the chunk size.
func (e *ChaChaStreamEncryptor) SetChunkSize(size int) {
	if size > 0 && size <= ChaChaChunkSize*4 { // Max 64MB chunks
		e.chunkSize = size
	}
}

// DeriveKey derives a unique encryption key for a specific blob using HKDF.
// salt should be unique per blob (e.g., content hash).
func (e *ChaChaStreamEncryptor) DeriveKey(salt []byte) ([]byte, error) {
	hkdfReader := hkdf.New(sha256.New, e.masterKey, salt, []byte("alexander-chacha-stream"))
	derivedKey := make([]byte, ChaChaKeySize)
	if _, err := io.ReadFull(hkdfReader, derivedKey); err != nil {
		return nil, fmt.Errorf("failed to derive key: %w", err)
	}
	return derivedKey, nil
}

// EncryptingReader wraps a reader to provide streaming encryption.
type EncryptingReader struct {
	source    io.Reader
	aead      cipher.AEAD
	chunkSize int
	buffer    []byte
	baseNonce []byte
	chunkNum  uint64
	done      bool
	pending   []byte // Buffered encrypted data not yet read
}

// NewEncryptingReader creates a reader that encrypts data on-the-fly.
// The reader produces chunks in format: [4-byte size][12-byte nonce][ciphertext][16-byte tag]
func (e *ChaChaStreamEncryptor) NewEncryptingReader(source io.Reader, salt []byte) (*EncryptingReader, error) {
	derivedKey, err := e.DeriveKey(salt)
	if err != nil {
		return nil, err
	}

	aead, err := chacha20poly1305.New(derivedKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create AEAD: %w", err)
	}

	// Generate random base nonce
	baseNonce := make([]byte, ChaChaNonceSize)
	if _, err := rand.Read(baseNonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	return &EncryptingReader{
		source:    source,
		aead:      aead,
		chunkSize: e.chunkSize,
		buffer:    make([]byte, e.chunkSize),
		baseNonce: baseNonce,
		chunkNum:  0,
		done:      false,
	}, nil
}

// deriveNonce creates a unique nonce for each chunk by XORing base nonce with chunk number.
func (r *EncryptingReader) deriveNonce() []byte {
	nonce := make([]byte, ChaChaNonceSize)
	copy(nonce, r.baseNonce)
	// XOR last 8 bytes with chunk number for uniqueness
	chunkBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(chunkBytes, r.chunkNum)
	for i := 0; i < 8; i++ {
		nonce[ChaChaNonceSize-8+i] ^= chunkBytes[i]
	}
	return nonce
}

// Read implements io.Reader for streaming encryption.
func (r *EncryptingReader) Read(p []byte) (int, error) {
	// First, drain any pending encrypted data
	if len(r.pending) > 0 {
		n := copy(p, r.pending)
		r.pending = r.pending[n:]
		return n, nil
	}

	if r.done {
		return 0, io.EOF
	}

	// Read a chunk of plaintext
	n, err := io.ReadFull(r.source, r.buffer)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return 0, fmt.Errorf("failed to read source: %w", err)
	}

	if n == 0 {
		r.done = true
		return 0, io.EOF
	}

	// Check if this is the last chunk
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		r.done = true
	}

	// Derive nonce for this chunk
	nonce := r.deriveNonce()
	r.chunkNum++

	// Encrypt the chunk
	ciphertext := r.aead.Seal(nil, nonce, r.buffer[:n], nil)

	// Build chunk packet: [size:4][nonce:12][ciphertext+tag]
	chunkPacketSize := ChaChaHeaderSize + len(ciphertext)
	packet := make([]byte, chunkPacketSize)
	binary.BigEndian.PutUint32(packet[0:4], uint32(len(ciphertext)))
	copy(packet[4:4+ChaChaNonceSize], nonce)
	copy(packet[ChaChaHeaderSize:], ciphertext)

	// Copy what fits into p, buffer the rest
	copied := copy(p, packet)
	if copied < len(packet) {
		r.pending = packet[copied:]
	}

	return copied, nil
}

// DecryptingReader wraps a reader to provide streaming decryption.
type DecryptingReader struct {
	source  io.Reader
	aead    cipher.AEAD
	pending []byte
	done    bool
}

// NewDecryptingReader creates a reader that decrypts data on-the-fly.
func (e *ChaChaStreamEncryptor) NewDecryptingReader(source io.Reader, salt []byte) (*DecryptingReader, error) {
	derivedKey, err := e.DeriveKey(salt)
	if err != nil {
		return nil, err
	}

	aead, err := chacha20poly1305.New(derivedKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create AEAD: %w", err)
	}

	return &DecryptingReader{
		source: source,
		aead:   aead,
		done:   false,
	}, nil
}

// Read implements io.Reader for streaming decryption.
func (r *DecryptingReader) Read(p []byte) (int, error) {
	// First, drain any pending decrypted data
	if len(r.pending) > 0 {
		n := copy(p, r.pending)
		r.pending = r.pending[n:]
		return n, nil
	}

	if r.done {
		return 0, io.EOF
	}

	// Read chunk header
	header := make([]byte, ChaChaHeaderSize)
	_, err := io.ReadFull(r.source, header)
	if err == io.EOF {
		r.done = true
		return 0, io.EOF
	}
	if err != nil {
		return 0, fmt.Errorf("failed to read chunk header: %w", err)
	}

	// Parse header
	ciphertextSize := binary.BigEndian.Uint32(header[0:4])
	if ciphertextSize > uint32(ChaChaChunkSize*4+ChaChaOverhead) {
		return 0, ErrChunkTooLarge
	}

	nonce := header[4 : 4+ChaChaNonceSize]

	// Read ciphertext
	ciphertext := make([]byte, ciphertextSize)
	_, err = io.ReadFull(r.source, ciphertext)
	if err != nil {
		return 0, fmt.Errorf("failed to read ciphertext: %w", err)
	}

	// Decrypt
	plaintext, err := r.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return 0, ErrChaChaDecryptionFailed
	}

	// Copy what fits into p, buffer the rest
	copied := copy(p, plaintext)
	if copied < len(plaintext) {
		r.pending = plaintext[copied:]
	}

	return copied, nil
}

// EncryptBlob encrypts an entire blob using streaming chunks.
// Returns the complete encrypted data.
// For large files, prefer NewEncryptingReader for streaming.
func (e *ChaChaStreamEncryptor) EncryptBlob(plaintext []byte, salt []byte) ([]byte, error) {
	derivedKey, err := e.DeriveKey(salt)
	if err != nil {
		return nil, err
	}

	aead, err := chacha20poly1305.New(derivedKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create AEAD: %w", err)
	}

	// Generate random base nonce
	baseNonce := make([]byte, ChaChaNonceSize)
	if _, err := rand.Read(baseNonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	var result []byte
	var chunkNum uint64

	for offset := 0; offset < len(plaintext); offset += e.chunkSize {
		end := offset + e.chunkSize
		if end > len(plaintext) {
			end = len(plaintext)
		}
		chunk := plaintext[offset:end]

		// Derive nonce for this chunk
		nonce := make([]byte, ChaChaNonceSize)
		copy(nonce, baseNonce)
		chunkBytes := make([]byte, 8)
		binary.BigEndian.PutUint64(chunkBytes, chunkNum)
		for i := 0; i < 8; i++ {
			nonce[ChaChaNonceSize-8+i] ^= chunkBytes[i]
		}
		chunkNum++

		// Encrypt chunk
		ciphertext := aead.Seal(nil, nonce, chunk, nil)

		// Append header + ciphertext
		header := make([]byte, 4)
		binary.BigEndian.PutUint32(header, uint32(len(ciphertext)))
		result = append(result, header...)
		result = append(result, nonce...)
		result = append(result, ciphertext...)
	}

	return result, nil
}

// DecryptBlob decrypts an entire blob that was encrypted with EncryptBlob.
func (e *ChaChaStreamEncryptor) DecryptBlob(ciphertext []byte, salt []byte) ([]byte, error) {
	derivedKey, err := e.DeriveKey(salt)
	if err != nil {
		return nil, err
	}

	aead, err := chacha20poly1305.New(derivedKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create AEAD: %w", err)
	}

	var result []byte
	offset := 0

	for offset < len(ciphertext) {
		// Read header
		if offset+ChaChaHeaderSize > len(ciphertext) {
			return nil, ErrInvalidChunk
		}

		chunkSize := binary.BigEndian.Uint32(ciphertext[offset : offset+4])
		nonce := ciphertext[offset+4 : offset+ChaChaHeaderSize]
		offset += ChaChaHeaderSize

		if offset+int(chunkSize) > len(ciphertext) {
			return nil, ErrInvalidChunk
		}

		chunkCiphertext := ciphertext[offset : offset+int(chunkSize)]
		offset += int(chunkSize)

		// Decrypt chunk
		plaintext, err := aead.Open(nil, nonce, chunkCiphertext, nil)
		if err != nil {
			return nil, ErrChaChaDecryptionFailed
		}

		result = append(result, plaintext...)
	}

	return result, nil
}

// GetScheme returns the encryption scheme identifier.
func (e *ChaChaStreamEncryptor) GetScheme() string {
	return ChaChaEncryptionScheme
}

// CalculateEncryptedSize calculates the encrypted size for a given plaintext size.
func (e *ChaChaStreamEncryptor) CalculateEncryptedSize(plaintextSize int64) int64 {
	if plaintextSize == 0 {
		return 0
	}

	chunkCount := (plaintextSize + int64(e.chunkSize) - 1) / int64(e.chunkSize)
	// Each chunk has: header (16 bytes) + ciphertext + tag (16 bytes)
	overhead := chunkCount * int64(ChaChaHeaderSize+ChaChaOverhead)
	return plaintextSize + overhead
}
