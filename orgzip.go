package server

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"math"
	"path"
	"slices"
	"strings"

	"github.com/i2y/ringi-server/internal/jatext"
)

// MaxOrgFiles is how many entries an upload of the organization's data may hold, its directories'
// among them.
const MaxOrgFiles = 10000

// ErrOrganizationTooLarge is the error of an upload of the organization's data past its limits: more
// than MaxOrgFiles entries, or more bytes than Config.MaxOrganization once uncompressed, in one file
// or in all, by the sizes the zip file records or by the bytes read. The API answers it 413.
var ErrOrganizationTooLarge = errors.New("the organization's data is too large")

// The signatures of a zip file's records this reader looks for.
var (
	zipDirectoryEnd   = []byte("PK\x05\x06")
	zipCentralHeader  = uint32(0x02014b50)
	zipLocalHeader    = uint32(0x04034b50)
	zip64Locator      = uint32(0x07064b50)
	zip64DirectoryEnd = uint32(0x06064b50)
)

// readUpload reads the organization's data from an upload: a zip file of the directory of the
// versions (want.Data) and the files of the masters, at their paths from the project's root, which
// ReadOrgFiles reads as it reads them in the project. It checks the zip file before it reads a file of
// it, and every refusal is a problem that names the entry (*OrgProblems) or, past a limit, an error
// that wraps ErrOrganizationTooLarge:
//
//   - more than MaxOrgFiles entries, as the end of its central directory says (its zip64 record too)
//     and as the central directory holds them, read as archive/zip reads it (zipDirectoryOf), before
//     archive/zip reads them all into memory: the bytes of the files are not counted;
//   - one file, or all, of more than max bytes once uncompressed, as the zip file records them;
//   - no zip file, or a broken one, and an entry whose own header (its local file header) names it
//     otherwise than the central directory, which a tool that reads the zip file from its start
//     would read as another file;
//   - an entry with no name, a name with `..` or `\`, one that starts with `/` or a drive (`C:`), or
//     another that is no clean path, and a name there twice;
//   - a symbolic link, and an entry that is neither a file nor a directory;
//   - a file ReadOrgFiles does not read (outside the versions' directory and the masters' files, or
//     in it other than a version's files and proxies.csv), and a directory not on the way to one;
//   - an encrypted file, and one compressed by a method other than deflate;
//   - more than max bytes read in all, whatever the zip file records, and a file whose bytes read do
//     not match the size and the checksum it records.
func readUpload(archive []byte, want OrgLayout, max int64) (*OrgData, error) {
	if want.Data == "" {
		return nil, errors.New("the kinds name no directory of the organization's data")
	}
	if max < 0 {
		max = 0
	}
	// archive/zip reads every entry of the central directory into memory, and reads on past the
	// number its end records (a number cut to 16 bits where the file is no zip64 file): the number
	// recorded, and the headers of the central directory as archive/zip reads them, are counted first
	dir, found := zipDirectoryOf(archive, MaxOrgFiles)
	if found && dir.says > MaxOrgFiles {
		return nil, fmt.Errorf("%w: the zip file says it holds %d entries, and it may hold %d", ErrOrganizationTooLarge, dir.says, MaxOrgFiles)
	}
	if len(dir.local) > MaxOrgFiles {
		return nil, fmt.Errorf("%w: the zip file holds more than %d entries", ErrOrganizationTooLarge, MaxOrgFiles)
	}
	if !found {
		return nil, uploadProblem("", "The upload is not a zip file", "アップロードしたファイルが zip ではありません")
	}
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return nil, uploadProblem("", "The zip file is broken: "+err.Error(), jatext.Join("zip ファイルが壊れています（", err.Error(), "）"))
	}
	if len(zr.File) > MaxOrgFiles {
		return nil, fmt.Errorf("%w: the zip file holds more than %d entries", ErrOrganizationTooLarge, MaxOrgFiles)
	}
	if len(zr.File) != len(dir.local) {
		// zipDirectoryOf reads the directory as archive/zip does: another count is a zip file that
		// reads in two ways
		return nil, uploadProblem("", "The zip file is broken: its central directory reads in two ways",
			"zip ファイルが壊れています（セントラルディレクトリが二通りに読めます）")
	}
	// the sizes it records, before a file is read
	var total uint64
	for _, f := range zr.File {
		size := f.UncompressedSize64
		if size > uint64(max) {
			return nil, fmt.Errorf("%w: %s is %d bytes once uncompressed, and the organization's data may be %d", ErrOrganizationTooLarge, escaped(f.Name, 200), size, max)
		}
		if size > uint64(max)-total {
			return nil, fmt.Errorf("%w: its files are more than %d bytes once uncompressed", ErrOrganizationTooLarge, max)
		}
		total += size
	}
	paths := uploadPathsOf(want)
	var problems []OrgProblem
	add := func(f *zip.File, en, ja string) {
		problems = append(problems, OrgProblem{At: escaped(f.Name, 200), Text: en, TextJa: ja})
	}
	seen := map[string]bool{}
	var files []*zip.File
	for i, f := range zr.File {
		if en, ja, bad := localProblem(archive, dir.local[i], f.Name); bad {
			add(f, en, ja)
			continue
		}
		if en, ja, bad := nameProblem(f.Name); bad {
			add(f, en, ja)
			continue
		}
		name := strings.TrimSuffix(f.Name, "/")
		if seen[name] {
			add(f, "The zip file holds this name twice", "zip に同じ名前が二つあります")
			continue
		}
		seen[name] = true
		mode, isDir := f.Mode(), strings.HasSuffix(f.Name, "/")
		switch {
		case mode&fs.ModeSymlink != 0:
			add(f, "The entry is a symbolic link: put the file itself in the zip file", "シンボリックリンクです。ファイルそのものを zip に入れてください")
		case isDir && !paths.dir(name):
			add(f, "The server reads nothing in this directory: "+paths.hold(), jatext.Join("サーバーは、このディレクトリの中を読みません。", paths.holdJa()))
		case isDir:
		case !mode.IsRegular():
			add(f, "The entry is neither a file nor a directory", "ファイルでもディレクトリでもありません")
		case !paths.file(name):
			add(f, "The server does not read this file: "+paths.hold(), jatext.Join("サーバーは、このファイルを読みません。", paths.holdJa()))
		case f.Flags&0x1 != 0:
			add(f, "The file is encrypted, and the server does not read encrypted files", "ファイルが暗号化されています。暗号化したファイルは読めません")
		case f.Method != zip.Store && f.Method != zip.Deflate:
			add(f, fmt.Sprintf("The file is compressed by a method the server does not read (%d): compress it with deflate, or not at all", f.Method),
				fmt.Sprintf("サーバーが読めない方式で圧縮されています（%d）。deflate で圧縮するか、圧縮しないでください", f.Method))
		default:
			files = append(files, f)
		}
	}
	if len(problems) > 0 {
		return nil, &OrgProblems{Problems: problems}
	}
	// the bytes themselves, counted whatever the zip file records, and checked against it
	left := max
	for _, f := range files {
		n, sum, err := readEntry(f, left)
		switch {
		case errors.Is(err, ErrOrganizationTooLarge):
			return nil, fmt.Errorf("%w: its files are more than %d bytes once uncompressed", ErrOrganizationTooLarge, max)
		case err != nil:
			add(f, "The file does not read: "+err.Error(), jatext.Join("ファイルを展開できません（", err.Error(), "）"))
		case uint64(n) != f.UncompressedSize64 || f.CRC32 != 0 && sum != f.CRC32:
			// archive/zip does not check a checksum of 0 either
			add(f, "The file does not match the size or the checksum the zip file records for it", "ファイルの中身が、zip に記録された大きさかチェックサムと合いません")
		}
		left -= n
	}
	if len(problems) > 0 {
		return nil, &OrgProblems{Problems: problems}
	}
	d, err := ReadOrgFiles(zr, want)
	var ps *OrgProblems
	if err == nil || errors.As(err, &ps) {
		return d, err
	}
	// every byte is in memory: a file that does not read is a broken zip file
	at := ""
	var pe *fs.PathError
	if errors.As(err, &pe) {
		at, err = escaped(pe.Path, 200), pe.Err
	}
	return nil, &OrgProblems{Problems: []OrgProblem{{At: at, Text: "The file does not read: " + err.Error(), TextJa: jatext.Join("ファイルを読めません（", err.Error(), "）")}}}
}

