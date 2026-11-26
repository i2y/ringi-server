// Package page shows the screens of a kind of request as plain HTML (ringi's DESIGN 13.6): the
// request form, the approval card, the list and the history, for any browser, with JavaScript or
// without. It reads the forms a browser posts from them into the operations of the API.
//
// It is built on package screens, which fills each screen's data (Set.Form, Set.Card, Set.List,
// Set.History, names through the same Lookup). A posted form becomes an action, which screens makes
// into the operation and its idempotency key (Set.Operation). It keeps nothing and asks no one.
//
// Package web uses it. README.md shows how to call it, and DESIGN.md says how it works.
package page

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"maps"
	"mime/multipart"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/i2y/ringi-server/screens"
)

var (
	// ErrDescription is a kind's description a page cannot be made from: a number type it cannot
	// read exactly, a choice of nothing it knows.
	ErrDescription = errors.New("page: a description the pages cannot be made from")
	// ErrPost is a post that is not one of the page it is read with: no surface or a surface of
	// another screen, a button the page does not have, a time or a revision that cannot be read.
	ErrPost = errors.New("page: not a post of the page")
)

// The names of the hidden fields every form holds.
const (
	FieldToken    = "_csrf"     // the token against cross-site requests, which the server checks
	FieldSurface  = "_surface"  // the surface of the screen
	FieldAt       = "_at"       // when the page was shown: the time of the operation's key
	FieldRevision = "_revision" // the ticket's revision the page shows, as JSON
	FieldDo       = "_do"       // the button pressed
)

// DoStay is the value of the button a form presses when a person presses Enter in it: the first in
// the form, which no one sees. Its post is answered 204 No Content (Reading.Stay), and the browser
// keeps the page as it is, so that Enter neither sends the request nor removes a row, nor moves the
// focus.
const DoStay = "stay"

//go:embed files/page.css files/page.js files/page.html files/admin.html
var files embed.FS

// Options is how a server serves the pages.
type Options struct {
	// Assets is the address the pages load their files from, ending in "/": page.css, page.js and
	// the set's functions.js, which page.js imports from beside it ("/assets/").
	Assets string
}

// Pages are the plain HTML screens of a set. They do not change after New, and are safe to use
// from many goroutines.
type Pages struct {
	set    *screens.Set
	kinds  map[string]*kind
	assets map[string]screens.Asset
	base   string
	tmpl   *template.Template
}

// kind is what the pages read of a kind of request.
type kind struct {
	name    string
	d       *description
	buttons map[string]map[string]screens.Button // screen → button ID → button
}

// New reads the descriptions of the set's kinds. It refuses one it cannot make pages from
// (ErrDescription).
func New(set *screens.Set, o Options) (*Pages, error) {
	if o.Assets == "" || !strings.HasSuffix(o.Assets, "/") {
		return nil, fmt.Errorf("page: Options.Assets is the address the files are served at, ending in \"/\" (it is %q)", o.Assets)
	}
	p := &Pages{set: set, kinds: map[string]*kind{}, assets: map[string]screens.Asset{}, base: o.Assets}
	for _, k := range set.Kinds() {
		raw, err := set.Description(k.Name)
		if err != nil {
			return nil, err
		}
		d, err := readDescription(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: types/%s.json: %v", ErrDescription, k.Name, err)
		}
		kp := &kind{name: k.Name, d: d, buttons: map[string]map[string]screens.Button{}}
		for _, sc := range []string{"form", "card", "list", "history"} {
			bs, err := set.Buttons(k.Name, sc)
			if err != nil {
				return nil, err
			}
			kp.buttons[sc] = map[string]screens.Button{}
			for _, b := range bs {
				kp.buttons[sc][b.ID] = b
			}
		}
		p.kinds[k.Name] = kp
	}
	for _, f := range []struct{ name, kind string }{{"page.css", "text/css; charset=utf-8"}, {"page.js", "text/javascript; charset=utf-8"}} {
		b, err := files.ReadFile("files/" + f.name)
		if err != nil {
			return nil, err
		}
		p.assets[f.name] = asset(f.name, f.kind, b)
	}
	fn, err := set.Asset("functions.js")
	if err != nil {
		return nil, err
	}
	p.assets["functions.js"] = fn
	t, err := template.New("page").Funcs(template.FuncMap{"part": partOf, "rowpart": rowOf, "described": describedBy, "size": sizeWords,
		"list": func(xs ...string) []string { return xs }}).ParseFS(files, "files/page.html", "files/admin.html")
	if err != nil {
		return nil, err
	}
	p.tmpl = t
	return p, nil
}

