package page_test

import (
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/i2y/ringi-server/screens"
	"github.com/i2y/ringi-server/screens/page"
)

// A new expense request, shown and posted as a browser with no script posts it.
func Example() {
	set, err := screens.Load(os.DirFS("../testdata/requests"))
	if err != nil {
		panic(err)
	}
	pages, err := page.New(set, page.Options{Assets: "/assets/"})
	if err != nil {
		panic(err)
	}
	in := page.Form{
		Form:   screens.Form{Kind: "expense", Language: "en", Surface: "f1", MaySubmit: true, Can: []string{"submit"}},
		Action: "/requests/expense",
		Now:    time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC),
	}
	pg, err := pages.Form(in)
	if err != nil {
		panic(err)
	}
	fmt.Println(pg.Title)

	// what the browser posts (the page's hidden fields, what the person typed, the button)
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
	if err != nil {
		panic(err)
	}
	fmt.Println(rd.Operation.Method, rd.Operation.Path)
	fmt.Println(string(rd.Operation.Body))
	// Output:
	// Expense request
	// POST /requests/expense/tickets
	// {"requester":null,"form":{"category":"travel","items":[{"price":12800,"what":"Train to Yokohama and back"},{"price":3200,"what":"Lunch with the customer"}],"purpose":"A visit to a customer","trip_to":"Yokohama","used_on":"2026-12-02"},"comment":null}
}
