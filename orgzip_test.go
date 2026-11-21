package server

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// zipEntry is an entry of a test's zip file: a file, or a directory by a name that ends in "/";
// mode makes it another kind of entry, and stored leaves it uncompressed. An entry with raw is
// written with that header and the bytes data as they are, whatever they hold.
type zipEntry struct {
	name, body string
	mode       fs.FileMode
	stored     bool
	raw        *zip.FileHeader
	data       []byte
}

// zipOf writes a zip file of the entries.
func zipOf(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for _, e := range entries {
		if e.raw != nil {
			f, err := w.CreateRaw(e.raw)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Write(e.data); err != nil {
				t.Fatal(err)
			}
			continue
		}
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.stored {
			h.Method = zip.Store
		}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// entriesOf are the entries of an upload of the files, at their paths, each directory before the
// files in it.
func entriesOf(f fstest.MapFS) []zipEntry {
	var names []string
	dirs := map[string]bool{}
	for name := range f {
		names = append(names, name)
		for d := path.Dir(name); d != "."; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	for d := range dirs {
		names = append(names, d+"/")
	}
	slices.Sort(names)
	var out []zipEntry
	for _, n := range names {
		e := zipEntry{name: n}
		if !strings.HasSuffix(n, "/") {
			e.body = string(f[n].Data)
		}
		out = append(out, e)
	}
	return out
}

// replaced is the entries with each of more in place of the entry of its name, or after them.
func replaced(entries []zipEntry, more ...zipEntry) []zipEntry {
	out := slices.Clone(entries)
	for _, m := range more {
		if i := slices.IndexFunc(out, func(e zipEntry) bool { return e.name == m.name }); i >= 0 {
			out[i] = m
		} else {
			out = append(out, m)
		}
	}
	return out
}

// without is the entries but the one of a name.
func without(entries []zipEntry, name string) []zipEntry {
	return slices.DeleteFunc(slices.Clone(entries), func(e zipEntry) bool { return e.name == name })
}

// rawEntry is a file written with a header that says the body, compressed with deflate or not, as
// change leaves the header.
func rawEntry(name, body string, deflate bool, change func(h *zip.FileHeader)) zipEntry {
	data := []byte(body)
	h := &zip.FileHeader{Name: name, Method: zip.Store, CRC32: crc32.ChecksumIEEE(data), UncompressedSize64: uint64(len(data))}
	if deflate {
		data, h.Method = deflated(body), zip.Deflate
	}
	h.CompressedSize64 = uint64(len(data))
	if change != nil {
		change(h)
	}
	return zipEntry{name: name, raw: h, data: data}
}

func deflated(s string) []byte {
	var b bytes.Buffer
	w, _ := flate.NewWriter(&b, flate.BestCompression)
	w.Write([]byte(s))
	w.Close()
	return b.Bytes()
}

// projectFiles are the files of a project an upload holds: the organization's directory and the
// masters' files.
func projectFiles(t *testing.T, fsys fs.FS, want OrgLayout) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	add := func(name string) {
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = &fstest.MapFile{Data: b}
	}
	err := fs.WalkDir(fsys, want.Data, func(name string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			add(name)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range want.Masters {
		add(m.Data)
	}
	return out
}

// asZip64 writes the end of a zip file again as a zip64 file's, whose record says it holds n
// entries.
func asZip64(t *testing.T, b []byte, n uint64) []byte {
	t.Helper()
	end := len(b) - 22
	if end < 0 || !bytes.HasPrefix(b[end:], []byte("PK\x05\x06")) {
		t.Fatal("the zip file does not end with the end of its central directory")
	}
	size, offset := binary.LittleEndian.Uint32(b[end+12:]), binary.LittleEndian.Uint32(b[end+16:])
	out := slices.Clone(b[:end])
	record := make([]byte, 56)
	binary.LittleEndian.PutUint32(record[0:], 0x06064b50)
	binary.LittleEndian.PutUint64(record[4:], 44)
	binary.LittleEndian.PutUint16(record[12:], 45)
	binary.LittleEndian.PutUint16(record[14:], 45)
	binary.LittleEndian.PutUint64(record[24:], n)
	binary.LittleEndian.PutUint64(record[32:], n)
	binary.LittleEndian.PutUint64(record[40:], uint64(size))
	binary.LittleEndian.PutUint64(record[48:], uint64(offset))
	locator := make([]byte, 20)
	binary.LittleEndian.PutUint32(locator[0:], 0x07064b50)
	binary.LittleEndian.PutUint64(locator[8:], uint64(len(out)))
	binary.LittleEndian.PutUint32(locator[16:], 1)
	e := slices.Clone(b[end:])
	binary.LittleEndian.PutUint16(e[8:], 0xffff)
	binary.LittleEndian.PutUint16(e[10:], 0xffff)
	binary.LittleEndian.PutUint32(e[12:], 0xffffffff)
	binary.LittleEndian.PutUint32(e[16:], 0xffffffff)
	return append(append(append(out, record...), locator...), e...)
}

// localHeader answers where the own header (the local file header) of the entry of a name starts.
func localHeader(t *testing.T, b []byte, name string) int {
	t.Helper()
	for i := 0; ; {
		j := bytes.Index(b[i:], []byte("PK\x03\x04"))
		if j < 0 {
			t.Fatalf("no local header names %s", name)
		}
		at := i + j
		if n := int(binary.LittleEndian.Uint16(b[at+26:])); at+30+n <= len(b) && string(b[at+30:at+30+n]) == name {
			return at
		}
		i = at + 4
	}
}

func TestReadUpload(t *testing.T) {
	s := testOrgServer(t)
	want, err := ReadOrgFiles(orgFiles(), s.layout)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(want)
	stored := entriesOf(orgFiles())
	for i := range stored {
		stored[i].stored = true
	}
	for name, archive := range map[string][]byte{
		"with its directories":       zipOf(t, entriesOf(orgFiles())...),
		"with no directories":        zipOf(t, slices.DeleteFunc(entriesOf(orgFiles()), func(e zipEntry) bool { return strings.HasSuffix(e.name, "/") })...),
		"stored":                     zipOf(t, stored...),
		"a zip64 file":               asZip64(t, zipOf(t, entriesOf(orgFiles())...), uint64(len(entriesOf(orgFiles())))),
		"a data record after a file": zipOf(t, replaced(entriesOf(orgFiles()), rawEntry(proxiesFile, string(orgFiles()[proxiesFile].Data), true, func(h *zip.FileHeader) { h.Flags |= 0x8 }))...),
	} {
		d, err := readUpload(archive, s.layout, 1<<20)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got, _ := json.Marshal(d); string(got) != string(b) {
			t.Errorf("%s: the upload reads as\n%s\nnot\n%s", name, got, b)
		}
		if got := rowAt(d.where.versions["2026-04-01"].members, 3, memberKey(d.Versions[0].Members[3])); got != membersFile+":5" {
			t.Errorf("%s: a row of the upload is at %q", name, got)
		}
		if err := s.CheckOrganization(d); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := readUpload(zipOf(t, entriesOf(orgFiles())...), OrgLayout{}, 1<<20); err == nil {
		t.Errorf("an upload with no directory of the data to read reads")
	}
}

// TestReadUploadWrittenColumns: in an upload, as in the files, a master's file may write a field's
// column by its other name, and reads as the files that write it by its name; a file that writes it
// by both is not read, and its problem names the two at the line of the columns.
func TestReadUploadWrittenColumns(t *testing.T) {
	s := writtenServer(t)
	want, err := ReadOrgFiles(orgFiles(), s.layout)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(want)
	written := orgFiles()
	written[accountsFile] = orgFile("code,名前,valid_from,valid_to\n5101,Travel,,\n5102,Meetings,2026-01-01,\n")
	d, err := readUpload(zipOf(t, entriesOf(written)...), s.layout, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := json.Marshal(d); string(got) != string(b) {
		t.Errorf("the upload that writes the other name reads as\n%s\nnot\n%s", got, b)
	}
	if err := s.CheckOrganization(d); err != nil {
		t.Error(err)
	}
	both := orgFiles()
	both[accountsFile] = orgFile("code,label,valid_from,valid_to,名前\n5101,Travel,,,Travel\n")
	_, err = readUpload(zipOf(t, entriesOf(both)...), s.layout, 1<<20)
	sameProblems(t, err, orgWant{accountsFile + ":1", "The columns `label` and `名前` are the same field, `label`: keep one of them"})
}

// The examples' organization uploaded reads as it reads from the project.
func TestReadUploadExamples(t *testing.T) {
	s := examplesServer(t)
	dir := os.DirFS("examples/project")
	want, err := ReadOrgFiles(dir, s.layout)
	if err != nil {
		t.Fatal(err)
	}
	d, err := readUpload(zipOf(t, entriesOf(projectFiles(t, dir, s.layout))...), s.layout, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(d)
	b, _ := json.Marshal(want)
	if string(a) != string(b) {
		t.Errorf("the upload reads as\n%s\nnot\n%s", a, b)
	}
	if err := s.CheckOrganization(d); err != nil {
		t.Error(err)
	}
}

func TestReadUploadProblems(t *testing.T) {
	s := testOrgServer(t)
	base := entriesOf(orgFiles())
	teams := string(orgFiles()[teamsFile].Data)
	with := func(more ...zipEntry) []byte { return zipOf(t, append(slices.Clone(base), more...)...) }
	for _, c := range []struct {
		name    string
		archive func(t *testing.T) []byte
		wants   []orgWant
	}{
		{"not a zip file", func(t *testing.T) []byte { return []byte(teams) },
			[]orgWant{{"", "The upload is not a zip file"}}},
		{"nothing", func(t *testing.T) []byte { return nil },
			[]orgWant{{"", "The upload is not a zip file"}}},
		{"a broken zip file", func(t *testing.T) []byte {
			b := with()
			i := bytes.Index(b, []byte("PK\x01\x02"))
			b[i+3] = 0x03
			return b
		}, []orgWant{{"", "The zip file is broken"}}},
		{"a name with ..", func(t *testing.T) []byte { return with(zipEntry{name: "org/../notes.txt", body: "x"}) },
			[]orgWant{{"org/../notes.txt", "The name holds `..`"}}},
		{"a name from /", func(t *testing.T) []byte { return with(zipEntry{name: "/org/2026-05-01/teams.csv", body: teams}) },
			[]orgWant{{"/org/2026-05-01/teams.csv", "The name starts with `/`"}}},
		{"a name with \\", func(t *testing.T) []byte { return with(zipEntry{name: `org\2026-05-01\teams.csv`, body: teams}) },
			[]orgWant{{`org\\2026-05-01\\teams.csv`, "The name holds `\\`"}}},
		{"a name from a drive", func(t *testing.T) []byte {
			return with(zipEntry{name: "C:/org/2026-05-01/teams.csv", body: teams}, zipEntry{name: "c:org", body: "x"})
		}, []orgWant{{"C:/org/2026-05-01/teams.csv", "The name starts with a drive (`C:`)"}, {"c:org", "The name starts with a drive (`C:`)"}}},
		{"no name", func(t *testing.T) []byte { return with(zipEntry{name: "", body: "x"}) },
			[]orgWant{{"", "The entry has no name"}}},
		{"a name that is no clean path", func(t *testing.T) []byte {
			return with(zipEntry{name: "org/./2026-05-01/teams.csv", body: teams}, zipEntry{name: "org//proxies.csv", body: "x"})
		}, []orgWant{{"org/./2026-05-01/teams.csv", "The name is no clean path"}, {"org//proxies.csv", "The name is no clean path"}}},
		{"a name twice", func(t *testing.T) []byte { return with(zipEntry{name: teamsFile, body: teams}) },
			[]orgWant{{teamsFile, "The zip file holds this name twice"}}},
		{"a file of a directory's name", func(t *testing.T) []byte { return with(zipEntry{name: "org/2026-04-01", body: "x"}) },
			[]orgWant{{"org/2026-04-01", "The zip file holds this name twice"}}},
		{"a symbolic link", func(t *testing.T) []byte {
			return zipOf(t, replaced(base, zipEntry{name: proxiesFile, body: "../shared/proxies.csv", mode: fs.ModeSymlink | 0o777})...)
		}, []orgWant{{proxiesFile, "The entry is a symbolic link"}}},
		{"a named pipe", func(t *testing.T) []byte {
			return zipOf(t, replaced(base, zipEntry{name: proxiesFile, mode: fs.ModeNamedPipe | 0o644})...)
		}, []orgWant{{proxiesFile, "The entry is neither a file nor a directory"}}},
		{"files the server does not read", func(t *testing.T) []byte {
			return with(zipEntry{name: "README.md", body: "x"}, zipEntry{name: "org/notes.txt", body: "x"}, zipEntry{name: "org/2026-04-01/extra.csv", body: "x"},
				zipEntry{name: "org/.drafts/teams.csv", body: "x"}, zipEntry{name: "masters/vendors.csv", body: "x"}, zipEntry{name: "backup/org/proxies.csv", body: "x"})
		}, []orgWant{
			{"README.md", "The server does not read this file"},
			{"org/notes.txt", "The server does not read this file"},
			{"org/2026-04-01/extra.csv", "The server does not read this file"},
			{"org/.drafts/teams.csv", "The server does not read this file"},
			{"masters/vendors.csv", "The server does not read this file"},
			{"backup/org/proxies.csv", "The server does not read this file"},
		}},
		{"directories the server reads nothing in", func(t *testing.T) []byte {
			return with(zipEntry{name: "docs/"}, zipEntry{name: "org/2026-04-01/old/"}, zipEntry{name: "org/.drafts/"})
		}, []orgWant{
			{"docs/", "The server reads nothing in this directory"},
			{"org/2026-04-01/old/", "The server reads nothing in this directory"},
			{"org/.drafts/", "The server reads nothing in this directory"},
		}},
		{"an encrypted file", func(t *testing.T) []byte {
			return zipOf(t, replaced(base, rawEntry(teamsFile, teams, false, func(h *zip.FileHeader) { h.Flags |= 0x1 }))...)
		}, []orgWant{{teamsFile, "The file is encrypted"}}},
		{"a method of compression the server does not read", func(t *testing.T) []byte {
			return zipOf(t, replaced(base, rawEntry(teamsFile, teams, false, func(h *zip.FileHeader) { h.Method = 12 }))...)
		}, []orgWant{{teamsFile, "The file is compressed by a method the server does not read (12)"}}},
		{"a size the file does not have", func(t *testing.T) []byte {
			return zipOf(t, replaced(base, rawEntry(teamsFile, teams, true, func(h *zip.FileHeader) { h.UncompressedSize64 += 5 }))...)
		}, []orgWant{{teamsFile, "The file does not match the size or the checksum the zip file records for it"}}},
		{"a checksum the file does not have", func(t *testing.T) []byte {
			return zipOf(t, replaced(base, rawEntry(teamsFile, teams, true, func(h *zip.FileHeader) { h.CRC32 ^= 1 }))...)
		}, []orgWant{{teamsFile, "The file does not match the size or the checksum the zip file records for it"}}},
		{"a file that does not inflate", func(t *testing.T) []byte {
			e := rawEntry(teamsFile, teams, true, nil)
			e.data = []byte("not deflate")
			e.raw.CompressedSize64 = uint64(len(e.data))
			return zipOf(t, replaced(base, e)...)
		}, []orgWant{{teamsFile, "The file does not read: flate: corrupt input"}}},
		{"a data record of another checksum", func(t *testing.T) []byte {
			e := rawEntry(teamsFile, teams, true, func(h *zip.FileHeader) { h.Flags |= 0x8 })
			b := zipOf(t, replaced(base, e)...)
			i := bytes.Index(b, append(slices.Clone(e.data), "PK\x07\x08"...))
			if i < 0 {
				t.Fatal("no data record after the file")
			}
			b[i+len(e.data)+4] ^= 0xff
			return b
		}, []orgWant{{teamsFile, "The file does not read: zip: checksum error"}}},
		{"a file its own header names otherwise", func(t *testing.T) []byte {
			b := with()
			copy(b[localHeader(t, b, teamsFile)+30:], "org/2026-04-01/notes.csv")
			return b
		}, []orgWant{{teamsFile, "The zip file is broken: the file's local header names it `org/2026-04-01/notes.csv`, and the central directory `org/2026-04-01/teams.csv`"}}},
		{"two files whose own headers name each other", func(t *testing.T) []byte {
			const extra = "org/2026-04-01/extra.csv"
			b := with(zipEntry{name: extra, body: "id\n"})
			i, j := localHeader(t, b, teamsFile), localHeader(t, b, extra)
			copy(b[i+30:], extra)
			copy(b[j+30:], teamsFile)
			return b
		}, []orgWant{
			{teamsFile, "The zip file is broken: the file's local header names it `org/2026-04-01/extra.csv`, and the central directory `org/2026-04-01/teams.csv`"},
			{"org/2026-04-01/extra.csv", "The zip file is broken: the file's local header names it `org/2026-04-01/teams.csv`, and the central directory `org/2026-04-01/extra.csv`"},
		}},
		{"a file whose own header does not read", func(t *testing.T) []byte {
			b := with()
			b[localHeader(t, b, teamsFile)+3] = 0x09
			return b
		}, []orgWant{{teamsFile, "The zip file is broken: the file's local header does not read"}}},
		{"the problems of the files", func(t *testing.T) []byte { return zipOf(t, without(base, membersFile)...) },
			[]orgWant{{"org/2026-04-01", "The version `2026-04-01` has no `members.csv`"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, err := readUpload(c.archive(t), s.layout, 1<<20)
			if d != nil {
				t.Errorf("an upload with problems is answered")
			}
			if errors.Is(err, ErrOrganizationTooLarge) {
				t.Fatalf("the upload is too large: %v", err)
			}
			sameProblems(t, err, c.wants...)
		})
	}
}

func TestReadUploadLimits(t *testing.T) {
	s := testOrgServer(t)
	base := entriesOf(orgFiles())
	tooLarge := func(name string, archive []byte, max int64) {
		t.Helper()
		_, err := readUpload(archive, s.layout, max)
		var ps *OrgProblems
		if !errors.Is(err, ErrOrganizationTooLarge) || errors.As(err, &ps) {
			t.Errorf("%s: the answer is %v, not ErrOrganizationTooLarge", name, err)
		}
	}
	// the entries, counted before archive/zip reads them
	var es []zipEntry
	for i := range MaxOrgFiles + 1 {
		es = append(es, zipEntry{name: fmt.Sprintf("org/2026-04-01/x%05d.csv", i), stored: true})
	}
	many := zipOf(t, es...)
	tooLarge("10,001 entries", many, 1<<20)
	saysOne := slices.Clone(many)
	binary.LittleEndian.PutUint16(saysOne[len(saysOne)-22+8:], 1)
	binary.LittleEndian.PutUint16(saysOne[len(saysOne)-22+10:], 1)
	tooLarge("10,001 entries the end of the directory says are 1", saysOne, 1<<20)
	tooLarge("a zip64 end that says 10,001 entries", asZip64(t, zipOf(t, base...), MaxOrgFiles+1), 1<<20)
	_, err := readUpload(zipOf(t, es[:MaxOrgFiles]...), s.layout, 1<<20)
	var ps *OrgProblems
	if !errors.As(err, &ps) || len(ps.Problems) != MaxOrgFiles {
		t.Errorf("10,000 entries answer %.200v", err)
	}
	// the entries are counted in the central directory, not in the bytes of the upload: a comment and
	// a file that hold the signature of the directory's headers (PK\x01\x02) more than MaxOrgFiles
	// times are no entries
	sig := strings.Repeat("PK\x01\x02", MaxOrgFiles+1)
	commented := zipOf(t, base...)
	binary.LittleEndian.PutUint16(commented[len(commented)-2:], uint16(len(sig)))
	commented = append(commented, sig...)
	if _, err := readUpload(commented, s.layout, 1<<20); err != nil {
		t.Errorf("a zip file whose comment holds the signature of the directory's headers: %.200v", err)
	}
	_, err = readUpload(zipOf(t, append(slices.Clone(base), zipEntry{name: "org/notes.txt", body: sig, stored: true})...), s.layout, 1<<20)
	if errors.Is(err, ErrOrganizationTooLarge) {
		t.Errorf("a zip file of a file that holds the signature of the directory's headers: %v", err)
	} else {
		sameProblems(t, err, orgWant{"org/notes.txt", "The server does not read this file"})
	}
	// the sizes the zip file records, before a file is read
	zeros := strings.Repeat("0", 1<<20)
	tooLarge("a file past the limit", zipOf(t, replaced(base, zipEntry{name: teamsFile, body: zeros})...), 64<<10)
	tooLarge("files past the limit", zipOf(t, replaced(base, zipEntry{name: teamsFile, body: zeros[:40<<10]}, zipEntry{name: peopleFile, body: zeros[:40<<10]})...), 64<<10)
	// the bytes read, whatever the zip file records
	tooLarge("a file larger than its header says", zipOf(t, replaced(base, rawEntry(teamsFile, zeros, true, func(h *zip.FileHeader) { h.UncompressedSize64 = 10 }))...), 64<<10)
	// at the limit
	var total int64
	for _, e := range base {
		total += int64(len(e.body))
	}
	if _, err := readUpload(zipOf(t, base...), s.layout, total); err != nil {
		t.Errorf("files of as many bytes as the limit: %v", err)
	}
	tooLarge("files of a byte more than the limit", zipOf(t, base...), total-1)
}
