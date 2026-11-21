package screens

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"time"
)

// Asset is a file a renderer reads from the server: ritsu's catalog (catalog.json) and its
// functions (functions.js, an ES module a browser loads as it is). Its name does not change with
// what it holds; its ETag does.
type Asset struct {
	Name        string // "catalog.json", "functions.js"
	ContentType string // "application/json", "text/javascript; charset=utf-8"
	Body        []byte // the file as ritsu wrote it; the caller does not change it
	ETag        string // a strong ETag: the body's SHA-256, quoted
}

func newAsset(name, contentType string, body []byte) Asset {
	sum := sha256.Sum256(body)
	return Asset{Name: name, ContentType: contentType, Body: body, ETag: `"` + hex.EncodeToString(sum[:]) + `"`}
}

// Asset is the file of a name: "catalog.json" or "functions.js" (fs.ErrNotExist for another).
func (s *Set) Asset(name string) (Asset, error) {
	a, ok := s.assets[name]
	if !ok {
		return Asset{}, fmt.Errorf("screens: no asset %q (catalog.json, functions.js): %w", name, fs.ErrNotExist)
	}
	return a, nil
}

// Assets is the two files as a file system, at their names. A server that serves it sets each
// file's Content-Type from Set.Asset rather than from its system's table of types.
func (s *Set) Assets() fs.FS {
	return assetFS(s.assets)
}

type assetFS map[string]Asset

func (a assetFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if name == "." {
		names := make([]string, 0, len(a))
		for n := range a {
			names = append(names, n)
		}
		slices.Sort(names)
		entries := make([]fs.DirEntry, len(names))
		for i, n := range names {
			entries[i] = fs.FileInfoToDirEntry(assetInfo{name: n, size: int64(len(a[n].Body))})
		}
		return &assetDir{entries: entries}, nil
	}
	f, ok := a[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return &assetFile{Reader: bytes.NewReader(f.Body), info: assetInfo{name: name, size: int64(len(f.Body))}}, nil
}

type assetFile struct {
	*bytes.Reader
	info assetInfo
}

func (f *assetFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *assetFile) Close() error               { return nil }

type assetDir struct {
	entries []fs.DirEntry
	at      int
}

func (d *assetDir) Stat() (fs.FileInfo, error) { return assetInfo{name: ".", dir: true}, nil }
func (d *assetDir) Close() error               { return nil }
func (d *assetDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: ".", Err: errors.New("is a directory")}
}

func (d *assetDir) ReadDir(n int) ([]fs.DirEntry, error) {
	rest := d.entries[d.at:]
	if n <= 0 {
		d.at = len(d.entries)
		return slices.Clone(rest), nil
	}
	if len(rest) == 0 {
		return nil, io.EOF
	}
	n = min(n, len(rest))
	d.at += n
	return slices.Clone(rest[:n]), nil
}

type assetInfo struct {
	name string
	size int64
	dir  bool
}

func (i assetInfo) Name() string       { return i.name }
func (i assetInfo) Size() int64        { return i.size }
func (i assetInfo) ModTime() time.Time { return time.Time{} }
func (i assetInfo) IsDir() bool        { return i.dir }
func (i assetInfo) Sys() any           { return nil }
func (i assetInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}
