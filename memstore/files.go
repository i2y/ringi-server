package memstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"strings"
	"sync"
	"time"

	server "github.com/i2y/ringi-server"
)

// Files is a server.Files in memory: the attachments of a server that keeps its tickets in memory.
// With a Base, URL answers Base/<id> (as a store that serves its own bytes would); without one, ""
// (the server sends them). Everything is lost when the process ends.
type Files struct {
	mu    sync.Mutex
	files map[string]file
	Base  string
}

type file struct {
	info server.FileInfo
	data []byte
}

// Put keeps a file and answers its ID: 32 hexadecimal digits. A file with no time (FileInfo.At) is
// kept with the time it is put, as the other Files keep it; the time is kept to the microsecond, as
// PostgreSQL keeps it (us).
func (f *Files) Put(_ context.Context, info server.FileInfo, r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	var b [16]byte
	rand.Read(b[:])
	id := hex.EncodeToString(b[:])
	info.Size = int64(len(data))
	if info.At.IsZero() {
		info.At = time.Now()
	}
	info.At = us(info.At)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.files == nil {
		f.files = map[string]file{}
	}
	f.files[id] = file{info, data}
	return id, nil
}

// Stat answers what is kept of a file.
func (f *Files) Stat(_ context.Context, id string) (server.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	x, ok := f.files[id]
	if !ok {
		return server.FileInfo{}, server.ErrNotFound
	}
	return x.info, nil
}

// Open reads a file.
func (f *Files) Open(_ context.Context, id string) (io.ReadCloser, server.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	x, ok := f.files[id]
	if !ok {
		return nil, server.FileInfo{}, server.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(x.data)), x.info, nil
}

// Delete deletes a file.
func (f *Files) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.files[id]; !ok {
		return server.ErrNotFound
	}
	delete(f.files, id)
	return nil
}

// Has reports whether a file is kept.
func (f *Files) Has(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.files[id]
	return ok
}

// Len answers how many files are kept.
func (f *Files) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.files)
}

// URL answers Base/<id>, or "" with no Base.
func (f *Files) URL(_ context.Context, id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.files[id]; !ok {
		return "", server.ErrNotFound
	}
	if f.Base == "" {
		return "", nil
	}
	return strings.TrimSuffix(f.Base, "/") + "/" + id, nil
}
