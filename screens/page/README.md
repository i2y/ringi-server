# Pages of plain HTML (screens/page)

Package `page` makes the pages of a ringi server's requests: the form, the page of a request with its
buttons, the lists and the history. They work in any browser, with JavaScript or without. It also makes what
is around them: the start page, the bar at the top of each page, and the pages that say one thing. What a
browser posts from a page it turns into an operation of the API.

It is built on package [`screens`](../README.md). The data, the names, the operations and their keys, and the
sentences of a refusal are those of the A2UI screens. The package depends on the standard library alone and
keeps nothing. Every request type runs through the same code. Japanese: [README.ja.md](README.ja.md).

| To | Read |
|---|---|
| use the pages | [Run it, and approve a request](../../docs/start.md). A server serves them with package [`web`](../../web/README.md), and you call nothing of this package |
| serve the pages from a server of your own, without `web` | [Use](#use), below, and [the design](DESIGN.md), section 1, which has the whole of a server's handler |
| know how it works, and why | [the design](DESIGN.md) |

## Use

```go
set, err := screens.Load(sub)                              // as for the A2UI screens
pages, err := page.New(set, page.Options{Assets: "/assets/"}) // serve pages.Asset(name) there
```

### Show a form, and read what is posted

This shows the form of a new expense request, and then reads what a browser with no script posts from it. It
is in [example_test.go](example_test.go), and `go test` runs it.

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

The amounts are as people type them (`12,800`, `¥3200`), the day is `2026/12/2`, and the purpose has spaces
around it. The body is what ritsu's `functions.js` sends for the same values on the A2UI form, and a test
compares the two. The server does `rd.Operation` for the person, as it does the same operation sent over
HTTP.

### Show the page again

| What happened | What the server does |
|---|---|
| The form could not be read (`rd.Operation == nil`) | Shows the page again with `Again: rd` |
| The API refused the operation | Shows the page again with `Again: rd`, after `rd.Refused(refusal)` with the `screens.Refusal` the API answered |
| Enter was pressed in a field (`rd.Stay()`) | Answers 204 No Content, and the browser keeps the page as it is |

The page shown again has the values as they were typed, and what is wrong beside each part. A refusal of a
value is beside the field its place names.

### What a field looks like

The price of the first row is

```html
<div class="field number" id="field-in-items-0-price" data-field="items.price" data-type="number" data-num="1" data-den="1" data-decimals="0" data-format="{} JPY" data-code="JPY" data-yen="true" data-required="true">
<label for="in-items-0-price">Price <span class="required">required</span></label>
<span class="amount"><input type="text" inputmode="decimal" autocomplete="off" id="in-items-0-price" name="f.items.0.price" value="" aria-describedby="checks-in-items-0-price" aria-required="true" /> <span class="unit">JPY</span></span>
<ul class="checks" id="checks-in-items-0-price"><li data-check="items.price.required" hidden="hidden">Fill in Price (Items).</li><li data-check="items.price.range" hidden="hidden">Enter Price (Items) from 1 JPY to 1,000,000 JPY.</li><li data-read="number" hidden="hidden">Enter Price (Items) as a number.</li><li data-read="step" hidden="hidden">Enter Price (Items) as a whole number.</li></ul>
```

In Japanese the same part has `金額`, `円` and `明細の金額は1円から1,000,000円までにしてください。`.

- `data-check` is a check of the A2UI screen, in the same words. `data-read` is the page's own reading of a
  number.
- Without the script, the checks that fail are shown when the server shows the page again. With it, `page.js`
  shows them as the person types, with ritsu's `functions.js`, as the A2UI screens do.
- A button of a stage that needs a reason checks the comment in its own words ("Write the reason in the
  comment."). An empty comment, or one of white space alone, does not pass.

### Fields that depend on other fields

Give the page the entry `Worked` of the request type, in `Form.Worked` and `Card.Worked`. A server gives it
`Server.Worked`. The page gives the entry the values of the form, and the approver's edits where the request
has them, and is made by what comes back.

| The field | On the page |
|---|---|
| shown on a condition that does not hold | Hidden, with its controls disabled, so that nothing of it is sent. On a card, it is left out of the values |
| required on a condition | Has the mark and the help of a required field while the condition holds |
| worked out by the form | Shows its value. When the value is out of its range, the check names the fields it is worked out from ("Change Price (Items).") |

All of this works without the script. With it, `page.js` does it again as the person types
(`data-shown`, `data-required-when`). When the entry is nil, or a version has none, the form is as it is
before it is filled in.

### Texts

- A text and a comment are read without the white space at their ends (Unicode's White_Space: the 25 code
  points `unicode.IsSpace` knows, which `functions.js` and `page.js` take off as well).
- Where a text a person wrote is shown to be read (a value on a card, a comment in the history), each control
  character but a tab and the ends of lines is shown as its picture (U+001C as ␜, U+007F as ␡), so that none
  hides in a name or a reason. A part that holds the text to send it again holds it as it is.

### The style sheet, the script and the headers

The pages load `page.css`, `page.js` and the set's `functions.js` from `Options.Assets`. They have no inline
script or style, and work under `Content-Security-Policy: default-src 'none'; script-src 'self'; style-src
'self'; form-action 'self'`.

### What is around the pages

| Function | What it makes |
|---|---|
| `pages.Home` | The start page: what waits for the person, the requests they may make, and their own requests with how many are in each state. Each request type is a link, with a few of its requests under it. One with no version in effect yet (`Entry.From`, which is what `Server.Offered` answers) shows the day it may be used from, with no link |
| `pages.Bar` | The bar at the top of each page: the start page, who is signed in, the other languages, and signing out |
| `pages.Notice` | A page that says one thing in place of the page asked for: "Not found", "Not signed in", "The form could not be read", "Too large to send", "The server failed" |
| `pages.Offered` | A page that says from which day a request type may be used, in the words of the API's `unoffered` refusal and of the A2UI screen (`screens.OfferedWords`) |

[The design](DESIGN.md), section 3.4, says more of them.

## The tests

```sh
go test ./...        # in screens/: about 9 seconds
go test -race ./...  # about 95 seconds
```

They use the test data of `screens` (`../testdata/`).

- They make 9,516 pages from the tickets of the vectors, and the start page, the lists' headings and columns,
  the bar and each notice in both languages.
- They post each person's event of the 2,990 lines on its page as a browser without the script does (2,244
  posts, and 454 that add or remove a row), and give what the page sends to the code ritsu made. Each of the
  2,070 sent has the result its vector says. The kind refuses each post the page's checks stop, and each line
  that asks for what no page sends.
- In Node, when there is one, they compare `page.js` with the Go reading, the amounts it works out (996) with
  the kinds', and 1,208 posted forms with what `functions.js` sends. The kind refuses each of the 12 that the
  page sends and `functions.js` stops, at the place of the check that stops it. Without Node that test prints
  a `SKIP:` line (`go test -v` shows it) and passes.
- They check each page they read back for what a person with a keyboard or a screen reader needs: one
  `<main>`, where the focus goes, what a search found in a line of its own, and the words shown in another
  language marked with it.
- They check the colours of `page.css` for the contrast WCAG 2.2 asks for, in light and in dark.

[The design](DESIGN.md), section 7, lists the tests.
