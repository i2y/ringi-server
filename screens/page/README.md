# The plain HTML pages (screens/page)

The plain HTML pages of a ringi server's requests: the request form, the approval card, the lists of
tickets and the history, for any browser, with JavaScript or without. The package makes the pages,
and turns what the browser posts from them into operations of the API. It also makes what is around
them: the start page, the bar at the top of each page, and the notices.

It is built on the package `screens`, and uses the same data, the same names, the same operations
and keys, and the same sentences of a refusal as the A2UI screens. It depends on the standard
library alone and keeps nothing, and every kind of request runs through the same code. [The
design](DESIGN.md) says how it works, and why. Japanese: [README.ja.md](README.ja.md). Package
[`web`](../../web/README.md) serves the pages over HTTP, so a server that uses `web` need not call
this package itself.

## Use

```go
set, err := screens.Load(sub)                              // as for the A2UI screens
pages, err := page.New(set, page.Options{Assets: "/assets/"}) // serve pages.Asset(name) there
```

This shows a new expense request's form, and then reads what a browser with no script posts from it.
It is in [example_test.go](example_test.go), and `go test` runs it:

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

It prints

```
POST /requests/expense/tickets
{"requester":null,"form":{"category":"travel","items":[{"price":12800,"what":"Train to Yokohama and back"},{"price":3200,"what":"Lunch with the customer"}],"purpose":"A visit to a customer","trip_to":"Yokohama","used_on":"2026-12-02"},"comment":null}
```

The amounts are as people type them (`12,800`, `¥3200`), the day is `2026/12/2`, and the purpose has
spaces around it. The body is what ritsu's `functions.js` sends for the same values on the A2UI form
(a test compares the two). The server runs `rd.Operation` for the person, as it runs the same
operation sent over HTTP.

When the reading stops a post (`rd.Operation == nil`), or the API refuses the operation
(`rd.Refused(refusal)`, with the `screens.Refusal` the API returned), the server shows the page again
with `Again: rd`: the values as typed, and what is wrong beside each part, a refusal of a value
beside the field its place names. To a post of Enter pressed in a field (`rd.Stay()`) it returns 204
No Content, and the browser keeps the page as it is. DESIGN.md, section 1, has the whole of a
server's handler.

In the page, the price of the first row is

```html
<div class="field number" id="field-in-items-0-price" data-field="items.price" data-type="number" data-num="1" data-den="1" data-decimals="0" data-format="{} JPY" data-code="JPY" data-yen="true" data-required="true">
<label for="in-items-0-price">Price <span class="required">required</span></label>
<span class="amount"><input type="text" inputmode="decimal" autocomplete="off" id="in-items-0-price" name="f.items.0.price" value="" aria-describedby="checks-in-items-0-price" aria-required="true" /> <span class="unit">JPY</span></span>
<ul class="checks" id="checks-in-items-0-price"><li data-check="items.price.required" hidden="hidden">Fill in Price (Items).</li><li data-check="items.price.range" hidden="hidden">Enter Price (Items) from 1 JPY to 1,000,000 JPY.</li><li data-read="number" hidden="hidden">Enter Price (Items) as a number.</li><li data-read="step" hidden="hidden">Enter Price (Items) as a whole number.</li></ul>
```

and in Japanese the same part has `金額`, `円` and `明細の金額は1円から1,000,000円までにしてください。`.
The checks are the A2UI screens' (`data-check`, in the same words) and the page's own reading of a
number (`data-read`). Without the script, the server shows the checks that fail when it shows the
page again. With it, `page.js` shows them as the person types, and calls ritsu's `functions.js` as
the A2UI screens do. A button whose stage needs a reason checks the comment in its own words ("Write
the reason in the comment."); an empty comment, or one of white space alone, does not pass.

`Form.Worked` and `Card.Worked` give the page the kind's `Worked` entry (a server gives it
`Server.Worked`). The page gives the entry the form, with the approver's edits where the ticket has
them, and is made by what the entry returns. A field shown on a condition that does not hold is in
the page hidden, with its controls disabled so that nothing of it is sent. A field required on a
condition has its required mark and help while the condition holds. A field the form works out shows
its value. When a worked value is outside its range, the check names the fields it is worked out
from, for the person to fix one of them ("Change Price (Items)."). On a card, the values leave out a
hidden field and include the worked ones. All of this works without the script; a page whose entry
is nil, or a version with none, shows the form as it is before it is filled in. With the script,
`page.js` does it again as the person types (`data-shown`, `data-required-when`).

A text and a comment are read without the white space at their ends (Unicode's White_Space: the 25
code points `unicode.IsSpace` knows, which `functions.js` and `page.js` take off as well). Where a
text a person wrote is shown to be read (a value on a card, a comment in the history), each control
character but a tab and the ends of lines is shown as its picture (U+001C as ␜, U+007F as ␡), so that
none hides in a name or a reason. A part that holds the text to send it again holds it as it is.

The pages load `page.css`, `page.js` and the set's `functions.js` from `Options.Assets`, and have no
inline script or style. They work under `Content-Security-Policy: default-src 'none';
script-src 'self'; style-src 'self'; form-action 'self'`.

Around the screens:

- `pages.Home` makes the start page: what waits for the person, the requests they may make, and their
  own requests with how many are in each state. Each kind is a link, with a few of its tickets under
  it. An entry of `Home.New` whose kind has no version in effect yet shows the day it may be requested
  from (`Entry.From`, which is what `Server.Offered` returns) and has no link, as the kind has no form
  to show.
- `pages.Bar` makes the bar a server puts at the top of each page: the start page, who is signed in,
  the other languages, and signing out.
- `pages.Notice` makes a page with one message, in place of the page asked for: "Not found", "Not
  signed in", "The form could not be read", "Too large to send", "The server failed".
- `pages.Offered` makes the page that says from which day a kind may be requested, in the words of the
  API's `unoffered` refusal and of the A2UI screen (`screens.OfferedWords`).

DESIGN.md, section 3.4, has them.

## Tests

```sh
go test ./...        # in screens/: about 9 seconds
go test -race ./...  # about 95 seconds
```

The tests use the test data of `screens` (`../testdata/`). They make 9,516 pages from the tickets of
the vectors, and the start page, the lists' headings and columns, the bar and each notice in both
languages. They post each person's event of the 2,990 lines on its page as a browser without the
script does (2,244 posts, and 454 that add or remove a row), and give what the page sends to the code
ritsu generates: each of the 2,070 sent has the result its vector says, and the kind refuses each
post the page's checks stop and each line that asks for what no page sends.

In Node, when there is one, they compare `page.js` with the Go reading, the amounts it works out (996)
with the kinds', and 1,208 posted forms with what `functions.js` sends; the kind refuses each of the
12 that the page sends and `functions.js` stops, at the place of the check that stops it. Without
Node that test prints a `SKIP:` line (`go test -v` shows it) and passes.

Each page they read back is checked for what a person with a keyboard or a screen reader needs (one
`<main>`, where the focus goes, what a search found in a line of its own, the words of a kind shown in
another language marked with it), and the colours of `page.css` for the contrast WCAG 2.2 asks for, in
light and in dark. DESIGN.md, section 7, lists the tests.
