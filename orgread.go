package server

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/i2y/ringi-server/internal/jatext"
)

// ReadOrgFiles reads the organization's data from the files ritsu reads, in fsys (the directory
// ritsu gen was given as --root): in want.Data, a directory for each version, named by the day it
// takes effect, with teams.csv, people.csv and members.csv or one organization.json, and
// proxies.csv beside the versions; and the CSV file of each master. ritsu writes their columns in
// requests/organization/columns.md.
//
// It answers every problem it finds with the files' shape as *OrgProblems: no directory or no
// version, a directory not named by a day, a version with both kinds of file or with neither, a file
// that is not UTF-8 or does not read as CSV or JSON, a column missing, there twice or not one of its
// file's, a row of another number of values than there are columns, a row of a CSV file longer than
// 1 MiB or of more than 1,000 values, a key of organization.json not read, a `primary` that is not
// yes, no or empty. A byte order mark at the start of a file is not read. A directory whose name
// starts with a dot, and a file other than proxies.csv beside the versions, are not read. A file it
// cannot read is an error of its own (*fs.PathError). It stops at MaxOrgProblems problems.
//
// The data keeps the file and the line of each row, for CheckOrganization to say where a problem is.
// Its versions are in the order of their days, and its proxies and masters are empty, not nil, where
// there are none.
func ReadOrgFiles(fsys fs.FS, want OrgLayout) (*OrgData, error) {
	r := &orgReading{fsys: fsys}
	d, err := r.read(want)
	if err != nil {
		return nil, err
	}
	if len(r.problems) > 0 {
		return nil, &OrgProblems{Problems: r.problems, Stopped: r.stopped}
	}
	return d, nil
}

// The limits of a line of a CSV file of the organization's data, checked before the line is read
// into its values (csvLimit): a row (a line, or the lines of one row whose quoted value holds line
// breaks) of at most maxRowBytes bytes and maxRowValues values. They hold what one row costs to read,
// well past the rows of data that passes: its names, addresses and values have their own limits.
var (
	maxRowBytes  = 1 << 20
	maxRowValues = 1000
)

// orgColumn is a column of a file of the organization's data, and whether the file needs it.
// written is another name the file may write it in (a master's field as the .ringi file writes it);
// the rows keep its cells by name.
type orgColumn struct {
	name    string
	needed  bool
	written string
}

// The columns of the files of the organization, in the order ritsu writes them
// (requests/organization/columns.md), and the keys of the objects of organization.json: a column a
// later ritsu adds is added here.
var (
	teamColumns   = []orgColumn{{name: "id", needed: true}, {name: "name", needed: true}, {name: "name_ja", needed: false}, {name: "kind", needed: true}, {name: "parent", needed: true}, {name: "head", needed: true}}
	personColumns = []orgColumn{{name: "id", needed: true}, {name: "name", needed: true}, {name: "name_ja", needed: false}, {name: "email", needed: false}, {name: "language", needed: false}}
	memberColumns = []orgColumn{{name: "person", needed: true}, {name: "team", needed: true}, {name: "position", needed: true}, {name: "primary", needed: true}}
	proxyColumns  = []orgColumn{{name: "person", needed: true}, {name: "proxy", needed: true}, {name: "kind", needed: true}, {name: "from", needed: true}, {name: "to", needed: true}, {name: "requests", needed: false}}
)

// The files of a version, and the one file that holds the same instead of them.
var versionFiles = []string{"teams.csv", "people.csv", "members.csv"}

const versionJSON = "organization.json"

// masterColumns are the columns of a master's file: its key and its fields, then the days a row may
// be chosen on. It needs them all but those of the fields that may be left empty (MasterLayout.
// Optional), whose rows hold none when the file leaves them out. A field's column may be written by
// the field's name or by the name the .ringi file writes it in (MasterLayout.Written), as ritsu
// reads the file.
func masterColumns(m MasterLayout) []orgColumn {
	var out []orgColumn
	add := func(name string) {
		if name != "" && !slices.ContainsFunc(out, func(c orgColumn) bool { return c.name == name }) {
			out = append(out, orgColumn{name: name, needed: !m.Optional[name] || name == m.Key, written: m.Written[name]})
		}
	}
	if !slices.Contains(m.Fields, m.Key) {
		add(m.Key)
	}
	for _, f := range m.Fields {
		add(f)
	}
	add("valid_from")
	add("valid_to")
	return out
}

// orgWhere is where each row of the organization's data was read (OrgData.where): the files and the
// lines, for the checks to say where a problem is.
type orgWhere struct {
	// dir is the directory of the versions.
	dir      string
	versions map[string]*versionWhere // by the version's day
	proxies  *tableWhere              // nil with no proxies.csv
	masters  map[string]*tableWhere   // by the master's name
}

// versionWhere is where a version's rows were read: its directory, and the tables of its teams,
// people and members.
type versionWhere struct {
	dir                    string
	teams, people, members *tableWhere
}

