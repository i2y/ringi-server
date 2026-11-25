package page

import (
	"bytes"
	"html/template"
	"strconv"
	"time"
)

// The administration's pages (the framework's DESIGN 13.7): the versions of the kinds and their
// switches, the organization's data and its imports, and the record. The server (package web) makes
// what is on them from what the administration's operations answer; these pages say it in the
// page's language, each name and value of the data in an element of its own direction (<bdi>).

// The administration's pages an Admin can be.
const (
	AdminHomePage    = "admin"
	AdminKindsPage   = "admin-kinds"
	AdminOrgPage     = "admin-organization"
	AdminDraftPage   = "admin-draft"
	AdminRecordsPage = "admin-records"
)

// Admin is what one of the administration's pages is made of.
type Admin struct {
	Language string
	What     string // AdminHomePage, AdminKindsPage, AdminOrgPage, AdminDraftPage or AdminRecordsPage
	// Done says what a post did, at the head; Refused why a post was not taken (a change made since
	// the page was read); Problem what was wrong with a post; Problems the problems of the
	// organization's data, each at its file and line.
	Done, Refused, Problem string
	Problems               []AdminProblem
	// MoreProblems is how many problems of the data were found past Problems, and Stopped says the
	// checks stopped before they found all.
	MoreProblems int
	Stopped      bool
	Now          time.Time // when the page is shown: a post's key reads it
	// Back is the address of the administration's start page: a link under the head leads back to
	// it on a page that is not it; "" shows none.
	Back    string
	Home    *AdminParts
	Kinds   []AdminKind
	Org     *AdminOrg
	Draft   *AdminDraft
	Records *AdminRecords
}

// AdminParts are the parts of the administration the person may use, each by its address ("" for
// one they may not).
type AdminParts struct {
	Kinds, Organization, Records string
}

// AdminProblem is a problem of the organization's data: where it is (a file and its line), and what.
type AdminProblem struct {
	At, Text string
}

// AdminKind is a kind as the page of the versions shows it.
type AdminKind struct {
	Name, Title, Owner string
	Current            int // the version a new request takes now; 0 for none
	Versions           []AdminVersion
}

// AdminVersion is a version of a kind, with its form when the person may switch it.
type AdminVersion struct {
	Version                   int
	Owner                     string
	From, Through             string // the days it may be used on; "" for no bound
	FirstDay                  string // the first day it may be switched to start on: today, or From when later
	Start                     string // how it starts: "description", "now", "day" or "never"
	StartDay                  string // the day it starts on; "" for none
	DecidedBy, DecidedAt, Why string // the record that decided the start; "" for none
	Open, All                 int    // its tickets
	Hidden                    bool   // the person may not read its lines: the record and the tickets are not shown
	Action                    string // the address its form posts to; "" when the person may not switch it
	Number                    int64  // the start's number, which the form sends
	Description               string // the day its description writes; "" for none
}

// AdminOrg is the organization's page: the latest snapshot, and the ways to check new data.
type AdminOrg struct {
	Latest     string // the latest snapshot's ID; "" for none
	ImportedAt string
	ImportedBy string // who imported it; "" for the server at start
	Versions   []AdminOrgVersion
	Source     bool   // whether the server has a source to check
	Action     string // the address the checks post to
	MaxUpload  int64
}

// AdminOrgVersion is a version of the latest snapshot.
type AdminOrgVersion struct {
	Day, When     string // When: "past", "now" or "later"
	People, Teams int
}

// AdminDraft is a draft checked, with its difference from the latest snapshot.
type AdminDraft struct {
	ID, Latest, Digest, Source, At string
	Same                           bool
	// LocksOut says the import would leave the person unable to import the organization's data:
	// the page says so, and the import asks them to tick that they know it.
	LocksOut      bool
	Days          []AdminDay
	Proxies, Rows AdminCount
	Action        string // the address the import posts to
}

// AdminDay is a day of the difference: a version added, removed, changed or the same, with what
// changes on it.
type AdminDay struct {
	Day, When, Change       string
	People, Teams, Members  AdminCount
	Added, Removed, Changed []string // the people, by ID
}

