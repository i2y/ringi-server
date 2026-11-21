package examples

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	server "github.com/i2y/ringi-server"
	"github.com/i2y/ringi-server/memstore"
	"github.com/i2y/ringi-server/web"
)

// grownOrg is the example organization with n more people in each version, each one of the staff of
// general affairs, and n more rows of systems: more than a picker lists.
type grownOrg struct {
	server.OrgSource
	n int
}

func (g grownOrg) Read(ctx context.Context, want server.OrgLayout) (*server.OrgData, error) {
	d, err := g.OrgSource.Read(ctx, want)
	if err != nil {
		return nil, err
	}
	for i := range d.Versions {
		for j := 1; j <= g.n; j++ {
			id := fmt.Sprintf("c%04d", j)
			d.Versions[i].People = append(d.Versions[i].People, server.OrgPerson{ID: id, Name: fmt.Sprintf("Clerk %04d", j), NameJa: fmt.Sprintf("事務 %04d", j)})
			d.Versions[i].Members = append(d.Versions[i].Members, server.Member{Person: id, Team: "general_affairs", Position: "staff", Primary: true})
		}
	}
	for j := 1; j <= g.n; j++ {
		d.Masters["company_masters.systems"] = append(d.Masters["company_masters.systems"],
			map[string]string{"id": fmt.Sprintf("s%04d", j), "name": fmt.Sprintf("Tool %04d", j), "owner": "e1001", "valid_from": "2026-01-01", "valid_to": ""})
	}
	return d, nil
}

// found is the answer of the address the pickers find at.
type found struct {
	Found []struct {
		Value, Name, Label string
	}
	More bool
}