// uploadProblem is the refusal of an upload for one problem.
func uploadProblem(at, en, ja string) error {
	return &OrgProblems{Problems: []OrgProblem{{At: at, Text: en, TextJa: ja}}}
}

// readEntry reads a file of the zip file to its end, at most left bytes once uncompressed
// (ErrOrganizationTooLarge past them), by its own count of the bytes and not by the size the zip file
// records. It answers how many bytes it read and their checksum (CRC-32).
func readEntry(f *zip.File, left int64) (int64, uint32, error) {
	raw, err := f.OpenRaw()
	if err != nil {
		return 0, 0, err
	}
	r := raw
	if f.Method == zip.Deflate {
		fr := flate.NewReader(raw)
		defer fr.Close()
		r = fr
	}
	limit := left
	if limit < math.MaxInt64 {
		limit++
	}
	h := crc32.NewIEEE()
	n, err := io.Copy(h, io.LimitReader(r, limit))
	if n > left {
		return n, 0, ErrOrganizationTooLarge
	}
	return n, h.Sum32(), err
}

// zipDirectory is a zip file's central directory, as zipDirectoryOf reads it.
type zipDirectory struct {
	// says is how many entries the end of the directory says it holds (the zip64 record's number,
	// where the end points to one).
	says uint64
	// local are the offsets of the entries' own headers (their local file headers), one for each
	// header of the directory in its order, as archive/zip reads them into zip.Reader.File: at most
	// one more than the most asked for.
	local []int64
}