// AdminCount is how many were added, removed and changed.
type AdminCount struct {
	Added, Removed, Changed int
}

// AdminRecords is the page of the record: its filters, its lines, and the ways on.
type AdminRecords struct {
	Action                               string // the address of the page, which the filters' form is sent to
	From, To, Kind, What, Person, Ticket string
	Kinds                                []string // the kinds one may choose
	Lines                                []AdminLine
	Older, CSV                           string // the addresses of the older lines and of the CSV; Older "" for none
}

// AdminLine is a line of the record.
type AdminLine struct {
	At, What, Ticket, Kind, Person, Via, Event, Reason string
	Version                                            int
	History                                            []AdminHistoryLine
	// Body is the values the move's event sent (a form's), as JSON on one line; Before and After
	// are what a record changed. "" for none.
	Body, Before, After string
}

// AdminHistoryLine is a line of the history a move made: what was done, at which stage, by whom,
// and what they said.
type AdminHistoryLine struct {
	Did, Stage, By, Comment string
}

// adminView is what the template writes of an administration's page.
type adminView struct {
	Admin
	W      words
	Lang   string
	Title  string
	At     string
	Assets string
}

// AdminPage makes one of the administration's pages.
func (p *Pages) AdminPage(in Admin) (Page, error) {
	w := words{in.Language}
	v := adminView{Admin: in, W: w, Lang: in.Language, Title: w.adminTitle(in.What), At: in.Now.UTC().Format(time.RFC3339Nano), Assets: p.base}
	var body, head bytes.Buffer
	if err := p.tmpl.ExecuteTemplate(&body, "admin", v); err != nil {
		return Page{}, err
	}
	if err := p.tmpl.ExecuteTemplate(&head, "head", &view{Assets: p.base}); err != nil {
		return Page{}, err
	}
	return Page{Lang: in.Language, Title: v.Title, Head: template.HTML(head.String()), Body: template.HTML(body.String())}, nil
}

func (w words) adminTitle(what string) string {
	switch what {
	case AdminKindsPage:
		return w.t("Versions of the request types", "申請の種類の版")
	case AdminOrgPage:
		return w.t("The organization's data", "組織のデータ")
	case AdminDraftPage:
		return w.t("New data of the organization", "組織の新しいデータ")
	case AdminRecordsPage:
		return w.t("The record", "記録")
	}
	return w.t("Administration", "管理")
}

// The problems of a post of the administration's pages that a person can make with the pages' own
// forms, which a page says in its language (AdminSays): the server's own words of them are for
// the API.
const (
	AdminBadDay      = "bad-day"      // the day does not read as YYYY-MM-DD
	AdminPastDay     = "past-day"     // the day is before today; args: today
	AdminUnusableDay = "unusable-day" // the version may not be used on the day; args: its first and last days ("" for none)
	AdminLongReason  = "long-reason"  // the reason is too long; args: its characters
	AdminReasonChar  = "reason-char"  // the reason has a character it cannot keep (control, format, a separator of lines); args: the character, as U+XXXX
	AdminLargeFile   = "large-file"   // the file is larger than the server takes; args: the most, as SizeWords writes it
	AdminNoFile      = "no-file"      // no file was chosen
	AdminManyImports = "many-imports" // the imports of a day are taken; args: the minutes to wait ("" for none)
	AdminBusy        = "busy"         // the server is checking as much data as it checks at once
)