// tableWhere is where the rows of a table were read: the file, the array of organization.json they
// are in ("" for a CSV file), the line each row starts on (0 where it is not known), and what each
// row was read as (its rowKey), so that a row the data no longer holds at its place is not said to be
// there.
type tableWhere struct {
	file  string
	array string
	lines []int
	keys  []string
}

// row answers where the row at place i of the table is, when it is the row read there.
func (t *tableWhere) row(i int, key string) (rowRef, bool) {
	if t == nil || i < 0 || i >= len(t.keys) || t.keys[i] != key {
		return rowRef{}, false
	}
	r := rowRef{file: t.file, line: t.lines[i], short: fmt.Sprintf("%s[%d]", t.array, i)}
	return r, true
}

// rowRef is where a row is: its file and line, its file and place in an array of organization.json,
// or, for data not read from files, its place in the data.
type rowRef struct {
	file string
	line int
	// short is the row's place among the rows of its table ("members[3]"), and long the same as a
	// problem says it of data not read from files ("version 2026-04-01, members[3]").
	short, long string
}

// at is where the row is, as a problem's At says it: "org/2026-04-01/members.csv:4",
// "org/2026-04-01/organization.json (members[3])", "version 2026-04-01, members[3]".
func (r rowRef) at() string {
	switch {
	case r.file != "" && r.line > 0:
		return fmt.Sprintf("%s:%d", escaped(r.file, 200), r.line)
	case r.file != "":
		return fmt.Sprintf("%s (%s)", escaped(r.file, 200), r.short)
	}
	return r.long
}

// en and ja name the row in a sentence about another row of its table: "line 4", "4行目",
// "members[3]".
func (r rowRef) en() string {
	if r.line > 0 {
		return fmt.Sprintf("line %d", r.line)
	}
	return r.short
}

func (r rowRef) ja() string {
	if r.line > 0 {
		return fmt.Sprintf("%d行目", r.line)
	}
	return r.short
}

// What identifies a row of each table in its tableWhere: the IDs it is read by, which the checks do
// not change.
func teamKey(t Team) string        { return t.ID }
func personKey(p OrgPerson) string { return p.ID }
func memberKey(m Member) string    { return m.Person + "\x00" + m.Team }
func proxyKey(p Proxy) string {
	return p.Person + "\x00" + p.Proxy + "\x00" + p.Kind + "\x00" + p.From + "\x00" + p.To
}
func masterKey(r map[string]string, key string) string {
	return r[key] + "\x00" + r["valid_from"] + "\x00" + r["valid_to"]
}

// orgReading is one reading of the organization's files, with the problems found in them.
type orgReading struct {
	fsys     fs.FS
	problems []OrgProblem
	// stopped says the reading stopped at MaxOrgProblems problems.
	stopped bool
}

func (r *orgReading) add(at, en, ja string) {
	if r.full() {
		return
	}
	r.problems = append(r.problems, OrgProblem{At: at, Text: en, TextJa: ja})
}

// full reports whether the reading has MaxOrgProblems problems: it reads no more, and says it
// stopped.
func (r *orgReading) full() bool {
	if len(r.problems) >= MaxOrgProblems {
		r.stopped = true
		return true
	}
	return false
}

// fileAt is a place in a file, as a problem's At says it: "org/2026-04-01/members.csv:4", or the
// file alone for line 0.
func fileAt(file string, line int) string {
	if line <= 0 {
		return escaped(file, 200)
	}
	return fmt.Sprintf("%s:%d", escaped(file, 200), line)
}

func (r *orgReading) read(want OrgLayout) (*OrgData, error) {
	if want.Data == "" {
		return nil, errors.New("the kinds name no directory of the organization's data")
	}
	dir := path.Clean(want.Data)
	w := &orgWhere{dir: dir, versions: map[string]*versionWhere{}, masters: map[string]*tableWhere{}}
	d := &OrgData{Versions: []OrgVersion{}, Proxies: []Proxy{}, Masters: map[string][]map[string]string{}, where: w}
	if err := r.versions(d, dir); err != nil {
		return nil, err
	}
	for _, m := range want.Masters {
		if r.full() {
			break
		}
		if err := r.master(d, m); err != nil {
			return nil, err
		}
	}
	return d, nil
}