// zipDirectoryOf reads a zip file's central directory as archive/zip's zip.NewReader reads it, and
// keeps no more than the offsets of most+1 entries: the end of the directory, found in the last
// 65 KiB (the last signature, whose comment the file holds whole), and the zip64 record it points to;
// the offset the directory starts at, from the end's offset less the directory's size and offset
// (0 where a header reads at the offset the end says); and the directory's headers from there, to the
// first that does not read. found is false where there is no end, and the file is no zip file. Where
// archive/zip refuses the file, local holds what was read up to there.
func zipDirectoryOf(b []byte, most int) (d zipDirectory, found bool) {
	const endLen, locLen, end64Len = 22, 20, 56
	u16 := func(at int64) uint64 { return uint64(binary.LittleEndian.Uint16(b[at:])) }
	u32 := func(at int64) uint64 { return uint64(binary.LittleEndian.Uint32(b[at:])) }
	u64 := func(at int64) uint64 { return binary.LittleEndian.Uint64(b[at:]) }
	size := int64(len(b))
	at := int64(-1)
	for i := size - endLen; i >= 0 && i >= size-65*1024; i-- {
		if bytes.HasPrefix(b[i:], zipDirectoryEnd) {
			if comment := int64(u16(i + endLen - 2)); i+endLen+comment <= size {
				at = i
			}
			break
		}
	}
	if at < 0 {
		return d, false
	}
	d.says = u16(at + 10)
	dirSize, dirOffset := u32(at+12), u32(at+16)
	if d.says == 0xffff || dirSize == 0xffff || dirOffset == 0xffffffff {
		// a zip64 file: the locator before the end names the zip64 record, which archive/zip reads
		// (an offset past int64 it takes for none)
		if loc := at - locLen; loc >= 0 && u32(loc) == uint64(zip64Locator) && u32(loc+4) == 0 && u32(loc+16) == 1 && int64(u64(loc+8)) >= 0 {
			p := int64(u64(loc + 8))
			if size < end64Len || p > size-end64Len || u32(p) != uint64(zip64DirectoryEnd) {
				return d, true // archive/zip refuses it
			}
			at = p
			d.says, dirSize, dirOffset = u64(at+32), u64(at+40), u64(at+48)
		}
	}
	if dirSize > math.MaxInt64 || dirOffset > math.MaxInt64 {
		return d, true
	}
	base := at - int64(dirSize) - int64(dirOffset)
	if o := base + int64(dirOffset); o < 0 || o >= size {
		return d, true
	}
	if _, _, ok := centralHeader(b, int64(dirOffset)); base > 0 && ok {
		base = 0
	}
	for p := base + int64(dirOffset); len(d.local) <= most; {
		next, local, ok := centralHeader(b, p)
		if !ok {
			break
		}
		d.local = append(d.local, base+local)
		p = next
	}
	return d, true
}