// AdminSays answers a problem of a post of the administration's pages (AdminBadDay and the rest)
// in the language ("ja", else English).
func AdminSays(lang, problem string, args ...string) string {
	w := words{lang}
	arg := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	switch problem {
	case AdminBadDay:
		return w.t("Write the day as YYYY-MM-DD, as 2027-04-01.", "日は 2027-04-01 のように YYYY-MM-DD の形で書いてください。")
	case AdminPastDay:
		return w.t("The day is before today, "+arg(0)+".", "日が今日（"+arg(0)+"）より前です。")
	case AdminUnusableDay:
		switch from, through := arg(0), arg(1); {
		case from != "" && through != "":
			return w.t("The version may be used from "+from+" through "+through+", and the day is not one of those days.",
				"この版を使える日は "+from+" から "+through+" までで、その日は入っていません。")
		case from != "":
			return w.t("The version may be used from "+from+" on, and the day is before that.",
				"この版を使える日は "+from+" からで、その日は入っていません。")
		case through != "":
			return w.t("The version may be used through "+through+", and the day is after that.",
				"この版を使える日は "+through+" までで、その日は入っていません。")
		}
		return w.t("The version may not be used on the day.", "この版は、その日には使えません。")
	case AdminLongReason:
		return w.t("A reason has at most 500 characters, and this one has "+arg(0)+".", "理由は500字までです。この理由は"+arg(0)+"字あります。")
	case AdminReasonChar:
		return w.t("The reason has a character that cannot be kept ("+arg(0)+"): take it out.", "理由に残せない文字（"+arg(0)+"）があります。取り除いてください。")
	case AdminLargeFile:
		return w.t("The file is larger than the "+arg(0)+" the server takes.", "ファイルが、サーバーの受け取る "+arg(0)+" より大きいです。")
	case AdminNoFile:
		return w.t("Choose a zip file of the organization's data to check.", "確かめる組織のデータの zip ファイルを選んでください。")
	case AdminBusy:
		return w.t("The server is checking other data now: try again in a moment.", "サーバーはいま、ほかのデータを確かめています。少しあとで、もう一度確かめてください。")
	case AdminManyImports:
		switch arg(0) {
		case "":
			return w.t("The server takes no more imports of new data for now.", "今は新しいデータをこれ以上取り込めません。")
		case "1":
			return w.t("The server takes no more imports of new data for now: try again in a minute.",
				"今は新しいデータをこれ以上取り込めません。1分たってから、もう一度取り込んでください。")
		}
		return w.t("The server takes no more imports of new data for now: try again in "+arg(0)+" minutes.",
			"今は新しいデータをこれ以上取り込めません。"+arg(0)+"分たってから、もう一度取り込んでください。")
	}
	return problem
}