func asset(name, contentType string, body []byte) screens.Asset {
	sum := sha256.Sum256(body)
	return screens.Asset{Name: name, ContentType: contentType, Body: body, ETag: `"` + hex.EncodeToString(sum[:]) + `"`}
}

// WithAssets answers the pages with their files loaded from another address, ending in "/": a
// server with several packages serves each package's files at an address of its own, so that its
// pages load its functions.js. The pages are the same otherwise; p does not change.
func (p *Pages) WithAssets(base string) (*Pages, error) {
	if base == "" || !strings.HasSuffix(base, "/") {
		return nil, fmt.Errorf("page: the address the files are served at ends in \"/\" (it is %q)", base)
	}
	c := *p
	c.base = base
	return &c, nil
}

// WithStyle answers the pages with rules added at the end of their page.css: a server's own look,
// such as the colours of its organization (web.Look). The pages are the same otherwise; p does not
// change, and a style given before is replaced.
func (p *Pages) WithStyle(css string) (*Pages, error) {
	b, err := files.ReadFile("files/page.css")
	if err != nil {
		return nil, err
	}
	c := *p
	c.assets = maps.Clone(p.assets)
	if css != "" {
		b = append(append(b, '\n'), css...)
	}
	c.assets["page.css"] = asset("page.css", p.assets["page.css"].ContentType, b)
	return &c, nil
}

// Asset is a file the pages load, by its name: "page.css", "page.js", or the set's "functions.js"
// (fs.ErrNotExist for another). A server serves each at Options.Assets with its ContentType and
// ETag.
func (p *Pages) Asset(name string) (screens.Asset, error) {
	a, ok := p.assets[name]
	if !ok {
		return screens.Asset{}, fmt.Errorf("page: no asset %q (page.css, page.js, functions.js): %w", name, fs.ErrNotExist)
	}
	return a, nil
}

// AssetNames are the names of the files the pages load.
func (p *Pages) AssetNames() []string {
	return slices.Sorted(maps.Keys(p.assets))
}

// Option is a value a picker offers: a person, a team, a row of a master.
type Option struct {
	Value string // the ID or the key, as the API takes it
	Label string // what the page shows: the name with the ID or key ("Sales staff 1 (e1201)")
	Name  string // the name alone; "" for a value with none
}

// Picks are the values the pickers of a page offer, where the server lists them. A person, a team
// or a row of a master with no list is typed by its ID or key, and found by its name where Find
// has a way to find it.
type Picks struct {
	People  []Option            // the people a person's field offers
	Teams   []Option            // the teams a team's field offers
	Masters map[string][]Option // the rows a field of a master offers, by the master's path ("company_masters/accounts")
	// Find holds a way to find the values of each picker the server has too many of to list:
	// "person", "team", or a master's path. It answers the values whose ID or key or name holds
	// every word of the query, the closest first (the value the query is, then a name it is, then
	// those that begin with it), at most limit of them, and whether more hold them. Such a part
	// has a button that finds by what is typed in it; the page shown again lists what was found,
	// as buttons that choose one, and names the value the part holds.
	Find map[string]func(query string, limit int) (found []Option, more bool)
}

// FindShown is the most values a part lists of those found.
const FindShown = 20

// FindLongest is the most characters (code points) the words the script finds by may have: the
// most the API's finds take (server.FindLongest), which answer 400 to more. The script does not ask
// with more, and says so in the page's language.
const FindLongest = 200

// Form is what a request form is made of: the core's form (a new request, or one made again), and
// what the page needs besides.
type Form struct {
	screens.Form
	Action string    // the address the form posts to
	Token  string    // the CSRF token, put in the hidden field _csrf
	Now    time.Time // when the page is shown; the operation's key reads it (required)
	Picks  Picks
	// Back is the address of the page the form leads from: the ticket's page for a request made
	// again, the start page for a new one. A link under the head leads back to it; "" shows none.
	Back  string
	Again *Reading // a post read, shown again with what was typed in it and what was wrong
	// Worked asks what the form works out, as the page shows it (nil: the page shows each field as
	// it would before the form is filled in). A field whose condition does not hold is in the page
	// hidden, with nothing of it sent; one whose condition holds is shown, and asked for; a computed
	// field shows its value. The script decides them again as the person types.
	Worked WorkedFunc
	// FormAt answers the form the ticket had at a revision: ReadForm reads with it a post of a page
	// that showed the ticket before it last changed (nil: no such form is known).
	FormAt FormAtFunc
}