// centralHeader reads the header of the central directory at p as archive/zip reads one: it answers
// where the next one starts, and the offset of the entry's own header (from the zip64 field of its
// extra data where the header says 0xffffffff). ok is false where archive/zip stops at it: the file
// ends in it, it has another signature, or its zip64 field does not hold the sizes and the offset it
// has to.
func centralHeader(b []byte, p int64) (next, local int64, ok bool) {
	const headerLen = 46
	if p < 0 || p > int64(len(b))-headerLen || binary.LittleEndian.Uint32(b[p:]) != zipCentralHeader {
		return 0, 0, false
	}
	h := b[p:]
	compressed, uncompressed := binary.LittleEndian.Uint32(h[20:]), binary.LittleEndian.Uint32(h[24:])
	nameLen, extraLen, commentLen := int64(binary.LittleEndian.Uint16(h[28:])), int64(binary.LittleEndian.Uint16(h[30:])), int64(binary.LittleEndian.Uint16(h[32:]))
	offset := uint64(binary.LittleEndian.Uint32(h[42:]))
	next = p + headerLen + nameLen + extraLen + commentLen
	if next > int64(len(b)) {
		return 0, 0, false
	}
	needU, needC, needH := uncompressed == math.MaxUint32, compressed == math.MaxUint32, offset == math.MaxUint32
	extra := h[headerLen+nameLen : headerLen+nameLen+extraLen]
	for len(extra) >= 4 {
		tag, n := binary.LittleEndian.Uint16(extra), int(binary.LittleEndian.Uint16(extra[2:]))
		if len(extra)-4 < n {
			break
		}
		field := extra[4 : 4+n]
		extra = extra[4+n:]
		if tag != 0x0001 { // the zip64 field
			continue
		}
		for _, need := range []*bool{&needU, &needC, &needH} {
			if !*need {
				continue
			}
			if len(field) < 8 {
				return 0, 0, false
			}
			if need == &needH {
				offset = binary.LittleEndian.Uint64(field)
			}
			*need, field = false, field[8:]
		}
	}
	if needC || needH {
		return 0, 0, false
	}
	return next, int64(offset), true
}

// localProblem says what is wrong with an entry's own header (its local file header) at the offset
// the central directory gives it, if anything: one that does not read, and one that names the entry
// otherwise than the central directory does (name), which a tool that reads the zip file from its
// start reads as another file than the server reads.
func localProblem(b []byte, at int64, name string) (en, ja string, bad bool) {
	const headerLen = 30
	if at < 0 || at > int64(len(b))-headerLen || binary.LittleEndian.Uint32(b[at:]) != zipLocalHeader {
		return "The zip file is broken: the file's local header does not read", "zip ファイルが壊れています。ファイルのローカルヘッダーを読めません", true
	}
	n := int64(binary.LittleEndian.Uint16(b[at+26:]))
	if at+headerLen+n > int64(len(b)) {
		return "The zip file is broken: the file's local header does not read", "zip ファイルが壊れています。ファイルのローカルヘッダーを読めません", true
	}
	if own := string(b[at+headerLen : at+headerLen+n]); own != name {
		return "The zip file is broken: the file's local header names it " + shown(own) + ", and the central directory " + shown(name),
			jatext.Join("zip ファイルが壊れています。ファイルのローカルヘッダーでは名前が", shown(own), "で、セントラルディレクトリでは", shown(name), "です"), true
	}
	return "", "", false
}