// versions reads the directory of the versions: each version, and proxies.csv.
func (r *orgReading) versions(d *OrgData, dir string) error {
	info, err := fs.Stat(r.fsys, dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		r.add(escaped(dir, 200), fmt.Sprintf("There is no directory %s of the organization's data", shown(dir)),
			jatext.Join("組織のデータのディレクトリ", shown(dir), "がありません"))
		return nil
	case err != nil:
		return err
	case !info.IsDir():
		r.add(escaped(dir, 200), fmt.Sprintf("%s is not a directory: the organization's data is a directory of its versions", shown(dir)),
			jatext.Join(shown(dir), "がディレクトリではありません。組織のデータは、版を並べたディレクトリです"))
		return nil
	}
	entries, err := fs.ReadDir(r.fsys, dir)
	if err != nil {
		return err
	}
	var days []string
	proxies, refused := false, false
	for _, e := range entries {
		name, p := e.Name(), path.Join(dir, e.Name())
		isDir := e.IsDir()
		if e.Type()&fs.ModeSymlink != 0 {
			// a link is read as what it leads to, as the files under it are
			if info, err := fs.Stat(r.fsys, p); err == nil {
				isDir = info.IsDir()
			}
		}
		switch {
		case !isDir:
			// a file beside the versions other than proxies.csv is not read
			proxies = proxies || name == "proxies.csv"
		case strings.HasPrefix(name, "."):
			// a directory whose name starts with a dot holds no version, as ritsu reads the directory
		case !isOrgDay(name):
			refused = true
			r.add(escaped(p, 200), fmt.Sprintf("The directory %s is not named by a day, as a version's directory is: name it after the day the version takes effect (`2026-04-01`)", shown(name)),
				jatext.Join("ディレクトリ", shown(name), "の名前が日付ではありません。版のディレクトリには、その版が効き始める日（`2026-04-01`）の名前を付けます"))
		default:
			days = append(days, name)
		}
	}
	slices.Sort(days)
	if len(days) == 0 && !refused {
		r.add(escaped(dir, 200), fmt.Sprintf("%s holds no version: a directory named by the day the version takes effect (`2026-04-01`)", shown(dir)),
			jatext.Join(shown(dir), "に版がありません。版は、効き始める日の名前（`2026-04-01`）のディレクトリです"))
	}
	for _, day := range days {
		if r.full() {
			return nil
		}
		v, vw, err := r.version(path.Join(dir, day), day)
		if err != nil {
			return err
		}
		if v != nil {
			d.Versions = append(d.Versions, *v)
			d.where.versions[day] = vw
		}
	}
	if proxies {
		return r.proxies(d, path.Join(dir, "proxies.csv"))
	}
	return nil
}

// isFile reports whether name is a file, and says a directory of that name. It answers false for no
// such file.
func (r *orgReading) isFile(name string) (bool, error) {
	info, err := fs.Stat(r.fsys, name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, err
	case info.IsDir():
		r.add(escaped(name, 200), fmt.Sprintf("%s is a directory, not a file", shown(path.Base(name))),
			jatext.Join(shown(path.Base(name)), "がファイルではなく、ディレクトリです"))
		return false, nil
	}
	return true, nil
}

// version reads a version's directory: nil when a problem is found in it (said).
func (r *orgReading) version(dir, day string) (*OrgVersion, *versionWhere, error) {
	before := len(r.problems)
	var missing []string
	for _, f := range versionFiles {
		ok, err := r.isFile(path.Join(dir, f))
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			missing = append(missing, f)
		}
	}
	hasJSON, err := r.isFile(path.Join(dir, versionJSON))
	if err != nil {
		return nil, nil, err
	}
	if len(r.problems) > before {
		return nil, nil, nil
	}
	at, v := escaped(dir, 200), shown(day)
	switch {
	case hasJSON && len(missing) < len(versionFiles):
		r.add(at, fmt.Sprintf("The version %s holds both `organization.json` and CSV files: keep one of them", v),
			jatext.Join("版", v, "に `organization.json` と CSV のファイルの両方があります。どちらか一方にしてください"))
		return nil, nil, nil
	case hasJSON:
		return r.jsonVersion(dir, day)
	case len(missing) == len(versionFiles):
		r.add(at, fmt.Sprintf("The version %s holds no data: a version holds `teams.csv`, `people.csv` and `members.csv`, or one `organization.json`", v),
			jatext.Join("版", v, "にデータがありません。版には、`teams.csv`、`people.csv`、`members.csv` の三つか、`organization.json` 一つを置きます"))
		return nil, nil, nil
	case len(missing) > 0:
		for _, f := range missing {
			r.add(at, fmt.Sprintf("The version %s has no `%s`: a version holds `teams.csv`, `people.csv` and `members.csv`, or one `organization.json`", v, f),
				jatext.Join("版", v, "に", "`"+f+"`", "がありません。版には、`teams.csv`、`people.csv`、`members.csv` の三つか、`organization.json` 一つを置きます"))
		}
		return nil, nil, nil
	}
	var tables []*orgTable
	for i, cols := range [][]orgColumn{teamColumns, personColumns, memberColumns} {
		t, err := r.csvTable(path.Join(dir, versionFiles[i]), cols)
		if err != nil {
			return nil, nil, err
		}
		tables = append(tables, t)
	}
	return r.versionOf(dir, day, tables[0], tables[1], tables[2])
}

