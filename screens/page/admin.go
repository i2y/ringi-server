package page

import (
	"bytes"
	"html/template"
	"strconv"
	"strings"
	"time"
)

// The administration's pages (the framework's DESIGN 13.7, 15.8): the versions of the kinds and
// their switches, the organization's data and its imports, the record, and the bundles that add a
// version or a kind to the running server. The server (package web) makes
// what is on them from what the administration's operations answer; these pages say it in the
// page's language, each name and value of the data in an element of its own direction (<bdi>).

// The administration's pages an Admin can be.
const (
	AdminHomePage    = "admin"
	AdminKindsPage   = "admin-kinds"
	AdminOrgPage     = "admin-organization"
	AdminDraftPage   = "admin-draft"
	AdminRecordsPage = "admin-records"
	AdminBundlesPage = "admin-bundles"
	AdminBundlePage  = "admin-bundle"
)

// Admin is what one of the administration's pages is made of.
type Admin struct {
	Language string
	What     string // AdminHomePage, AdminKindsPage, AdminOrgPage, AdminDraftPage, AdminRecordsPage, AdminBundlesPage or AdminBundlePage
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
	Bundles *AdminBundles
	Bundle  *AdminBundle
}

// AdminParts are the parts of the administration the person may use, each by its address ("" for
// one they may not).
type AdminParts struct {
	Kinds, Organization, Records, Bundles string
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
	Added, Removed, Changed []AdminPerson
}

// AdminPerson is a person of a day of the difference: their ID, their name in the page's language
// (from the draft, or from the snapshot for one removed), and, for one changed, the fields of theirs
// that change (id, name, name_ja, email, language).
type AdminPerson struct {
	ID, Name string
	Fields   []string
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

// AdminBundles is the page of the bundles: those the person may read, the newest first, and the form
// that uploads one.
type AdminBundles struct {
	List      []AdminBundleRow
	Upload    string // the address the upload posts to; "" when the person may not upload
	Signed    bool   // the server takes only signed bundles: the form asks for the signature too
	MaxUpload int64
	Hidden    bool   // the list shows the hidden bundles too
	Toggle    string // the address of the list with the hidden bundles, or without them
}

// AdminBundleRow is a bundle in the list.
type AdminBundleRow struct {
	Link, Short, State     string
	Hidden                 bool
	Adds                   []AdminBundleVersion
	UploadedBy, UploadedAt string
}

// AdminBundleVersion is a version a bundle adds or holds.
type AdminBundleVersion struct {
	Name, Title string
	Version     int
	New         bool // a kind the server did not have
}

// AdminBundle is the page of a bundle: what it is, what each version it adds changes, and the forms
// that decide and hide it.
type AdminBundle struct {
	Digest, Short, State, Source string
	Hidden, Signed               bool
	Size                         int64
	Vectors, Worked              int
	UploadedBy, UploadedAt       string
	DecidedBy, DecidedAt, Why    string // the decision; "" for none
	HidBy, HidAt, HidWhy         string // the hiding; "" for none
	Kinds                        []AdminBundleKind
	Holds                        []AdminBundleVersion
	// Forgotten says the server forgot the archive (a bundle hidden before it was approved): what
	// it changed is not shown.
	Forgotten bool
	// APIChanges says a version it adds changes the API: the approval asks the person to tick that
	// they checked it. Difference is the digest of the difference shown, which the approval sends.
	APIChanges bool
	Difference string
	Action     string // the address the decisions and the hiding post to
	MayDecide  bool
	Uploader   bool // the person uploaded it: another decides it
	MayHide    bool
}

// AdminBundleKind is a version a bundle adds, with its difference from the version below it.
type AdminBundleKind struct {
	Name, Title, Owner string
	Version, Against   int // Against: the version it is compared with; 0 for none
	New, APIChanges    bool
	StartsDay          string // the day its description starts it on; "" for as soon as it is approved
	From, Through      string // the days it may be used on; "" for no bound
	Lines              []AdminDiffLine
	// Said is what the bundle itself says the version changes (ritsu's `ringi diff`), against the
	// version SaidFrom; "" for nothing.
	Said     string
	SaidFrom int
}

// AdminDiffLine is a line of a version's difference: its part (form, stages, approvers, usable, api,
// other), what changes, and whether it changes the API.
type AdminDiffLine struct {
	Section, Text string
	API           bool
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
	case AdminBundlesPage:
		return w.t("Bundles of request types", "申請の種類のバンドル")
	case AdminBundlePage:
		return w.t("A bundle", "バンドル")
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
	AdminNoBundle    = "no-bundle"    // no bundle was chosen
	AdminBundleBusy  = "bundle-busy"  // the server is checking another bundle
	AdminNotYours    = "not-yours"    // the bundle holds a version the person may not upload
	AdminAPIUnticked = "api-unticked" // the bundle changes the API, and its approval does not say it was checked
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
	case AdminNoBundle:
		return w.t("Choose the zip file of a bundle to upload.", "アップロードするバンドルの zip ファイルを選んでください。")
	case AdminBundleBusy:
		return w.t("The server is checking another bundle now: try again in a moment.", "サーバーはいま、ほかのバンドルを確かめています。少しあとで、もう一度アップロードしてください。")
	case AdminNotYours:
		return w.t("The bundle holds a version of a request type you may not upload.", "このバンドルには、あなたがアップロードできない申請の種類の版が入っています。")
	case AdminAPIUnticked:
		return w.t("This bundle changes the API: tick that you checked it, and approve it again.", "このバンドルは API の形を変えます。確かめたことに印を付けて、もう一度承認してください。")
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
func (w words) AdminAddedP() string   { return w.t("People added: ", "足す人：") }
func (w words) AdminRemovedP() string { return w.t("People removed: ", "除く人：") }
func (w words) AdminChangedP() string { return w.t("People changed: ", "変わる人：") }

// AdminSep is what parts the people of a line of the difference.
func (w words) AdminSep() string { return w.t(", ", "、") }

// AdminPersonText is a person of the difference by name and ID ("Alice (e1201)", 「アリス（e1201）」),
// with what changes of one changed ("Alice (e1201): email", 「アリス（e1201）：メールアドレス」); a
// person whose data has no name is said by ID.
func (w words) AdminPersonText(p AdminPerson) string {
	s := p.ID
	if p.Name != "" && p.Name != p.ID {
		s = p.Name + w.t(" ("+p.ID+")", "（"+p.ID+"）")
	}
	if len(p.Fields) > 0 {
		var fields []string
		for _, f := range p.Fields {
			fields = append(fields, w.adminField(f))
		}
		s += w.t(": ", "：") + strings.Join(fields, w.AdminSep())
	}
	return s
}

// adminField says a field of a person's data.
func (w words) adminField(f string) string {
	switch f {
	case "id":
		return "ID"
	case "name":
		return w.t("name", "名前")
	case "name_ja":
		return w.t("name in Japanese", "日本語の名前")
	case "email":
		return w.t("email address", "メールアドレス")
	case "language":
		return w.t("language", "言語")
	}
	return f
}
func (w words) AdminImport() string { return w.t("Import", "取り込む") }
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
	case "bundle":
		return w.t("a bundle", "バンドル")
	}
	return w.AdminAny()
}
func (w words) AdminValues() string { return w.t("Values sent", "送った値") }
func (w words) AdminBefore() string { return w.t("Before", "前") }
func (w words) AdminAfter() string  { return w.t("After", "後") }

