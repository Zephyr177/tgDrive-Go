package telegram

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
)

// Fake is an in-memory Storage used by tests; no network involved.
type Fake struct {
	mu    sync.Mutex
	files map[string]*fakeEntry
	seq   int
}

type fakeEntry struct {
	name string
	data []byte
}

// NewFake returns an empty fake backend.
func NewFake() *Fake {
	return &Fake{files: map[string]*fakeEntry{}}
}

// Upload stores the bytes and returns a deterministic file id.
func (f *Fake) Upload(_ context.Context, name string, r io.Reader, _ int64) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	f.mu.Lock()
	f.seq++
	id := fmt.Sprintf("fake-%d", f.seq)
	f.files[id] = &fakeEntry{name: name, data: data}
	f.mu.Unlock()
	return id, nil
}

// Open streams back stored bytes along with the original document name.
func (f *Fake) Open(_ context.Context, fileID string) (*Blob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.files[fileID]
	if !ok {
		return nil, fmt.Errorf("fake telegram: unknown file id %q", fileID)
	}
	return &Blob{Reader: io.NopCloser(bytes.NewReader(e.data)), Name: e.name, Size: int64(len(e.data))}, nil
}

// Delete drops a stored object.
func (f *Fake) Delete(_ context.Context, fileID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.files, fileID)
	return nil
}
