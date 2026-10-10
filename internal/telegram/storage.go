// Package telegram abstracts the Telegram blob storage behind an interface so
// tests can substitute an in-memory fake for the real Bot API backend.
package telegram

import (
	"context"
	"io"
)

// Blob is an opened remote object.
type Blob struct {
	Reader io.ReadCloser
	Name   string // stored name (server-side path or original document name)
	Size   int64  // bytes, -1 when unknown
}

// Storage is the file backend used by tgDrive.
type Storage interface {
	// Upload stores a document and returns its Telegram file id.
	Upload(ctx context.Context, name string, r io.Reader, size int64) (fileID string, err error)
	// Open starts streaming a stored document by its file id.
	Open(ctx context.Context, fileID string) (*Blob, error)
	// Delete removes a stored object when the backend supports it.
	Delete(ctx context.Context, fileID string) error
}
