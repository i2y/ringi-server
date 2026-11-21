# page

The plain HTML pages of the requests a server runs: the request form, the approval card, the lists
of tickets and the history, as pages any browser shows, with JavaScript or without, and the reading
of what the browser posts from them into the operations of the API; and around them, the start
page, the bar at the top of each page, and the notices. It is built on the package `screens`: the
same data, the same names, the same operations and keys, and the same sentences of a refusal as
the A2UI screens. It depends on the standard library alone, keeps nothing, and knows no kind of
request. The package `web` serves the pages over HTTP ([../../web/README.md](../../web/README.md)).
[DESIGN.md](DESIGN.md) says how; [README.ja.md](README.ja.md) is this page in Japanese.

## Use

```go
set, err := screens.Load(sub)                              // as for the A2UI screens
pages, err := page.New(set, page.Options{Assets: "/assets/"}) // serve pages.Asset(name) there
```

A new expense request's form, shown and then posted by a browser with no script, from
[example_test.go](example_test.go) (`go test` runs it):

```go
in := page.Form{
	Form:   screens.Form{Kind: "expense", Language: "en", Surface: "f1", MaySubmit: true, Can: []string{"submit"}},
	Action: "/requests/expense",
	Now:    time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC),
}
pg, err := pages.Form(in) // pg.Document() is the HTML page

post := page.Post{Values: url.Values{
	"_surface": {"expense.form.f1"}, "_at": {"2026-11-17T09:00:00Z"}, "_do": {"button.submit"},
	"s.category": {"1"}, "f.category": {"travel"},
	"s.items": {"1"}, "r.items": {"0", "1"},
	"f.items.0.what": {"Train to Yokohama and back"}, "f.items.0.price": {"12,800"},
	"f.items.1.what": {"Lunch with the customer"}, "f.items.1.price": {"¥3200"},
	"s.purpose": {"1"}, "f.purpose": {" A visit to a customer "},
	"s.used_on": {"1"}, "f.used_on": {"2026/12/2"},
	"s.trip_to": {"1"}, "f.trip_to": {"Yokohama"},
	"s.receipt": {"1"},
	"comment":   {""},
}}
in.Surface = "" // a new request's surface is read from the post
rd, err := pages.ReadForm(in, post, nil)
fmt.Println(rd.Operation.Method, rd.Operation.Path)
fmt.Println(string(rd.Operation.Body))
```

prints

```
POST /requests/expense/tickets
{"requester":null,"form":{"category":"travel","items":[{"price":12800,"what":"Train to Yokohama and back"},{"price":3200,"what":"Lunch with the customer"}],"purpose":"A visit to a customer","trip_to":"Yokohama","used_on":"2026-12-02"},"comment":null}
```

The amounts were typed as people type them (`12,800`, `¥3200`), the day as `2026/12/2`, and the
purpose with spaces around it; the body holds what ritsu's `functions.js` sends for the same values
on the A2UI form (the tests hold the two to each other). The server
runs `rd.Operation` as the person, as it runs the same operation sent over HTTP. When the reading
stops a post (`rd.Operation == nil`), or the API refuses the operation (`rd.Refused(refusal)`, with
the `screens.Refusal` the API answered), the server shows the page again with `Again: rd`: the
values as typed, and what is wrong at each part, a refusal of a value at the field its place names.
A post of Enter pressed in a field (`rd.Stay()`) is answered 204 No Content, and the browser keeps
the page as it is. DESIGN.md, section 1, has the whole of a server's handler.

In the page, the price of the first row is

```html
<div class="field number" id="field-in-items-0-price" data-field="items.price" data-type="number" data-num="1" data-den="1" data-decimals="0" data-format="{} JPY" data-code="JPY" data-yen="true" data-required="true">
<label for="in-items-0-price">Price <span class="required">required</span></label>
<span class="amount"><input type="text" inputmode="decimal" autocomplete="off" id="in-items-0-price" name="f.items.0.price" value="" aria-describedby="checks-in-items-0-price" aria-required="true" /> <span class="unit">JPY</span></span>
<ul class="checks" id="checks-in-items-0-price"><li data-check="items.price.required" hidden="hidden">Fill in Price (Items).</li><li data-check="items.price.range" hidden="hidden">Enter Price (Items) from 1 JPY to 1,000,000 JPY.</li><li data-read="number" hidden="hidden">Enter Price (Items) as a number.</li><li data-read="step" hidden="hidden">Enter Price (Items) as a whole number.</li></ul>
```