// versionOf makes a version of its tables, read from its CSV files or its organization.json: nil
// when a problem is found in them (said), the one with `primary` among them.
func (r *orgReading) versionOf(dir, day string, teams, people, members *orgTable) (*OrgVersion, *versionWhere, error) {
	if !teams.ok || !people.ok || !members.ok {
		return nil, nil, nil
	}
	v := &OrgVersion{Day: day, Teams: []Team{}, People: []OrgPerson{}, Members: []Member{}}
	vw := &versionWhere{dir: dir, teams: teams.where(), people: people.where(), members: members.where()}
	for _, x := range teams.rows {
		t := Team{ID: x["id"], Name: x["name"], NameJa: x["name_ja"], Kind: x["kind"], Parent: x["parent"], Head: x["head"]}
		v.Teams = append(v.Teams, t)
		vw.teams.keys = append(vw.teams.keys, teamKey(t))
	}
	for _, x := range people.rows {
		p := OrgPerson{ID: x["id"], Name: x["name"], NameJa: x["name_ja"], Email: x["email"], Language: x["language"]}
		v.People = append(v.People, p)
		vw.people.keys = append(vw.people.keys, personKey(p))
	}
	ok := true
	for i, x := range members.rows {
		m := Member{Person: x["person"], Team: x["team"], Position: x["position"]}
		switch p := x["primary"]; p {
		case "yes":
			m.Primary = true
		case "no", "":
		default:
			ok = false
			r.primary(members, i, shown(p))
		}
		v.Members = append(v.Members, m)
		vw.members.keys = append(vw.members.keys, memberKey(m))
	}
	if !ok {
		return nil, nil, nil
	}
	return v, vw, nil
}

// primary says a row's `primary` is not one the files write: written is the value, as a problem
// quotes it.
func (r *orgReading) primary(t *orgTable, i int, written string) {
	at := fileAt(t.file, t.lines[i])
	if t.array == "" {
		r.add(at, fmt.Sprintf("`primary` is `yes`, `no` or empty, not %s", written),
			jatext.Join("`primary` は `yes`、`no`、空のどれかです。", written, "ではありません"))
		return
	}
	if t.lines[i] == 0 {
		at = fmt.Sprintf("%s (%s[%d])", escaped(t.file, 200), t.array, i)
	}
	r.add(at, fmt.Sprintf("`primary` is true, false, `yes`, `no`, empty or null, not %s", written),
		jatext.Join("`primary` は true、false、`yes`、`no`、空、null のどれかです。", written, "ではありません"))
}

// proxies reads proxies.csv.
func (r *orgReading) proxies(d *OrgData, name string) error {
	t, err := r.csvTable(name, proxyColumns)
	if err != nil || !t.ok {
		return err
	}
	w := t.where()
	for _, x := range t.rows {
		p := Proxy{Person: x["person"], Proxy: x["proxy"], Kind: x["kind"], From: x["from"], To: x["to"]}
		if f := strings.Fields(x["requests"]); len(f) > 0 {
			p.Requests = f
		}
		d.Proxies = append(d.Proxies, p)
		w.keys = append(w.keys, proxyKey(p))
	}
	d.where.proxies = w
	return nil
}

// master reads a master's file.
func (r *orgReading) master(d *OrgData, m MasterLayout) error {
	if m.Data == "" {
		r.add("master "+escaped(m.Name, 200), fmt.Sprintf("The kinds name no file of the rows of the master %s", shown(m.Name)),
			jatext.Join("マスタ", shown(m.Name), "の行のファイルを、種類の説明が挙げていません"))
		return nil
	}
	name := path.Clean(m.Data)
	info, err := fs.Stat(r.fsys, name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		r.add(escaped(name, 200), fmt.Sprintf("There is no file %s of the rows of the master %s", shown(name), shown(m.Name)),
			jatext.Join("マスタ", shown(m.Name), "の行のファイル", shown(name), "がありません"))
		return nil
	case err != nil:
		return err
	case info.IsDir():
		r.add(escaped(name, 200), fmt.Sprintf("%s, the file of the rows of the master %s, is a directory", shown(name), shown(m.Name)),
			jatext.Join("マスタ", shown(m.Name), "の行のファイル", shown(name), "がディレクトリです"))
		return nil
	}
	t, err := r.csvTable(name, masterColumns(m))
	if err != nil || !t.ok {
		return err
	}
	w := t.where()
	rows := t.rows
	if rows == nil {
		rows = []map[string]string{}
	}
	for _, x := range rows {
		// a file that leaves out the column of a field that may be empty holds none in it, as one
		// that writes the column with its cells empty
		for f := range m.Optional {
			if _, ok := x[f]; !ok {
				x[f] = ""
			}
		}
		w.keys = append(w.keys, masterKey(x, m.Key))
	}
	d.Masters[m.Name] = rows
	d.where.masters[m.Name] = w
	return nil
}

// orgTable is a table of the organization's data as a file holds it: each row its cells by column,
// with the line it starts on.
type orgTable struct {
	file string
	// array is the array of organization.json the table is; "" for a CSV file.
	array string
	rows  []map[string]string
	lines []int
	// ok is false when a problem was found in the table (said).
	ok bool
}

// where answers the table's tableWhere, with no rows' keys yet.
func (t *orgTable) where() *tableWhere {
	return &tableWhere{file: t.file, array: t.array, lines: t.lines}
}

// columnList is a list of columns as a sentence says it: "`id`, `name`", "`id`、`name`".
func columnList(cols []orgColumn, needed bool) (en, ja string) {
	var ens, jas []string
	for _, c := range cols {
		if c.needed || !needed {
			e, j := c.said()
			ens, jas = append(ens, e), append(jas, j)
		}
	}
	return strings.Join(ens, ", "), strings.Join(jas, "、")
}

