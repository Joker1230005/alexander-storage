package storage

import "errors"

// Storage errors
var (
	// ErrBlobNotFound indicates that the requested blob was not found.
	ErrBlobNotFound = errors.New("blob not found in storage")

	// ErrBlobAlreadyExists indicates that a blob with the same hash already exists.
	ErrBlobAlreadyExists = errors.New("blob already exists")

	// ErrStorageFull indicates that storage is full.
	ErrStorageFull = errors.New("storage is full")

	// ErrInvalidContentHash indicates that the content hash is invalid.
	ErrInvalidContentHash = errors.New("invalid content hash")
)