// nameProblem says what is wrong with the name of an entry of an upload, if anything: no name, `\`,
// a name that starts with `/` or a drive (`C:`), `..`, and another name that is no clean path (an
// empty part, `.`).
func nameProblem(name string) (en, ja string, bad bool) {
	const fromRoot, fromRootJa = "the zip file holds the paths from the project's root", "zip には、プロジェクトのルートからのパスで入れます"
	parts := strings.Split(strings.TrimSuffix(name, "/"), "/")
	switch {
	case name == "":
		return "The entry has no name", "名前の無い項目です", true
	case strings.Contains(name, `\`):
		return "The name holds `\\`: a zip file parts its directories with `/`", "名前に `\\` があります。zip のディレクトリの区切りは `/` です", true
	case strings.HasPrefix(name, "/"):
		return "The name starts with `/`: " + fromRoot, "名前が `/` で始まっています。" + fromRootJa, true
	case len(name) >= 2 && name[1] == ':' && ('a' <= name[0] && name[0] <= 'z' || 'A' <= name[0] && name[0] <= 'Z'):
		return "The name starts with a drive (`C:`): " + fromRoot, "名前がドライブ（`C:`）で始まっています。" + fromRootJa, true
	case slices.Contains(parts, ".."):
		return "The name holds `..`, which leads out of the directory it is in", "名前に、ディレクトリの外を指す `..` があります", true
	case !fs.ValidPath(strings.TrimSuffix(name, "/")):
		return "The name is no clean path: it holds an empty part or `.`", "名前に、空の部分か `.` があります", true
	}
	return "", "", false
}

// uploadPaths are the paths an upload may hold: the files ReadOrgFiles reads, and the directories on
// the way to them.
type uploadPaths struct {
	// data is the directory of the versions ("." for the root), masters the masters' files, and
	// ways the directories on the way to them, data among them.
	data    string
	masters map[string]bool
	ways    map[string]bool
}

func uploadPathsOf(want OrgLayout) uploadPaths {
	p := uploadPaths{data: path.Clean(want.Data), masters: map[string]bool{}, ways: map[string]bool{}}
	way := func(dir string) {
		for ; dir != "." && dir != "/"; dir = path.Dir(dir) {
			p.ways[dir] = true
		}
	}
	way(p.data)
	for _, m := range want.Masters {
		if m.Data != "" {
			f := path.Clean(m.Data)
			p.masters[f] = true
			way(path.Dir(f))
		}
	}
	return p
}

// under answers the rest of a path under the directory of the versions.
func (p uploadPaths) under(name string) (string, bool) {
	if p.data == "." {
		return name, true
	}
	return strings.CutPrefix(name, p.data+"/")
}

// file reports whether ReadOrgFiles reads the file: a master's, proxies.csv, or a version's file in a
// directory beside it (ReadOrgFiles says one that is not named by a day).
func (p uploadPaths) file(name string) bool {
	if p.masters[name] {
		return true
	}
	rest, ok := p.under(name)
	if !ok {
		return false
	}
	if rest == "proxies.csv" {
		return true
	}
	version, file, ok := strings.Cut(rest, "/")
	return ok && !strings.HasPrefix(version, ".") && (slices.Contains(versionFiles, file) || file == versionJSON)
}

// dir reports whether a directory is on the way to a file ReadOrgFiles reads, or is a version's.
func (p uploadPaths) dir(name string) bool {
	if p.ways[name] {
		return true
	}
	rest, ok := p.under(name)
	return ok && rest != "" && !strings.Contains(rest, "/") && !strings.HasPrefix(rest, ".")
}

// hold and holdJa say what an upload holds.
func (p uploadPaths) hold() string {
	return fmt.Sprintf("the zip file holds the directory %s of the organization's data and the files of the masters, at their paths from the project's root", shown(p.data))
}

func (p uploadPaths) holdJa() string {
	return jatext.Join("zip には、組織のデータのディレクトリ", shown(p.data), "とマスタのファイルを、プロジェクトのルートからのパスのまま入れます")
}
