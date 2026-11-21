package postgres

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"time"

	"github.com/jackc/pgx/v5"

	server "github.com/i2y/ringi-server"
)

// Files answers the store's attachments: a server.Files in the table files of the store's schema,
// each file's bytes in a bytea column. A file is read into memory whole, so keep the largest
// attachment the server takes (web.Config.MaxFile) to a size memory holds many of at once.
func (s *Store) Files() server.Files { return files{s} }

type files struct{ s *Store }

// Put keeps a file and answers its ID: 32 hexadecimal digits of 16 random bytes.
func (f files) Put(ctx context.Context, info server.FileInfo, r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b[:])
	if info.At.IsZero() {
		info.At = time.Now()
	}
	_, err = f.s.pool.Exec(ctx, f.s.sql["file_put"], id, info.Name, info.ContentType, int64(len(data)), info.By, info.At, data)
	if err != nil {
		return "", err
	}
	return id, nil
}

// Stat answers what is kept of a file, without its bytes.
func (f files) Stat(ctx context.Context, id string) (server.FileInfo, error) {
	var x server.FileInfo
	err := f.s.pool.QueryRow(ctx, f.s.sql["file_stat"], id).Scan(&x.Name, &x.ContentType, &x.Size, &x.By, &x.At)
	if errors.Is(err, pgx.ErrNoRows) {
		return server.FileInfo{}, server.ErrNotFound
	}
	x.At = x.At.UTC()
	return x, err
}

// Open reads a file.
func (f files) Open(ctx context.Context, id string) (io.ReadCloser, server.FileInfo, error) {
	var x server.FileInfo
	var data []byte
	err := f.s.pool.QueryRow(ctx, f.s.sql["file_open"], id).Scan(&x.Name, &x.ContentType, &x.Size, &x.By, &x.At, &data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, server.FileInfo{}, server.ErrNotFound
	}
	if err != nil {
		return nil, server.FileInfo{}, err
	}
	x.At = x.At.UTC()
	return io.NopCloser(bytes.NewReader(data)), x, nil
}

// Delete deletes a file.
func (f files) Delete(ctx context.Context, id string) error {
	tag, err := f.s.pool.Exec(ctx, f.s.sql["file_delete"], id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return server.ErrNotFound
	}
	return nil
}

// URL answers "": the server sends the bytes itself.
func (f files) URL(ctx context.Context, id string) (string, error) {
	if _, err := f.Stat(ctx, id); err != nil {
		return "", err
	}
	return "", nil
}