// said is the column as a problem names it: "`label`", or with the other name it may be written
// in, "`label` (or `名前`)" and "`label`（または `名前`）".
func (c orgColumn) said() (en, ja string) {
	if c.written == "" {
		return "`" + c.name + "`", "`" + c.name + "`"
	}
	return fmt.Sprintf("`%s` (or %s)", c.name, shown(c.written)), jatext.Join("`"+c.name+"`（または", shown(c.written), "）")
}

// csvTable reads a CSV file of the organization's data, whose first line names the columns: a
// problem for a file that is not UTF-8 or does not read as CSV, for a column missing, there twice
// or not one of the file's, for a row of another number of values than there are columns, and for a
// row longer than maxRowBytes or of more than maxRowValues values (csvLimit), whose values are not
// read. A line with nothing but spaces is no row, as ritsu reads one. A cell is read without the
// spaces around it.
func (r *orgReading) csvTable(name string, cols []orgColumn) (*orgTable, error) {
	b, err := fs.ReadFile(r.fsys, name)
	if err != nil {
		return nil, &fs.PathError{Op: "read", Path: name, Err: err}
	}
	t := &orgTable{file: name, ok: true}
	if !r.utf8(name, b) {
		t.ok = false
		return t, nil
	}
	cr := csv.NewReader(&csvLimit{r: bytes.NewReader(bytes.TrimPrefix(b, byteOrderMark)), line: 1, at: 1})
	cr.FieldsPerRecord = -1
	// a quote after spaces opens a quoted cell, as ritsu reads it
	cr.TrimLeadingSpace = true
	var head []string
	for {
		if r.full() {
			t.ok = false
			return t, nil
		}
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		var large *rowTooLarge
		if errors.As(err, &large) {
			t.ok = false
			if large.values {
				r.add(fileAt(name, large.line), fmt.Sprintf("The row has more than %d values, the most a row may have", maxRowValues),
					fmt.Sprintf("この行の値は%d個を超えます。一つの行の値は%d個までです", maxRowValues, maxRowValues))
			} else {
				r.add(fileAt(name, large.line), fmt.Sprintf("The row is longer than %d bytes, the most a row may be", maxRowBytes),
					fmt.Sprintf("この行は%dバイトを超えます。一つの行は%dバイトまでです", maxRowBytes, maxRowBytes))
			}
			return t, nil
		}
		if err != nil {
			t.ok = false
			r.csvError(name, err)
			return t, nil
		}
		line, _ := cr.FieldPos(0)
		if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
			continue
		}
		if head == nil {
			head = make([]string, len(rec))
			as := make([]string, len(rec))
			for i, c := range rec {
				head[i] = strings.TrimSpace(c)
				as[i] = head[i]
				// a column written by its other name is read by its name
				if j := slices.IndexFunc(cols, func(col orgColumn) bool { return col.written != "" && col.written == head[i] }); j >= 0 {
					head[i] = cols[j].name
				}
			}
			if !r.columns(name, line, as, head, cols) {
				t.ok = false
			}
			continue
		}
		if len(rec) != len(head) {
			t.ok = false
			r.add(fileAt(name, line), fmt.Sprintf("The row has %d values, and there are %d columns", len(rec), len(head)),
				fmt.Sprintf("この行の値は%d個で、列は%d個です", len(rec), len(head)))
			continue
		}
		row := make(map[string]string, len(head))
		for i, c := range head {
			row[c] = strings.TrimSpace(rec[i])
		}
		t.rows = append(t.rows, row)
		t.lines = append(t.lines, line)
	}
	if head == nil {
		t.ok = false
		en, ja := columnList(cols, true)
		r.add(fileAt(name, 1), fmt.Sprintf("The file is empty: its first line names its columns (the columns needed are %s)", en),
			fmt.Sprintf("ファイルが空です。最初の行に列の名前を書きます（要る列は %s）", ja))
	}
	return t, nil
}

// utf8 says a file that is not UTF-8 at the line it first is not, and reports whether it is.
func (r *orgReading) utf8(name string, b []byte) bool {
	if utf8.Valid(b) {
		return true
	}
	at := 0
	for at < len(b) {
		c, size := utf8.DecodeRune(b[at:])
		if c == utf8.RuneError && size == 1 {
			break
		}
		at += size
	}
	line := 1 + bytes.Count(b[:at], []byte{'\n'})
	r.add(fileAt(name, line), "The file is not written in UTF-8: save it as UTF-8",
		"ファイルが UTF-8 で書かれていません。UTF-8 で保存してください")
	return false
}

// byteOrderMark is the UTF-8 byte order mark a file may start with, which is not read.
var byteOrderMark = []byte("\xef\xbb\xbf")

// csvLimit is a CSV file as encoding/csv reads it, which fails at a row longer than maxRowBytes or of
// more than maxRowValues values before the row is read into its values: encoding/csv makes every
// value of a row before it answers the row, at tens of bytes for each value. A row ends at a line
// break outside quotes; a quote opens or closes a quoted value (the two quotes of a quote within one
// close and open it again). A file whose quotes encoding/csv refuses fails there first.
type csvLimit struct {
	r io.Reader
	// line is the line the row being read starts on, and at the line being read.
	line, at      int
	bytes, commas int
	quoted        bool
	err           error
}