// Card is what an approval card is made of.
type Card struct {
	screens.Card
	Action  string
	Token   string
	Now     time.Time
	Picks   Picks
	History string // the address of the ticket's history, for a link; "" for none
	// Resubmit is the address of the form a request that is back with its requester is made again in,
	// where they may make it again: the card says so, and links to it; "" shows none.
	Resubmit string
	// Back is the address of the start page: a link under the head leads back to it; "" shows none.
	Back  string
	Again *Reading
	// Worked asks what the ticket's form works out (nil, as a Form's): the card's values leave out a
	// field hidden on its condition and show the values the form works out, and the fields approvers
	// edit are shown as the form after the edits decides them.
	Worked WorkedFunc
	// FormAt answers the form the ticket had at a revision, as a Form's: ReadCard reads with it an
	// edit of a page that showed the ticket before it last changed.
	FormAt FormAtFunc
}

// List is what a list of tickets is made of.
type List struct {
	screens.List
	Open string // the address of a ticket's page, with {id} for its ID ("/tickets/{id}"); "" shows no link
	// Of is whose tickets the list shows, which its heading says: ListWaiting, ListMine, ListAll, or
	// "" for the kind's title alone.
	Of string
	// More is the address of the list's next page (the older tickets); "" for none.
	More string
	// Back is the address of the start page: a link under the head leads back to it; "" shows none.
	Back string
}

// The lists a List can be Of.
const (
	ListWaiting = "waiting" // the tickets that wait for the person
	ListMine    = "mine"    // the person's own requests: made by them, or for them
	ListAll     = "all"     // the requests of the kind the person sees for the kind (server.Server.Tickets)
)

// History is what a ticket's history is made of. Each line is shown in the zone of its At.
type History struct {
	screens.History
	Zone *time.Location // the zone the history shows times in, as the card does; nil is UTC
	// Back is the address of the ticket's page: a link under the head leads back to it; "" shows
	// none.
	Back string
}

// Home is what the start page is made of: what waits for the person, the requests they may make,
// and their own requests, each by kind.
type Home struct {
	Language string
	Waiting  []Entry // the kinds of the tickets that wait for the person, with how many
	New      []Entry // the kinds the person may make a request of, and those they may from a day (Entry.From)
	Mine     []Entry // the kinds the person has made requests of
	// All are the kinds whose requests the person sees for the kind, whoever made them (the
	// department that owns a kind, as the examples' gate has it: server.Server.KindsSeen), each a
	// link to the list of all of them (ListAll). The part is left out when there are none.
	All []Entry
	// Admin is the address of the administration's pages, for a person who may use one of them;
	// "" for none.
	Admin string
}

// Entry is a line of the start page: a kind of request, by its title, and the address of its page.
type Entry struct {
	Title string
	Lang  string // the language of Title when it is not the page's ("" when it is)
	// Name is the kind's name ("expense", "expense_ja"). Where a section holds two entries of one
	// title, it is shown after the title so that the kinds are told apart; "" shows none.
	Name  string
	Count int // how many tickets wait (Home.Waiting); not shown elsewhere
	// States is how many of the person's own requests of the kind are in each state
	// ("in_progress": 3), and SentBack how many of those back with their requester
	// ("with_requester") were sent back, the others being taken back. They are said after the title,
	// the ones that wait for the person first ("差し戻し 1・承認待ち 3"; Home.Mine); not shown
	// elsewhere.
	States   map[string]int
	SentBack int
	URL      string
	// Tickets are a few of the kind's tickets (of those that wait, in Home.Waiting; of the person's
	// own, in Home.Mine), each a link to its page under the entry.
	Tickets []EntryTicket
	// From is, for a kind no version of which is in effect now (Home.New), the day the person may
	// make a request of it from (YYYY-MM-DD, as Server.Offered answers it): the line says the day,
	// and has no link (URL is not shown), since the kind has no form to show yet.
	From string
}

// EntryTicket is a ticket an Entry lists: its ID, the address of its page, and what tells it and its
// state apart.
type EntryTicket struct {
	ID     string
	URL    string // the address of its page
	Status string // "in_progress", "with_requester", "approved", …: its words are shown as a badge
	// SentBack is, of a ticket back with its requester (with_requester), whether it was sent back
	// (its sent_back_by names by whom): one its requester took back was not.
	SentBack bool
	// Stage is the stage the ticket is at, in words ("Manager", as Pages.StageIn answers it); "" for
	// none. StageLang is their language when it is not the page's ("" when it is).
	Stage, StageLang string
	// Who is who the line names: the requester of a ticket that waits for the person; of the person's
	// own, who made the request for them, or whom they made it for. "" for none.
	Who string
	// Info is a short line of the request's values that tells it apart (the sum it works out, a
	// day); "" for none.
	Info string
	// Due is the deadline the ticket runs to, in words ("Due 2026-11-30 18:00", as Pages.TicketDue
	// answers it); "" for none.
	Due string
}