func (w words) AdminLink() string { return w.t("Administration", "管理") }
func (w words) AdminNone() string {
	return w.t("Nothing here is yours to do.", "ここでできることはありません。")
}
func (w words) AdminKindsLink() string {
	return w.t("Switch the versions of the request types", "申請の種類の版を切り替える")
}
func (w words) AdminOrgLink() string {
	return w.t("Import the organization's data", "組織のデータを取り込む")
}
func (w words) AdminRecordsLink() string { return w.t("Read the record", "記録を読む") }
func (w words) AdminNoKinds() string {
	return w.t("No request type has a version you may switch.", "切り替えられる版のある申請の種類はありません。")
}
func (w words) AdminOwner() string   { return w.t("Owner", "持ち主") }
func (w words) AdminCurrent() string { return w.t("New requests take", "新しい申請が使う版") }
func (w words) AdminNoCurrent() string {
	return w.t("none: no version is in effect", "なし（効いている版がありません）")
}
func (w words) AdminVersion() string { return w.t("Version", "版") }
func (w words) AdminUsable() string  { return w.t("May be used", "使える日") }
func (w words) AdminStart() string   { return w.t("Starts", "始まり") }
func (w words) AdminDecided() string { return w.t("Decided", "決めた人") }
func (w words) AdminTickets() string {
	return w.t("Tickets (open / all)", "チケット（進行中／全部）")
}
func (w words) AdminSwitch() string { return w.t("Switch", "切り替える") }
func (w words) AdminNow() string    { return w.t("Now", "今すぐ") }
func (w words) AdminOnDay() string  { return w.t("From a day", "日付から") }
func (w words) AdminTheDay() string { return w.t("The day", "日付") }
func (w words) AdminNever() string {
	return w.t("Never: new requests do not take it", "使わない（新しい申請はこの版を使いません）")
}
func (w words) AdminReason() string     { return w.t("Reason", "理由") }
func (w words) AdminReasonHint() string { return w.t("At most 500 characters.", "500字まで。") }
func (w words) AdminNoBound() string    { return "—" }
func (w words) AdminNotOwned() string   { return w.t("(no owner)", "（持ち主なし）") }
func (w words) AdminByStart() string {
	return w.t("the server, at start", "サーバー（起動のとき）")
}
func (w words) AdminHow(how, day string) string {
	switch how {
	case "now":
		return w.t("from "+day+" (switched to now)", day+" から（今すぐに切り替えた）")
	case "day":
		return w.t("from "+day, day+" から")
	case "never":
		return w.t("never", "使わない")
	}
	if day == "" {
		return w.t("as its description says: from the beginning", "説明のとおり（初めから）")
	}
	return w.t("as its description says: from "+day, "説明のとおり（"+day+" から）")
}
func (w words) AdminDescription(day string) string {
	if day == "" {
		return w.t("As its description says (from the beginning)", "説明のとおり（初めから）")
	}
	return w.t("As its description says (from "+day+")", "説明のとおり（"+day+" から）")
}
func (w words) AdminVersionLabel(n int) string {
	return w.t("Version "+strconv.Itoa(n), "版 "+strconv.Itoa(n))
}
func (w words) AdminLatest() string { return w.t("The latest data", "今のデータ") }
func (w words) AdminNoLatest() string {
	return w.t("No data is imported yet.", "まだ取り込んだデータがありません。")
}
func (w words) AdminImported() string   { return w.t("Imported", "取り込んだ日時") }
func (w words) AdminImportedBy() string { return w.t("Imported by", "取り込んだ人") }
func (w words) AdminDay() string        { return w.t("Day", "日") }
func (w words) AdminWhen() string       { return w.t("In effect", "効く時期") }
func (w words) AdminPeople() string     { return w.t("People", "人") }
func (w words) AdminTeams() string      { return w.t("Teams", "チーム") }
func (w words) AdminMembers() string    { return w.t("Memberships", "所属") }
func (w words) AdminWhenWords(when string) string {
	switch when {
	case "past":
		return w.t("past", "過去")
	case "later":
		return w.t("later", "これから")
	}
	return w.t("now", "今")
}
func (w words) AdminCheck() string { return w.t("Check new data", "新しいデータを確かめる") }
func (w words) AdminCheckSource() string {
	return w.t("Check the data the server reads at start", "起動のときに読むデータを確かめる")
}
func (w words) AdminUpload() string {
	return w.t("The data, as a zip file", "データ（zip ファイル）")
}
func (w words) AdminUploadHint(max string) string {
	return w.t("The organization's directory and the masters' files, from the project's root, at most "+max+".",
		"プロジェクトの根からの、組織のディレクトリとマスタのファイル。"+max+" まで。")
}
func (w words) AdminCheckUpload() string { return w.t("Check the file", "ファイルを確かめる") }
func (w words) AdminProblems() string {
	return w.t("The data does not pass its checks:", "データが確かめを通りませんでした。")
}
func (w words) AdminMoreProblems(n int) string {
	if n == 1 {
		return w.t("And 1 more problem.", "ほかに問題が1件あります。")
	}
	return w.t("And "+strconv.Itoa(n)+" more problems.", "ほかに問題が"+strconv.Itoa(n)+"件あります。")
}
func (w words) AdminStopped() string {
	return w.t("The checks stopped there, and the data may have more problems: check its files with `ritsu ringi check`.",
		"問題が多いので、そこで確かめを止めました。ほかにも問題があるかもしれません。ファイルを `ritsu ringi check` で確かめてください。")
}
func (w words) AdminDraftOf() string { return w.t("Checked", "確かめた日時") }
func (w words) AdminSource(source string) string {
	if source == "upload" {
		return w.t("a file uploaded", "上げたファイル")
	}
	return w.t("the data the server reads at start", "起動のときに読むデータ")
}
func (w words) AdminFrom() string { return w.t("From", "読んだ所") }
func (w words) AdminSame() string {
	return w.t("This is the latest data: nothing differs, and there is nothing to import.", "今のデータと同じです。違う所が無いので、取り込む物はありません。")
}
func (w words) AdminChange(change string) string {
	switch change {
	case "added":
		return w.t("a version added", "版を足す")
	case "removed":
		return w.t("a version removed", "版を除く")
	case "changed":
		return w.t("changed", "変わる")
	}
	return w.t("the same", "同じ")
}
func (w words) AdminCounts(c AdminCount) string {
	return w.t(strconv.Itoa(c.Added)+" added, "+strconv.Itoa(c.Removed)+" removed, "+strconv.Itoa(c.Changed)+" changed",
		"足す "+strconv.Itoa(c.Added)+"、除く "+strconv.Itoa(c.Removed)+"、変わる "+strconv.Itoa(c.Changed))
}
func (w words) AdminProxies() string  { return w.t("Proxies", "代理") }
func (w words) AdminRows() string     { return w.t("Rows of the masters", "マスタの行") }
func (w words) AdminAddedP() string   { return w.t("People added:", "足す人:") }
func (w words) AdminRemovedP() string { return w.t("People removed:", "除く人:") }
func (w words) AdminChangedP() string { return w.t("People changed:", "変わる人:") }
func (w words) AdminImport() string   { return w.t("Import", "取り込む") }
func (w words) AdminLocksOut() string {
	return w.t("After this import, the organization in effect today does not let you import the organization's data: you could not check or import data again, nor undo this import yourself.",
		"取り込むと、今日の組織では、あなたは組織のデータを取り込めなくなります。自分でデータを確かめ直すことも、この取り込みを元に戻すこともできなくなります。")
}
func (w words) AdminLocksOutYes() string {
	return w.t("Import it knowing this", "承知のうえで取り込む")
}
func (w words) AdminNotShown() template.HTML {
	return template.HTML(w.t("Before you switch or import, <code>ritsu ringi diff</code> says what a version of a request type changes, and whom new data makes an approver.",
		"申請の種類の版で変わる所と、新しいデータで承認者が変わる人は、切り替えや取り込みの前に <code>ritsu ringi diff</code> で読めます。"))
}
func (w words) AdminFilter() string  { return w.t("Show", "絞り込む") }
func (w words) AdminFromDay() string { return w.t("From the day", "この日から") }
func (w words) AdminToDay() string   { return w.t("To the day", "この日まで") }
func (w words) AdminKind() string    { return w.t("Request type", "申請の種類") }
func (w words) AdminAny() string     { return w.t("Any", "すべて") }
func (w words) AdminWhat() string    { return w.t("What", "種類") }
func (w words) AdminPerson() string  { return w.t("Person (ID)", "人（ID）") }
func (w words) AdminTicket() string  { return w.t("Ticket (ID)", "チケット（ID）") }
func (w words) AdminAt() string      { return w.t("When", "日時") }
func (w words) AdminWho() string     { return w.t("Who", "人") }
func (w words) AdminWay() string     { return w.t("Way in", "入口") }
func (w words) AdminDetail() string  { return w.t("What was done", "したこと") }
func (w words) AdminNoLines() string { return w.t("No lines.", "行はありません。") }
func (w words) AdminOlder() string   { return w.t("Show older lines", "もっと前の行を見る") }
func (w words) AdminCSV() string {
	return w.t("Download these lines as CSV", "この行を CSV で落とす")
}
func (w words) AdminWhatWords(what string) string {
	switch what {
	case "move":
		return w.t("a move of a ticket", "チケットの動き")
	case "kind":
		return w.t("a switch of a version", "版の切り替え")
	case "organization":
		return w.t("an import of the organization's data", "組織のデータの取り込み")
	}
	return w.AdminAny()
}
func (w words) AdminValues() string { return w.t("Values sent", "送った値") }
func (w words) AdminBefore() string { return w.t("Before", "前") }
func (w words) AdminAfter() string  { return w.t("After", "後") }