// rowTooLarge is the error of csvLimit: the row at line is longer than maxRowBytes, or has more
// values than maxRowValues.
type rowTooLarge struct {
	line   int
	values bool
}

func (e *rowTooLarge) Error() string {
	if e.values {
		return fmt.Sprintf("the row at line %d has more than %d values", e.line, maxRowValues)
	}
	return fmt.Sprintf("the row at line %d is longer than %d bytes", e.line, maxRowBytes)
}

func (l *csvLimit) Read(p []byte) (int, error) {
	if l.err != nil {
		return 0, l.err
	}
	n, err := l.r.Read(p)
	for i, c := range p[:n] {
		switch c {
		case '"':
			l.quoted = !l.quoted
		case ',':
			if !l.quoted {
				l.commas++
			}
		case '\n':
			l.at++
			if !l.quoted {
				l.line, l.bytes, l.commas = l.at, 0, 0
				continue
			}
		}
		l.bytes++
		if l.bytes > maxRowBytes || l.commas >= maxRowValues {
			l.err = &rowTooLarge{line: l.line, values: l.commas >= maxRowValues}
			return i, l.err
		}
	}
	return n, err
}

// csvError says a CSV file that does not read, at the line of the row it does not read in.
func (r *orgReading) csvError(name string, err error) {
	line, en, ja := 0, err.Error(), err.Error()
	var pe *csv.ParseError
	if errors.As(err, &pe) {
		line, en, ja = pe.StartLine, pe.Err.Error(), pe.Err.Error()
		switch {
		case errors.Is(pe.Err, csv.ErrQuote):
			en = "a quote `\"` is not closed, or more follows it in its cell"
			ja = "引用符 `\"` が閉じていないか、閉じたあとに文字が続いています"
		case errors.Is(pe.Err, csv.ErrBareQuote):
			en = "a quote `\"` is inside a cell not written in quotes"
			ja = "引用符で囲んでいない値の中に `\"` があります"
		}
	}
	r.add(fileAt(name, line), "The file does not read as CSV: "+en, jatext.Join("CSV として読めません。", ja))
}

// columns says what is wrong with the columns of a file, at the line that names them: a column it
// needs and does not have, one it has twice (a field's column written by its name and by its other
// name too, which is not read either way), and one that is not one of its columns. as is the line
// as the file writes it, and head the same line with each other name read as its column's name. It
// reports whether there is nothing wrong.
func (r *orgReading) columns(name string, line int, as, head []string, cols []orgColumn) bool {
	at, ok := fileAt(name, line), true
	var missing, missingJa []string
	for _, c := range cols {
		if c.needed && !slices.Contains(head, c.name) {
			e, j := c.said()
			missing, missingJa = append(missing, e), append(missingJa, j)
		}
	}
	if len(missing) > 0 {
		ok = false
		en, ja := columnList(cols, true)
		said := "The column %s is missing (the columns needed are %s)"
		if len(missing) > 1 {
			said = "The columns %s are missing (the columns needed are %s)"
		}
		r.add(at, fmt.Sprintf(said, strings.Join(missing, ", "), en),
			jatext.Join("列", strings.Join(missingJa, "、"), "がありません（要る列は", ja, "）"))
	}
	seen := map[string]int{} // each column read, at its first place in the line
	for i, h := range head {
		if first, twice := seen[h]; twice {
			ok = false
			if as[first] != as[i] {
				r.add(at, fmt.Sprintf("The columns %s and %s are the same field, %s: keep one of them", shown(as[first]), shown(as[i]), shown(h)),
					jatext.Join("列", shown(as[first]), "と", shown(as[i]), "は、同じフィールド", shown(h), "です。どちらか一つにしてください"))
				continue
			}
			en, ja := shown(h), shown(h)
			if i := slices.IndexFunc(cols, func(c orgColumn) bool { return c.name == h }); i >= 0 {
				en, ja = cols[i].said()
			}
			r.add(at, fmt.Sprintf("The column %s is there twice", en), jatext.Join("列", ja, "が二つあります"))
			continue
		}
		seen[h] = i
		if !slices.ContainsFunc(cols, func(c orgColumn) bool { return c.name == h }) {
			ok = false
			en, ja := columnList(cols, false)
			r.add(at, fmt.Sprintf("%s is not a column of this file (the columns read are %s)", shown(h), en),
				jatext.Join(shown(h), "は、このファイルの列ではありません（読む列は", ja, "）"))
		}
	}
	return ok
}