func (w words) AdminBundlesLink() string {
	return w.t("Add a version or a request type with a bundle", "バンドルで版や申請の種類を足す")
}
func (w words) AdminNoBundles() string {
	return w.t("No bundles.", "バンドルはありません。")
}
func (w words) AdminBundleState(state string, hidden bool) string {
	var s string
	switch state {
	case "pending":
		s = w.t("Waiting to be decided", "決めるのを待っている")
	case "approved":
		s = w.t("Approved: served", "承認済み（使っている）")
	case "declined":
		s = w.t("Declined", "却下")
	default:
		s = state
	}
	if hidden {
		s += w.t(" (hidden)", "（隠した）")
	}
	return s
}
func (w words) AdminBundle() string   { return w.t("Bundle", "バンドル") }
func (w words) AdminState() string    { return w.t("State", "状態") }
func (w words) AdminAdds() string     { return w.t("Adds", "足す版") }
func (w words) AdminUploaded() string { return w.t("Uploaded", "アップロード") }
func (w words) AdminDigest() string {
	return w.t("Digest (SHA-256)", "ダイジェスト（SHA-256）")
}
func (w words) AdminSize() string      { return w.t("Size", "大きさ") }
func (w words) AdminSignature() string { return w.t("Signature", "署名") }
func (w words) AdminReceived() string  { return w.t("Received", "受け取り方") }
func (w words) AdminChecks() string    { return w.t("Checks", "確かめ") }
func (w words) AdminHiddenBy() string  { return w.t("Hidden", "隠した人") }
func (w words) AdminNewKind() string   { return w.t("new", "新しい種類") }
func (w words) AdminShowHidden() string {
	return w.t("Show the hidden bundles too", "隠したバンドルも見る")
}
func (w words) AdminLeaveHidden() string {
	return w.t("Show the bundles without the hidden", "隠したバンドルを除いて見る")
}
func (w words) AdminVersionOf(name string, version int, isNew bool) string {
	s := name + " " + w.AdminVersionLabel(version)
	if isNew {
		s += w.t(" (new)", "（新しい種類）")
	}
	return s
}
func (w words) AdminUploadBundle() string {
	return w.t("Upload a bundle", "バンドルをアップロードする")
}
func (w words) AdminBundleFile() string {
	return w.t("The bundle: the zip file `ritsu gen --target requests --extra wasm --extra bundle` writes", "バンドル（`ritsu gen --target requests --extra wasm --extra bundle` が書く zip）")
}
func (w words) AdminBundleFileHint(max string) string {
	return w.t("At most "+max+". The server checks it, and another administrator decides it.", max+"まで。サーバーが確かめ、ほかの管理者が決めます。")
}
func (w words) AdminSignatureFile() string {
	return w.t("Its signature: the .sig file `ringi-bundle sign` writes", "署名（`ringi-bundle sign` が書く .sig のファイル）")
}
func (w words) AdminSignedOnly() string {
	return w.t("This server takes only signed bundles.", "このサーバーは、署名したバンドルだけを受け取ります。")
}
func (w words) AdminUploadAndCheck() string {
	return w.t("Upload and check", "アップロードして確かめる")
}
func (w words) AdminSignedWords(signed bool) string {
	if signed {
		return w.t("Signed with a key the server takes", "サーバーが受け取る鍵で署名してある")
	}
	return w.t("Not signed", "署名なし")
}
func (w words) AdminReceivedWords(source string) string {
	if source == "start" {
		return w.t("Read from the server's directory of bundles as it started", "起動のときに、サーバーのバンドルのディレクトリから読んだ")
	}
	return w.t("Uploaded", "アップロード")
}
func (w words) AdminChecksWords(vectors, worked int) string {
	return w.t(strconv.Itoa(vectors)+" vectors of the events and "+strconv.Itoa(worked)+" of the forms ran on its code, and every one agreed",
		"コードでベクタを流しました。出来事の "+strconv.Itoa(vectors)+" 本とフォームの "+strconv.Itoa(worked)+" 本が、すべて合いました")
}
func (w words) AdminAgainst(against int, isNew bool) string {
	switch {
	case isNew:
		return w.t("A new request type: the server has no version of it.", "新しい申請の種類です。サーバーには、この種類の版がありません。")
	case against == 0:
		return w.t("The server has no version of it below this one.", "サーバーには、これより前の版がありません。")
	}
	return w.t("What changes from version "+strconv.Itoa(against)+":", "版 "+strconv.Itoa(against)+" から変わる所：")
}
func (w words) AdminNoDifference() string {
	return w.t("Nothing differs in the form, the stages, the approvers, the days it may be used or the API.", "フォーム、ステップ、承認者、使える日、API に違いはありません。")
}
func (w words) AdminSection(section string) string {
	switch section {
	case "form":
		return w.t("Form", "フォーム")
	case "stages":
		return w.t("Stages", "ステップ")
	case "approvers":
		return w.t("Approvers", "承認者")
	case "usable":
		return w.t("Days it may be used", "使える日")
	case "api":
		return "API"
	}
	return w.t("Other", "そのほか")
}
func (w words) AdminAPILine() string { return w.t("changes the API", "API が変わる") }
func (w words) AdminStartsWords(day string) string {
	if day == "" {
		return w.t("Once approved, new requests take it at once (an administrator may switch it after).", "承認すると、新しい申請がすぐに使います（あとから切り替えられます）。")
	}
	return w.t("Once approved, new requests take it from "+day+" (an administrator may switch it after).", "承認すると、新しい申請が "+day+" から使います（あとから切り替えられます）。")
}
func (w words) AdminSaid(from int) string {
	return w.t("The text the bundle held (written by `ritsu ringi diff` against version "+strconv.Itoa(from)+")",
		"バンドルに入っていた文（`ritsu ringi diff` が版 "+strconv.Itoa(from)+" と比べて書いた物）")
}
func (w words) AdminHolds() string {
	return w.t("It holds these besides, as the server has them:", "ほかに次の版を持っています（サーバーにある物と同じ）：")
}
func (w words) AdminForgotten() string {
	return w.t("The server forgot the bundle's contents when it was hidden: what it changed is not shown.", "隠したときに、サーバーはバンドルの中身を忘れました。差は出せません。")
}
func (w words) AdminAPIChanges() string {
	return w.t("This bundle changes the API: programs that call these operations may need changing.", "このバンドルは API の形を変えます。この操作を呼ぶプログラムは、直す必要があるかもしれません。")
}
func (w words) AdminAPIChangesYes() string {
	return w.t("I checked that the API changes", "API が変わることを確かめた")
}
func (w words) AdminDecide() string {
	return w.t("Approve or decline", "承認するか却下するか")
}
func (w words) AdminApprove() string { return w.t("Approve", "承認する") }
func (w words) AdminDecline() string { return w.t("Decline", "却下する") }
func (w words) AdminUploader() string {
	return w.t("You uploaded this bundle: another administrator decides it.", "このバンドルはあなたがアップロードしました。決めるのは、ほかの管理者です。")
}
func (w words) AdminHideBundle() string { return w.t("Hide the bundle", "バンドルを隠す") }
func (w words) AdminHide() string       { return w.t("Hide", "隠す") }
func (w words) AdminHideHint(state string) string {
	if state == "approved" {
		return w.t("A hidden bundle is not listed. It is still served for its requests; hide it once no new request takes its versions (each switched to never).",
			"隠したバンドルは一覧に出ません。その申請のために使い続けます。隠せるのは、足した版をどれも「使わない」に切り替えたあとです。")
	}
	return w.t("A hidden bundle is not listed, and the server forgets its contents.", "隠したバンドルは一覧に出ず、サーバーはその中身を忘れます。")
}