and in Japanese the same part says `金額`, `円` and `明細の金額は1円から1,000,000円までにしてください。`.
The checks are the A2UI screens' (`data-check`, in the same words) and the page's own reading of a
number (`data-read`). Without the script, the server says the ones that fail when it shows the page
again; with it, `page.js` says them as the person types, and asks ritsu's `functions.js` as the
A2UI screens do. A button whose stage asks for a reason checks the comment with the words of its own
("Write the reason in the comment."), and an empty one, or one of white space alone, fails it.

`Form.Worked` and `Card.Worked` give the page the kind's `Worked` entry (a server gives it
`Server.Worked`): the page asks it what the form works out, with the approver's edits where the
ticket has them, and makes the page as the answer decides. A field shown on a condition that does
not hold is in the page hidden, its controls disabled so that nothing of it is sent; a field asked
for on a condition has its required mark and help while the condition holds; a field the form works
out shows its value; the check of a worked value outside its range names the fields it is worked
out from, for the person to fix one of them ("Change Price (Items)."). On a card, the values leave a
hidden field out and show the worked ones. It is
all decided without the script — a page whose entry is nil, or a version with none, shows the form
as it would be before it is filled in — and with it `page.js` decides again as the person types
(`data-shown`, `data-required-when`).

A text and a comment are read without the white space at their ends (Unicode's White_Space, the 25
code points `unicode.IsSpace` knows, which `functions.js` and `page.js` take off the same), and a
text a person wrote is shown to be read — a value on a card, a comment of the history — with each
control character but a tab and the ends of lines as its picture (U+001C as ␜, U+007F as ␡), so
that none hides in a name or a reason; a part that holds the text to send it again holds it as it
is.

The pages load `page.css`, `page.js` and the set's `functions.js` from `Options.Assets`, and have
no inline script or style: they work under `Content-Security-Policy: default-src 'none';
script-src 'self'; style-src 'self'; form-action 'self'`.

Around the screens, `pages.Home` makes the start page (what waits for the person, the requests they
may make, their own requests, each kind a link), `pages.Bar` the bar a server puts at the top of
each page (the start page, who is signed in, the other languages, signing out), `pages.Notice`
a page that says one thing in place of the page asked for ("Not found", "Not signed in", "The form
could not be read", "Too large to send", "The server failed"), and `pages.Offered` one that says a
kind may be requested from a day, in the words the API's `unoffered` answer and the A2UI screen use
(`screens.OfferedWords`). On the start page, an entry of `Home.New` whose kind has no version in
effect now says that day (`Entry.From`, as `Server.Offered` answers it) and has no link, since the
kind has no form to show yet. DESIGN.md, section 3.4, has them.

## Tests

```sh
go test ./...        # in screens/: about 9 seconds
go test -race ./...  # about 95 seconds
```

The tests read the test set of `screens` (`../testdata/`): they make 9,492 pages from the tickets of
the vectors and the start page, the lists' headings and columns, the bar and each notice in both
languages, post each person's event of the 2,964 lines on its page as a browser without the script
does (2,218 posts, and 454 that add or remove a row) and give what the page sends to the code ritsu
generates: each of the 2,064 sent is answered as the vector answers, and the kind refuses each post
the page's checks stop and each line that asks what no page sends; and, in Node when there is one,
hold page.js to the Go reading, the amounts it works out (996) to the kinds', and 1,208 posted forms
to what `functions.js` sends; the kind refuses each of the 12 the page sends and `functions.js`
stops, at the place of the check that stops it. Without Node that test is skipped with a `SKIP:`
line (`go test -v` shows it) and passes. Each page they read back is held to what a person with a
keyboard or a screen reader needs of it (one `<main>`, where the focus goes, what a search found
said in a line of its own, the words of a kind shown in another language marked with it), and the
colours of `page.css` to the contrast WCAG 2.2 asks for, in light and in dark. DESIGN.md, section 7,
lists them.