// jsonVersion reads a version's organization.json: nil when a problem is found in it (said).
func (r *orgReading) jsonVersion(dir, day string) (*OrgVersion, *versionWhere, error) {
	name := path.Join(dir, versionJSON)
	b, err := fs.ReadFile(r.fsys, name)
	if err != nil {
		return nil, nil, &fs.PathError{Op: "read", Path: name, Err: err}
	}
	if !r.utf8(name, b) {
		return nil, nil, nil
	}
	// a byte order mark at the start is not read, as in a CSV file; its line is the first still
	tables := r.jsonTables(name, bytes.TrimPrefix(b, byteOrderMark))
	if tables == nil {
		return nil, nil, nil
	}
	return r.versionOf(dir, day, tables["teams"], tables["people"], tables["members"])
}

// orgArray is an array of organization.json, with the keys of its objects.
type orgArray struct {
	key  string
	cols []orgColumn
}

var orgArrays = []orgArray{{"teams", teamColumns}, {"people", personColumns}, {"members", memberColumns}}

// jsonTables reads the arrays of organization.json, each as a table whose rows start on the lines
// their objects start on: nil when a problem is found in the file (said). A key of the file's object
// other than the arrays' is a problem, as a key of an object of an array is (one written otherwise,
// "People", would leave out what it holds); a key an object of an array leaves out is empty, as a
// cell left empty, and null is empty too.
func (r *orgReading) jsonTables(name string, b []byte) map[string]*orgTable {
	before := len(r.problems)
	lines := &lineCounter{b: b}
	// the file's value is read whole first, for the line of a syntax error: a decoder that has
	// answered tokens leaves their bytes out of the offset of an error
	dec := json.NewDecoder(bytes.NewReader(b))
	var top json.RawMessage
	if err := dec.Decode(&top); err != nil {
		r.jsonError(name, lines, err)
		return nil
	}
	if rest := bytes.TrimLeft(b[dec.InputOffset():], " \t\r\n"); len(rest) > 0 {
		r.add(fileAt(name, lines.at(int64(len(b)-len(rest)))), "More follows the JSON object of the file",
			"ファイルの JSON のオブジェクトのあとに、ほかの物が続いています")
		return nil
	}
	if top[0] != '{' {
		r.add(fileAt(name, 1), "The file is not a JSON object with the arrays `teams`, `people` and `members`",
			"ファイルが、配列 `teams`、`people`、`members` を持つ JSON のオブジェクトではありません")
		return nil
	}
	// the object starts after the spaces before it; its JSON read whole once, its tokens read too
	topAt := int64(len(b) - len(bytes.TrimLeft(b, " \t\r\n")))
	dec = json.NewDecoder(bytes.NewReader(top))
	if _, err := dec.Token(); err != nil {
		r.unreadable(name, err)
		return nil
	}
	out := map[string]*orgTable{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			r.unreadable(name, err)
			return nil
		}
		key, _ := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			r.unreadable(name, err)
			return nil
		}
		start := topAt + dec.InputOffset() - int64(len(raw))
		i := slices.IndexFunc(orgArrays, func(a orgArray) bool { return a.key == key })
		if i < 0 {
			r.add(fileAt(name, lines.at(start)), fmt.Sprintf("%s is not a key of the file (the keys read are `teams`, `people` and `members`)", shown(key)),
				jatext.Join(shown(key), "は、このファイルのキーではありません（読むキーは `teams`、`people`、`members`）"))
			continue
		}
		if out[key] != nil {
			r.add(fileAt(name, lines.at(start)), fmt.Sprintf("`%s` is there twice", key), jatext.Join("`"+key+"`", "が二つあります"))
			continue
		}
		t := &orgTable{file: name, array: key, ok: true}
		out[key] = t
		r.jsonArray(t, orgArrays[i].cols, raw, start, lines)
	}
	for _, a := range orgArrays {
		if out[a.key] == nil {
			r.add(fileAt(name, 1), fmt.Sprintf("The file has no array `%s`", a.key), jatext.Join("配列", "`"+a.key+"`", "がありません"))
		}
	}
	if len(r.problems) > before {
		return nil
	}
	return out
}

