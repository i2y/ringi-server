package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	server "github.com/i2y/ringi-server"
)

// DiskFiles keeps the attachments in a directory: each file at <dir>/<id[:2]>/<id>, and what is
// kept of it at <id>.json beside it, each written to a temporary file and renamed into place. An
// ID is 32 lowercase hexadecimal digits; any other is not looked up.
type DiskFiles struct{ dir string }

// NewDiskFiles makes the store of a directory, making the directory when it is not there.
func NewDiskFiles(dir string) (*DiskFiles, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &DiskFiles{dir: dir}, nil
}

// diskRecord is what is kept of a file.
type diskRecord struct {
	Name        string    `json:"name"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	By          string    `json:"by"`
	At          time.Time `json:"at"`
}

// isFileID reports whether id is 32 lowercase hexadecimal digits.
func isFileID(id string) bool {
	return len(id) == 32 && strings.Trim(id, "0123456789abcdef") == ""
}

func (d *DiskFiles) path(id string) string { return filepath.Join(d.dir, id[:2], id) }

// Put keeps a file and answers its ID.
func (d *DiskFiles) Put(_ context.Context, info server.FileInfo, r io.Reader) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b[:])
	sub := filepath.Join(d.dir, id[:2])
	if err := os.MkdirAll(sub, 0o700); err != nil {
		return "", err
	}
	n, err := place(sub, d.path(id), func(f *os.File) error {
		_, err := io.Copy(f, r)
		return err
	})
	if err != nil {
		return "", err
	}
	if info.At.IsZero() {
		info.At = time.Now()
	}
	rec, err := json.Marshal(diskRecord{Name: info.Name, ContentType: info.ContentType, Size: n, By: info.By, At: info.At.UTC()})
	if err != nil {
		return "", err
	}
	if _, err := place(sub, d.path(id)+".json", func(f *os.File) error {
		_, err := f.Write(rec)
		return err
	}); err != nil {
		os.Remove(d.path(id))
		return "", err
	}
	return id, nil
}

// place writes a file to a temporary file in dir and renames it to path, answering its size. On a
// failure nothing is left.
func place(dir, path string, write func(*os.File) error) (int64, error) {
	f, err := os.CreateTemp(dir, ".put-*")
	if err != nil {
		return 0, err
	}
	err = write(f)
	if err == nil {
		err = f.Sync()
	}
	var n int64
	if err == nil {
		var st os.FileInfo
		if st, err = f.Stat(); err == nil {
			n = st.Size()
		}
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
		return 0, err
	}
	return n, nil
}

// Stat answers what is kept of a file.
func (d *DiskFiles) Stat(_ context.Context, id string) (server.FileInfo, error) {
	if !isFileID(id) {
		return server.FileInfo{}, server.ErrNotFound
	}
	b, err := os.ReadFile(d.path(id) + ".json")
	if errors.Is(err, fs.ErrNotExist) {
		return server.FileInfo{}, server.ErrNotFound
	}
	if err != nil {
		return server.FileInfo{}, err
	}
	var rec diskRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return server.FileInfo{}, err
	}
	return server.FileInfo{Name: rec.Name, ContentType: rec.ContentType, Size: rec.Size, By: rec.By, At: rec.At}, nil
}

// Open reads a file.
func (d *DiskFiles) Open(ctx context.Context, id string) (io.ReadCloser, server.FileInfo, error) {
	info, err := d.Stat(ctx, id)
	if err != nil {
		return nil, server.FileInfo{}, err
	}
	f, err := os.Open(d.path(id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, server.FileInfo{}, server.ErrNotFound
	}
	if err != nil {
		return nil, server.FileInfo{}, err
	}
	return f, info, nil
}

// URL answers "": the server sends the bytes itself.
func (d *DiskFiles) URL(ctx context.Context, id string) (string, error) {
	_, err := d.Stat(ctx, id)
	return "", err
}

// Delete deletes a file: its record first, so that it is not there from then on, then its bytes.
// Bytes left by a failure are deleted when it is deleted again.
func (d *DiskFiles) Delete(_ context.Context, id string) error {
	if !isFileID(id) {
		return server.ErrNotFound
	}
	rec := os.Remove(d.path(id) + ".json")
	data := os.Remove(d.path(id))
	switch {
	case errors.Is(rec, fs.ErrNotExist) && errors.Is(data, fs.ErrNotExist):
		return server.ErrNotFound
	case rec != nil && !errors.Is(rec, fs.ErrNotExist):
		return rec
	case data != nil && !errors.Is(data, fs.ErrNotExist):
		return data
	}
	return nil
}