// Bar is the bar a server puts at the top of each page it shows (the page's Bar): the way back to
// the start page, who is signed in, the page in its other languages, and signing out. The way back
// is the organization's logo and name when the server has them, and "Home" otherwise.
type Bar struct {
	Language  string
	Home      string // the address of the start page; "" for none
	Name      string // the organization's name, the words of the way back; "" for none
	Logo      string // the address of the organization's logo, before the name; "" for none
	Person    string // the name of the person signed in; "" for none
	Languages []Link // the page in other languages, each named in its own
	SignOut   string // the address the bar's button posts to, to sign out; "" for none
	Token     string // the CSRF token of the sign-out's form, put in the hidden field _csrf
}

// Link is a link: its words, the language they are in, and its address.
type Link struct {
	Text, Lang, URL string
}

// FileRefusal is what a Keeper answers for a file it does not take: one larger than Max bytes
// (TooLarge), one that is not what its type says (Content: a browser sends the type its name
// ends with), one of a person who has TooMany uploaded files that no request holds, as many as the
// server keeps (each deleted Kept after it was sent; 0, not), or one of a type it does not take.
// The page is shown again with the reason at the file's field; a file the field held before stays.
type FileRefusal struct {
	TooLarge bool
	Max      int64
	Content  bool
	TooMany  int
	Kept     time.Duration
}

func (e *FileRefusal) Error() string {
	switch {
	case e.TooLarge:
		return fmt.Sprintf("page: the file is larger than %d bytes", e.Max)
	case e.Content:
		return "page: the file is not of the type it is sent as"
	case e.TooMany > 0:
		return fmt.Sprintf("page: the person has %d uploaded files that no request holds", e.TooMany)
	}
	return "page: the server does not take files of this type"
}

// Page is a screen as HTML.
type Page struct {
	Lang  string        // the page's language: "en", "ja"
	Title string        // the title of the screen, as text
	Head  template.HTML // what the document's head loads: the style sheet and the script
	Bar   template.HTML // the bar a server shows above the screen (Pages.Bar); "" for none
	Body  template.HTML // the screen
}

// Document is the page as a whole HTML document: the bar, and the screen as the document's main
// part (<main>), in the light or the dark colours the person's settings ask for.
func (pg Page) Document() []byte {
	var b bytes.Buffer
	b.WriteString("<!DOCTYPE html>\n<html lang=\"")
	b.WriteString(template.HTMLEscapeString(pg.Lang))
	b.WriteString("\">\n<head>\n<meta charset=\"utf-8\" />\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\" />\n<meta name=\"color-scheme\" content=\"light dark\" />\n<title>")
	b.WriteString(template.HTMLEscapeString(pg.Title))
	b.WriteString("</title>\n")
	b.WriteString(string(pg.Head))
	b.WriteString("</head>\n<body>\n")
	b.WriteString(string(pg.Bar))
	b.WriteString("<main>\n")
	b.WriteString(string(pg.Body))
	b.WriteString("</main>\n</body>\n</html>\n")
	return b.Bytes()
}

// Post is a form a browser posted from a page: the request's PostForm, and with attachments its
// MultipartForm.File.
type Post struct {
	Values url.Values
	Files  map[string][]*multipart.FileHeader
}

// FormAtFunc answers the form the ticket a page shows had at a revision (a server asks
// Server.FormAt); nil for a revision the ticket was never at, or one whose form is not said. What a
// post of a request made again or of an approver's edit changes is of the form its page showed: of
// this one, when the ticket has changed since. So a post sent again after it was taken makes the
// operation it made then (Reading.Sent).
type FormAtFunc func(revision json.RawMessage) (json.RawMessage, error)

// Keeper keeps the files a form attaches.
type Keeper interface {
	// Keep keeps a file a person attaches, and answers the ID the API names it by.
	Keep(file *multipart.FileHeader) (string, error)
}

// KeepFunc is a function as a Keeper, as http.HandlerFunc is a function as a handler: a server
// makes one for each post, for the person who sends it.
type KeepFunc func(file *multipart.FileHeader) (string, error)

// Keep calls f.
func (f KeepFunc) Keep(file *multipart.FileHeader) (string, error) { return f(file) }

// ids is the IDs of the elements a part names in aria-describedby.
func ids(list ...string) string {
	var out []string
	for _, s := range list {
		if s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, " ")
}