// jsonArray reads an array of organization.json, which starts at the offset start of the file.
func (r *orgReading) jsonArray(t *orgTable, cols []orgColumn, raw []byte, start int64, lines *lineCounter) {
	if raw[0] != '[' {
		t.ok = false
		r.add(fileAt(t.file, lines.at(start)), fmt.Sprintf("`%s` is not an array", t.array), jatext.Join("`"+t.array+"`", "が配列ではありません"))
		return
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if _, err := dec.Token(); err != nil {
		t.ok = false
		r.unreadable(t.file, err)
		return
	}
	for i := 0; dec.More(); i++ {
		if r.full() {
			t.ok = false
			return
		}
		var el json.RawMessage
		if err := dec.Decode(&el); err != nil {
			t.ok = false
			r.unreadable(t.file, err)
			return
		}
		line := lines.at(start + dec.InputOffset() - int64(len(el)))
		t.lines = append(t.lines, line)
		row, ok := r.jsonRow(t, i, el, cols)
		t.rows = append(t.rows, row)
		t.ok = t.ok && ok
	}
}

// jsonRow reads an object of an array of organization.json as a row: a value of a key is a string,
// or null for empty; `primary` may be true or false too.
func (r *orgReading) jsonRow(t *orgTable, i int, el []byte, cols []orgColumn) (map[string]string, bool) {
	place := fmt.Sprintf("%s[%d]", t.array, i)
	at := fileAt(t.file, t.lines[i])
	if el[0] != '{' {
		r.add(at, fmt.Sprintf("`%s` is not an object", place), jatext.Join("`"+place+"`", "がオブジェクトではありません"))
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(el))
	if _, err := dec.Token(); err != nil {
		r.unreadable(t.file, err)
		return nil, false
	}
	row, ok, seen := map[string]string{}, true, map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			r.unreadable(t.file, err)
			return nil, false
		}
		key, _ := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			r.unreadable(t.file, err)
			return nil, false
		}
		if seen[key] {
			ok = false
			r.add(at, fmt.Sprintf("`%s` has the key %s twice", place, shown(key)), jatext.Join("`"+place+"`", "にキー", shown(key), "が二つあります"))
			continue
		}
		seen[key] = true
		if !slices.ContainsFunc(cols, func(c orgColumn) bool { return c.name == key }) {
			ok = false
			en, ja := columnList(cols, false)
			r.add(at, fmt.Sprintf("%s is not a key of `%s` (the keys read are %s)", shown(key), t.array, en),
				jatext.Join(shown(key), "は", "`"+t.array+"`", "のキーではありません（読むキーは", ja, "）"))
			continue
		}
		switch v[0] {
		case '"':
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				r.unreadable(t.file, err)
				return nil, false
			}
			row[key] = s
		case 'n':
			row[key] = ""
		case 't', 'f':
			if key == "primary" {
				row[key] = "no"
				if v[0] == 't' {
					row[key] = "yes"
				}
				continue
			}
			ok = false
			r.jsonType(at, place+"."+key, string(v), "`"+string(v)+"`")
		default:
			ok = false
			if key == "primary" {
				r.primary(t, i, shown(string(v)))
				continue
			}
			what, whatJa := "a number", "数値"
			switch v[0] {
			case '[':
				what, whatJa = "an array", "配列"
			case '{':
				what, whatJa = "an object", "オブジェクト"
			}
			r.jsonType(at, place+"."+key, what, whatJa)
		}
	}
	// a key left out is empty, as a cell left empty; a string of `primary` other than yes and no is
	// said with the version's other rows (versionOf)
	for _, c := range cols {
		if _, ok := row[c.name]; !ok {
			row[c.name] = ""
		}
	}
	return row, ok
}

// jsonType says a value of organization.json that is not a string or null.
func (r *orgReading) jsonType(at, place, what, whatJa string) {
	r.add(at, fmt.Sprintf("%s is %s, and is to be a string or null", shown(place), what),
		jatext.Join(shown(place), "が", whatJa, "です。文字列か null にしてください"))
}

// jsonError says a file that does not read as JSON, at the line it stops reading at: the line of the
// byte a syntax error is at, the last for a file that ends too soon.
func (r *orgReading) jsonError(name string, lines *lineCounter, err error) {
	var se *json.SyntaxError
	switch {
	case errors.As(err, &se):
		r.add(fileAt(name, lines.at(se.Offset-1)), "The file does not read as JSON: "+err.Error(),
			jatext.Join("JSON として読めません（", err.Error(), "）"))
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		r.add(fileAt(name, lines.at(int64(len(lines.b)))), "The file does not read as JSON: it ends before its JSON does",
			"JSON として読めません。JSON の途中でファイルが終わっています")
	default:
		r.unreadable(name, err)
	}
}

// unreadable says a file that does not read as JSON, with no line: a value read whole once does not
// fail when its tokens are read, and is said rather than left out if it does.
func (r *orgReading) unreadable(name string, err error) {
	r.add(fileAt(name, 0), "The file does not read as JSON: "+err.Error(), jatext.Join("JSON として読めません（", err.Error(), "）"))
}

// lineCounter answers the line of an offset in a text, counting on from the offset it was asked
// last.
type lineCounter struct {
	b    []byte
	pos  int
	line int
}

func (l *lineCounter) at(off int64) int {
	n := int(min(max(off, 0), int64(len(l.b))))
	if l.line == 0 || n < l.pos {
		l.pos, l.line = 0, 1
	}
	l.line += bytes.Count(l.b[l.pos:n], []byte{'\n'})
	l.pos = n
	return l.line
}

// isOrgDay reports whether s is a day of the organization's data: YYYY-MM-DD, from 0001-01-01
// through 9999-12-31.
func isOrgDay(s string) bool {
	_, ok := orgDay(s)
	return ok
}

// orgDay reads a day of the organization's data, written YYYY-MM-DD from 0001-01-01 through
// 9999-12-31, as its number of days from 1970-01-01. time.Parse reads the year 0000 too, which is
// no day.
func orgDay(s string) (int64, bool) {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return 0, false
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil || t.Year() < 1 {
		return 0, false
	}
	return t.Unix() / 86400, true
}

// dayText writes a number of days from 1970-01-01 as a day, YYYY-MM-DD.
func dayText(n int64) string {
	return time.Unix(n*86400, 0).UTC().Format(time.DateOnly)
}