// TestPagesFind finds the person and the system of a request of accounts in an organization with
// more people and systems than a picker lists: at the address a renderer's pickers and the pages'
// script find at, and on the pages with no script, by posting the page and choosing one of what
// was found. The request is then sent with the values chosen.
func TestPagesFind(t *testing.T) {
	s := firstSubmit(t, "account")
	_, h := newWeb(t, webSetup{Store: memstore.New(), At: s.at, Config: func(c *server.Config) { c.Organization = grownOrg{Organization(), 1200} }})
	names := people(t)
	person, system := s.form["person"].(string), s.form["system"].(string)
	find := func(path, as string, header ...string) (int, found) {
		t.Helper()
		w := send(h, "GET", path, as, nil, header...)
		var a found
		if w.Code == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
		}
		return w.Code, a
	}

	// the address the pickers find at
	code, a := find(web.FindPath+"/people?q="+url.QueryEscape(names[person][0])+"&lang=en", s.requester)
	if code != http.StatusOK || len(a.Found) == 0 || a.Found[0].Value != person || a.Found[0].Name != names[person][0] || a.Found[0].Label != names[person][0]+" ("+person+")" {
		t.Errorf("%s by the name: %d %+v", person, code, a)
	}
	code, a = find(web.FindPath+"/people?q="+url.QueryEscape(strings.ReplaceAll(names[person][1], " ", "")), s.requester, "Accept-Language", "ja")
	if code != http.StatusOK || len(a.Found) == 0 || a.Found[0].Value != person || a.Found[0].Label != names[person][1]+"（"+person+"）" {
		t.Errorf("%s by the Japanese name, run together: %d %+v", person, code, a)
	}
	code, a = find(web.FindPath+"/people?q=clerk+0123&lang=en", s.requester)
	if code != http.StatusOK || len(a.Found) != 1 || a.Found[0].Value != "c0123" || a.More {
		t.Errorf("clerk 0123: %d %+v", code, a)
	}
	code, a = find(web.FindPath+"/people?q=%E4%BA%8B%E5%8B%99&lang=ja&limit=100", s.requester)
	if code != http.StatusOK || len(a.Found) != 100 || !a.More || a.Found[0].Value != "c0001" {
		t.Errorf("事務, a hundred: %d %d %v", code, len(a.Found), a.More)
	}
	code, a = find(web.FindPath+"/people?lang=en&limit=5", s.requester)
	if code != http.StatusOK || len(a.Found) != 5 || !a.More {
		t.Errorf("no words: %d %+v", code, a)
	}
	code, a = find(web.FindPath+"/masters/company_masters/systems?q=customer&lang=en", s.requester)
	if code != http.StatusOK || len(a.Found) != 1 || a.Found[0].Value != system || a.Found[0].Name != "Customer system" {
		t.Errorf("the system by a part of its name: %d %+v", code, a)
	}
	code, a = find(web.FindPath+"/teams?q=sales&lang=en", s.requester)
	if code != http.StatusOK || len(a.Found) == 0 {
		t.Errorf("a team: %d %+v", code, a)
	}
	for path, want := range map[string]int{
		"people?q=x":                                http.StatusUnauthorized,
		"people?q=x&limit=0":                        http.StatusBadRequest,
		"people?q=x&limit=101":                      http.StatusBadRequest,
		"people?q=x&limit=some":                     http.StatusBadRequest,
		"people?q=x&lang=fr":                        http.StatusBadRequest,
		"people?q=" + strings.Repeat("x", 201):      http.StatusBadRequest,
		"people?q=%FF":                              http.StatusBadRequest,
		"rooms?q=x":                                 http.StatusNotFound,
		"masters/company_masters/rooms?q=x":         http.StatusNotFound,
		"masters/company_masters/systems?q=x&lang=": http.StatusOK,
	} {
		as := s.requester
		if want == http.StatusUnauthorized {
			as = ""
		}
		if code, _ := find(web.FindPath+"/"+path, as); code != want {
			t.Errorf("%s: %d, not %d", path, code, want)
		}
	}

	// the form: the person and the system are found, not listed
	b := newRequestForm(t, h, s.requester, "account", "en")
	if b.kinds["f.person"] != "text" || !slices.Contains(b.buttons, "find:person") || b.kinds["f.system"] != "text" || !slices.Contains(b.buttons, "find:system") {
		t.Fatalf("the form's parts: %v %v", b.kinds, b.buttons)
	}
	press := func(b *pageForm, do string) *pageForm {
		t.Helper()
		w, err := b.press(h, s.requester, do)
		if err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %.300s", do, w.Code, w.Body.String())
		}
		b, err = formOf(w.Body.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	set := func(b *pageForm, name, v string) {
		t.Helper()
		if err := b.set(name, v); err != nil {
			t.Fatal(err)
		}
	}
	// with no script: the words, the button Search, one of those found
	set(b, "f.person", strings.ToLower(names[person][0]))
	b = press(b, "find:person")
	if !slices.Contains(b.buttons, "choose:person:"+person) {
		t.Fatalf("found by %q: %v", names[person][0], b.buttons)
	}
	b = press(b, "choose:person:"+person)
	set(b, "f.system", "customer")
	b = press(b, "find:system")
	b = press(b, "choose:system:"+system)
	if b.values.Get("f.person") != person || b.values.Get("f.system") != system {
		t.Fatalf("chosen: %v", b.values)
	}
	for name, v := range s.form {
		if name != "person" && name != "system" {
			set(b, "f."+name, fmt.Sprint(v))
		}
	}
	w, err := b.press(h, s.requester, "button.submit")
	if err != nil {
		t.Fatal(err)
	}
	id, ok := strings.CutPrefix(w.Header().Get("Location"), web.PagesPath+"tickets/")
	if w.Code != http.StatusSeeOther || !ok {
		t.Fatalf("sent: %d %s %.300s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
	w = send(h, "GET", "/tickets/"+id, s.requester, nil)
	var tk struct {
		Ticket struct {
			Form map[string]any `json:"form"`
		} `json:"ticket"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &tk); err != nil || tk.Ticket.Form["person"] != person || tk.Ticket.Form["system"] != system {
		t.Errorf("the ticket's form: %v %.300s", err, w.Body.String())
	}
}
