package screens

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// memFS is the test set in memory, to change.
func memFS(t testing.TB) fstest.MapFS {
	t.Helper()
	m := fstest.MapFS{}
	err := filepath.WalkDir(testRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(testRoot, p)
		m[filepath.ToSlash(rel)] = &fstest.MapFile{Data: b}
		return nil
	})
	must(t, err)
	return m
}

// edit changes a JSON file of m.
func edit(t testing.TB, m fstest.MapFS, name string, change func(v any)) {
	t.Helper()
	var v any
	must(t, decode(m[name].Data, &v))
	change(v)
	b, err := marshal(v)
	must(t, err)
	m[name] = &fstest.MapFile{Data: b}
}

// partOf is the part of a template's JSON with an ID.
func partOf(t testing.TB, v any, id string) map[string]any {
	t.Helper()
	for _, m := range v.(map[string]any)["messages"].([]any) {
		if u, ok := m.(map[string]any)["updateComponents"].(map[string]any); ok {
			for _, p := range u["components"].([]any) {
				if p.(map[string]any)["id"] == id {
					return p.(map[string]any)
				}
			}
		}
	}
	t.Fatalf("no part %q", id)
	return nil
}

func dataOf(v any) map[string]any {
	for _, m := range v.(map[string]any)["messages"].([]any) {
		if u, ok := m.(map[string]any)["updateDataModel"].(map[string]any); ok {
			return u["value"].(map[string]any)
		}
	}
	return nil
}

func TestLoad(t *testing.T) {
	s := testSet(t)
	kinds := s.Kinds()
	var names []string
	for _, k := range kinds {
		names = append(names, k.Name)
		if k.Version != 1 || !slices.Equal(k.Languages, []string{"en", "ja"}) {
			t.Errorf("%s: version %d, languages %v", k.Name, k.Version, k.Languages)
		}
	}
	want := []string{"account", "account_ja", "contract", "contract_ja", "expense", "expense_ja", "leave", "leave_ja", "purchase", "purchase_ja"}
	if !slices.Equal(names, want) {
		t.Errorf("kinds %v, not %v", names, want)
	}
	if s.CatalogID() != "https://forms.example.test/a2ui/catalog.json" {
		t.Errorf("catalog %q", s.CatalogID())
	}
	got, err := s.Actions("expense", "card")
	must(t, err)
	if !slices.Equal(got, []string{"edit", "approve", "reject", "send_back", "withdraw", "discard", "add_approver", "hand_over", "comment"}) {
		t.Errorf("the expense card asks about %v", got)
	}
	got, err = s.Actions("leave", "form")
	must(t, err)
	if !slices.Equal(got, []string{"submit", "resubmit", "discard"}) {
		t.Errorf("the leave form asks about %v", got)
	}
	if got, _ := s.Actions("leave", "list"); len(got) != 0 {
		t.Errorf("the list asks about %v", got)
	}
	if _, err := s.Actions("leave", "page"); !errors.Is(err, ErrKind) {
		t.Errorf("a screen it does not have: %v", err)
	}
	if _, err := s.Actions("travel", "card"); !errors.Is(err, ErrKind) {
		t.Errorf("a kind it does not have: %v", err)
	}
}

// Each change a set must not load with, and the error it is refused with.
func TestLoadRefuses(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, m fstest.MapFS)
		want   error
		says   string
	}{
		{"an API of another contract", func(t *testing.T, m fstest.MapFS) {
			edit(t, m, "openapi.json", func(v any) { v.(map[string]any)["x-ritsu-contract"] = json.Number("2") })
		}, ErrContract, "openapi.json's x-ritsu-contract is 2; this module reads [3]"},
		{"a description of another contract", func(t *testing.T, m fstest.MapFS) {
			edit(t, m, "types/leave.json", func(v any) { v.(map[string]any)["contract"] = json.Number("2") })
		}, ErrContract, "types/leave.json's contract is 2; this module reads [3]"},
		{"the functions of another catalog", func(t *testing.T, m fstest.MapFS) {
			b := bytes.Replace(m["a2ui/functions.js"].Data, []byte("forms.example.test"), []byte("other.example.test"), 1)
			m["a2ui/functions.js"] = &fstest.MapFile{Data: b}
		}, ErrSet, "functions.js names the catalog"},
		{"a part of a third catalog", func(t *testing.T, m fstest.MapFS) {
			edit(t, m, "a2ui/leave/form.en.json", func(v any) {
				partOf(t, v, "field.cover")["@catalogId"] = "https://other.example.test/catalog.json"
			})
		}, ErrSet, "names the catalog \"https://other.example.test/catalog.json\""},
		{"a place this module does not fill", func(t *testing.T, m fstest.MapFS) {
			edit(t, m, "a2ui/expense/card.en.json", func(v any) { dataOf(v)["remarks"] = nil })
		}, ErrSet, "the place /remarks"},
		{"a key of /ticket this module does not fill", func(t *testing.T, m fstest.MapFS) {
			edit(t, m, "a2ui/expense/card.ja.json", func(v any) { dataOf(v)["ticket"].(map[string]any)["owner"] = nil })
		}, ErrSet, "the place /ticket/owner"},
		{"a lookup in a table that is no place", func(t *testing.T, m fstest.MapFS) {
			edit(t, m, "a2ui/expense/card.en.json", func(v any) {
				p := partOf(t, v, "field.account.value")
				p["text"].(map[string]any)["args"].(map[string]any)["table"] = map[string]any{"@path": "/names"}
			})
		}, ErrSet, "/names, which is no table"},
		{"an event that is no operation", func(t *testing.T, m fstest.MapFS) {
			for _, lang := range []string{"en", "ja"} {
				edit(t, m, "a2ui/expense/card."+lang+".json", func(v any) {
					partOf(t, v, "button.approve")["action"].(map[string]any)["event"].(map[string]any)["name"] = "approve_it"
				})
			}
		}, ErrSet, "\"approve_it\", which is no operation"},
		{"a button that does not send what its operation requires", func(t *testing.T, m fstest.MapFS) {
			for _, lang := range []string{"en", "ja"} {
				edit(t, m, "a2ui/expense/card."+lang+".json", func(v any) {
					ctx := partOf(t, v, "button.approve")["action"].(map[string]any)["event"].(map[string]any)["context"].(map[string]any)
					delete(ctx, "revision")
				})
			}
		}, ErrSet, "does not send \"revision\""},
		{"the languages' buttons differing", func(t *testing.T, m fstest.MapFS) {
			edit(t, m, "a2ui/leave/card.en.json", func(any) {}) // written again as the other is
			edit(t, m, "a2ui/leave/card.ja.json", func(v any) {
				ctx := partOf(t, v, "button.approve")["action"].(map[string]any)["event"].(map[string]any)["context"].(map[string]any)
				delete(ctx, "comment")
			})
		}, ErrSet, "in its buttons"},
		{"a screen missing in a language", func(t *testing.T, m fstest.MapFS) {
			delete(m, "a2ui/purchase/history.ja.json")
		}, ErrSet, "has not the screens [form card list mine history] in \"ja\""},
		{"no list of one's own requests", func(t *testing.T, m fstest.MapFS) {
			delete(m, "a2ui/leave/mine.en.json")
			delete(m, "a2ui/leave/mine.ja.json")
		}, ErrSet, "a2ui/leave has not the screens [form card list mine history] in \"en\""},
		{"a screen that starts refused", func(t *testing.T, m fstest.MapFS) {
			edit(t, m, "a2ui/expense/form.en.json", func(v any) { dataOf(v)["refused"].(map[string]any)["text"] = "No." })
		}, ErrSet, "its place /refused/text starts as string, not null"},
		{"a key of /refused this module does not fill", func(t *testing.T, m fstest.MapFS) {
			edit(t, m, "a2ui/expense/card.en.json", func(v any) { dataOf(v)["refused"].(map[string]any)["code"] = nil })
		}, ErrSet, "the place /refused/code"},
		{"a row of one's own requests that reads the requester", func(t *testing.T, m fstest.MapFS) {
			b := bytes.ReplaceAll(m["a2ui/leave/mine.en.json"].Data, []byte(`"made_by"`), []byte(`"requester"`))
			m["a2ui/leave/mine.en.json"] = &fstest.MapFile{Data: b}
		}, ErrSet, "\"/tickets/*/requester\", which a ticket's row does not have"},
		{"a screen that starts refused at the comment", func(t *testing.T, m fstest.MapFS) {
			edit(t, m, "a2ui/expense/card.en.json", func(v any) { dataOf(v)["refused"].(map[string]any)["comment"] = true })
		}, ErrSet, "its place /refused/comment starts as true, not false"},
		{"a list that starts with more", func(t *testing.T, m fstest.MapFS) {
			edit(t, m, "a2ui/expense/list.en.json", func(v any) { dataOf(v)["more"] = nil })
		}, ErrSet, "its place /more starts as null, not bool"},
		// the next page's button sends the list's `before` in the query of list_waiting, which takes
		// no `after`
		{"a button that sends what its operation does not take", func(t *testing.T, m fstest.MapFS) {
			b := bytes.ReplaceAll(m["a2ui/leave/list.en.json"].Data, []byte(`"before": {`), []byte(`"after": {`))
			m["a2ui/leave/list.en.json"] = &fstest.MapFile{Data: b}
		}, ErrSet, "sends \"after\", which \"list_waiting\" takes neither in its body nor in its query"},
		{"a template of another surface", func(t *testing.T, m fstest.MapFS) {
			b := bytes.ReplaceAll(m["a2ui/leave/list.en.json"].Data, []byte(`"leave.list"`), []byte(`"leave.lines"`))
			m["a2ui/leave/list.en.json"] = &fstest.MapFile{Data: b}
		}, ErrSet, "of the surface \"leave.lines\""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := memFS(t)
			c.change(t, m)
			_, err := Load(m)
			if !errors.Is(err, c.want) || !strings.Contains(err.Error(), c.says) {
				t.Errorf("Load: %v; want %v saying %q", err, c.want, c.says)
			}
		})
	}
	// and the set as it is loads
	if _, err := Load(memFS(t)); err != nil {
		t.Fatal(err)
	}
}

func TestAssets(t *testing.T) {
	s := testSet(t)
	for name, ct := range map[string]string{"catalog.json": "application/json", "functions.js": "text/javascript; charset=utf-8"} {
		a, err := s.Asset(name)
		must(t, err)
		b, err := os.ReadFile(filepath.Join(testRoot, "a2ui", name))
		must(t, err)
		if a.Name != name || a.ContentType != ct || !bytes.Equal(a.Body, b) {
			t.Errorf("%s: %q, %q, %d bytes", name, a.Name, a.ContentType, len(a.Body))
		}
		if !regexp.MustCompile(`^"[0-9a-f]{64}"$`).MatchString(a.ETag) {
			t.Errorf("%s: ETag %s", name, a.ETag)
		}
	}
	if _, err := s.Asset("form.en.json"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("another name: %v", err)
	}
	if err := fstest.TestFS(s.Assets(), "catalog.json", "functions.js"); err != nil {
		t.Error(err)
	}
	// the ETag changes with the body, the name does not
	m := memFS(t)
	m["a2ui/functions.js"] = &fstest.MapFile{Data: append(slices.Clone(m["a2ui/functions.js"].Data), []byte("\n// changed\n")...)}
	other, err := Load(m)
	must(t, err)
	a, _ := s.Asset("functions.js")
	b, _ := other.Asset("functions.js")
	if a.ETag == b.ETag || a.Name != b.Name {
		t.Errorf("the ETags %s and %s of two bodies", a.ETag, b.ETag)
	}
}

func TestParseAction(t *testing.T) {
	good := `{"version": "1.0", "action": {"name": "approve", "surfaceId": "leave.card.T-1", "sourceComponentId": "button.approve",
		"timestamp": "2026-11-16T09:30:00.250+09:00", "context": {"ticket": "T-1", "revision": {"round": 1, "stage": 0, "edits": 0}, "comment": null},
		"userMessage": "Approve"}}`
	a, err := ParseAction([]byte(good))
	must(t, err)
	if a.Name != "approve" || a.SurfaceID != "leave.card.T-1" || a.Component != "button.approve" || len(a.Context) != 3 ||
		!a.Timestamp.Equal(time.Date(2026, 11, 16, 0, 30, 0, 250e6, time.UTC)) {
		t.Errorf("%+v", a)
	}
	for _, bad := range []string{
		`{"version": "0.9", "action": {"name": "approve", "surfaceId": "s", "sourceComponentId": "b", "timestamp": "2026-11-16T00:00:00Z", "context": {}}}`,
		`{"version": "1.0", "error": {"code": "VALIDATION_FAILED", "surfaceId": "s", "path": "/x", "message": "m"}}`,
		`{"version": "1.0", "action": {"name": "approve", "surfaceId": "s", "sourceComponentId": "b", "timestamp": "2026-11-16T00:00:00Z"}}`,
		`{"version": "1.0", "action": {"name": "approve", "surfaceId": "s", "sourceComponentId": "b", "timestamp": "yesterday", "context": {}}}`,
		`{"version": "1.0", "action": {"name": "approve", "surfaceId": "s", "sourceComponentId": "b", "timestamp": "2026-11-16T00:00:00Z", "context": {}}, "more": 1}`,
		`{"version": "1.0", "action": {"name": "approve", "surfaceId": "s", "sourceComponentId": "b", "timestamp": "2026-11-16T00:00:00Z", "context": {}}} {}`,
		`not JSON`,
	} {
		if _, err := ParseAction([]byte(bad)); !errors.Is(err, ErrMessage) {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

// action is a message as a renderer sends it.
func action(t testing.TB, name, surface, button, ts string, context map[string]any) Action {
	t.Helper()
	if context == nil {
		context = map[string]any{}
	}
	b, err := json.Marshal(map[string]any{"version": "1.0", "action": map[string]any{
		"name": name, "surfaceId": surface, "sourceComponentId": button, "timestamp": ts, "context": context}})
	must(t, err)
	a, err := ParseAction(b)
	must(t, err)
	return a
}

func TestOperation(t *testing.T) {
	s := testSet(t)
	rev := map[string]any{"round": 1, "stage": 0, "edits": 0}
	ts := "2026-11-16T00:00:00Z"
	op, err := s.Operation(action(t, "approve", "leave.card.T-1", "button.approve", ts, map[string]any{"ticket": "T-1", "revision": rev, "comment": "Fine."}))
	must(t, err)
	if op.ID != "approve" || op.Method != "POST" || op.Path != "/tickets/T-1/approve" || op.Ticket != "T-1" ||
		string(op.Body) != `{"revision":{"edits":0,"round":1,"stage":0},"comment":"Fine."}` || !strings.HasPrefix(op.Key, "screen-") {
		t.Errorf("%+v %s", op, op.Body)
	}
	// the ticket in the path is escaped
	op, err = s.Operation(action(t, "approve", "leave.card.x", "button.approve", ts, map[string]any{"ticket": "T 1/2", "revision": rev, "comment": nil}))
	must(t, err)
	if op.Path != "/tickets/T%201%2F2/approve" || string(op.Body) != `{"revision":{"edits":0,"round":1,"stage":0},"comment":null}` {
		t.Errorf("%s %s", op.Path, op.Body)
	}
	// a null the API takes stays: back to the requester
	op, err = s.Operation(action(t, "send_back", "contract.card.T-1", "button.send_back", ts, map[string]any{"ticket": "T-1", "revision": rev, "to": nil, "comment": nil}))
	must(t, err)
	if string(op.Body) != `{"revision":{"edits":0,"round":1,"stage":0},"to":null,"comment":null}` {
		t.Errorf("%s", op.Body)
	}
	// a GET: the list's row opens its ticket, with no body and no key
	op, err = s.Operation(action(t, "get_ticket", "leave.list.mine", "line.open", ts, map[string]any{"ticket": "T-7"}))
	must(t, err)
	if op.ID != "get_ticket" || op.Method != "GET" || op.Path != "/tickets/T-7" || op.Body != nil || op.Key != "" {
		t.Errorf("%+v", op)
	}
	// a list's next page: the list's operation, the last ticket of the page as `before` in its
	// query, and list_mine's `kind` (ringi's DESIGN 13.2)
	op, err = s.Operation(action(t, "list_waiting", "leave.list", "more", ts, map[string]any{"before": "T-9"}))
	must(t, err)
	if op.ID != "list_waiting" || op.Method != "GET" || op.Path != "/waiting" || op.Body != nil || op.Key != "" || op.Query.Encode() != "before=T-9" {
		t.Errorf("%+v", op)
	}
	op, err = s.Operation(action(t, "list_mine", "leave.mine", "more", ts, map[string]any{"before": "T-9", "kind": "leave"}))
	must(t, err)
	if op.ID != "list_mine" || op.Path != "/mine" || op.Query.Encode() != "before=T-9&kind=leave" {
		t.Errorf("%+v", op)
	}
	// a new request: no ticket in the path
	op, err = s.Operation(action(t, "submit_leave", "leave.form.f1", "button.submit", ts, map[string]any{"requester": nil, "form": map[string]any{"kind": "paid"}, "comment": nil}))
	must(t, err)
	if op.Path != "/requests/leave/tickets" || op.Ticket != "" || string(op.Body) != `{"requester":null,"form":{"kind":"paid"},"comment":null}` {
		t.Errorf("%+v %s", op, op.Body)
	}

	// a comment with nothing written is sent as it is: the API takes null for its text, and the kind
	// refuses it as form (ringi's DESIGN 12.4)
	op, err = s.Operation(action(t, "comment", "leave.card.T-1", "button.comment", ts, map[string]any{"ticket": "T-1", "comment": nil}))
	must(t, err)
	if op.Path != "/tickets/T-1/comment" || string(op.Body) != `{"comment":null}` {
		t.Errorf("%+v %s", op, op.Body)
	}

	refused := []struct {
		name string
		a    Action
		want error
	}{
		{"a surface of no screen", action(t, "approve", "leave.page.T-1", "button.approve", ts, nil), ErrSurface},
		{"a surface of no kind", action(t, "approve", "travel.card.T-1", "button.approve", ts, nil), ErrSurface},
		{"a surface with no screen", action(t, "approve", "leave", "button.approve", ts, nil), ErrSurface},
		{"a part that is no button", action(t, "approve", "leave.card.T-1", "comment", ts, nil), ErrButton},
		{"a button that opens a file on the renderer", action(t, "open", "expense.card.T-1", "field.receipt.value.open", ts, map[string]any{}), ErrButton},
		{"another event than the button's", action(t, "reject", "leave.card.T-1", "button.approve", ts, map[string]any{"ticket": "T-1", "revision": rev, "comment": nil}), ErrButton},
		{"a key the button sends missing", action(t, "approve", "leave.card.T-1", "button.approve", ts, map[string]any{"ticket": "T-1", "revision": rev}), ErrContext},
		{"a key the button does not send", action(t, "approve", "leave.card.T-1", "button.approve", ts, map[string]any{"ticket": "T-1", "revision": rev, "comment": nil, "by": "e0001"}), ErrContext},
		{"no ticket", action(t, "approve", "leave.card.T-1", "button.approve", ts, map[string]any{"ticket": nil, "revision": rev, "comment": nil}), ErrContext},
		{"an empty ticket", action(t, "approve", "leave.card.T-1", "button.approve", ts, map[string]any{"ticket": "", "revision": rev, "comment": nil}), ErrContext},
		{"no revision", action(t, "approve", "leave.card.T-1", "button.approve", ts, map[string]any{"ticket": "T-1", "revision": nil, "comment": nil}), ErrContext},
		{"no approver chosen", action(t, "add_approver", "leave.card.T-1", "button.add_approver", ts, map[string]any{"ticket": "T-1", "revision": rev, "approver": nil, "part": "manager", "comment": nil}), ErrContext},
		{"a list's next page after an object", action(t, "list_waiting", "leave.list", "more", ts, map[string]any{"before": map[string]any{"id": "T-9"}}), ErrContext},
	}
	for _, c := range refused {
		if _, err := s.Operation(c.a); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, not %v", c.name, err, c.want)
		}
	}
}

// The idempotency key of an operation made on a screen.
func TestOperationKeys(t *testing.T) {
	s := testSet(t)
	key := func(name, surface, button, ts string, context map[string]any) string {
		t.Helper()
		op, err := s.Operation(action(t, name, surface, button, ts, context))
		must(t, err)
		return op.Key
	}
	form := map[string]any{"requester": nil, "form": map[string]any{"kind": "paid", "first_day": "2026-12-21", "days": 3}, "comment": nil}
	other := map[string]any{"requester": nil, "form": map[string]any{"kind": "paid", "first_day": "2026-12-21", "days": 4}, "comment": nil}
	first := key("submit_leave", "leave.form.f1", "button.submit", "2026-11-16T09:00:00.100Z", form)
	// the same form pressed twice: one request
	if again := key("submit_leave", "leave.form.f1", "button.submit", "2026-11-16T09:00:00.400Z", form); again != first {
		t.Errorf("the same form pressed twice: %s, %s", first, again)
	}
	// another form, with the same values: another request
	if another := key("submit_leave", "leave.form.f2", "button.submit", "2026-11-16T09:00:00.100Z", form); another == first {
		t.Error("two forms make one key")
	}
	// the same form, its values changed after a refusal: another key, never the same key with another body
	if changed := key("submit_leave", "leave.form.f1", "button.submit", "2026-11-16T09:01:00Z", other); changed == first {
		t.Error("two bodies make one key")
	}
	// a comment written again, the same words a week after: another comment, and so another key;
	// the same message sent again: the same key
	c := map[string]any{"ticket": "T-1", "comment": "Thank you."}
	c1 := key("comment", "leave.card.T-1", "button.comment", "2026-11-16T09:00:00Z", c)
	if c1 == key("comment", "leave.card.T-1", "button.comment", "2026-11-23T09:00:00Z", c) {
		t.Error("two comments of the same words make one key")
	}
	if c1 != key("comment", "leave.card.T-1", "button.comment", "2026-11-16T09:00:00Z", c) {
		t.Error("a comment sent again makes another key")
	}
	// an operation with the revision: a key for each press, the same for the same message sent again
	rev := map[string]any{"ticket": "T-1", "revision": map[string]any{"round": 1, "stage": 0, "edits": 0}, "comment": nil}
	a1 := key("approve", "leave.card.T-1", "button.approve", "2026-11-16T09:00:00Z", rev)
	if a1 != key("approve", "leave.card.T-1", "button.approve", "2026-11-16T09:00:00Z", rev) {
		t.Error("the same message sent again makes another key")
	}
	if a1 == key("approve", "leave.card.T-1", "button.approve", "2026-11-16T09:00:05Z", rev) {
		t.Error("two presses of approve make one key")
	}
	// the time is read as a time: the same instant written in another zone is the same press
	if a1 != key("approve", "leave.card.T-1", "button.approve", "2026-11-16T18:00:00+09:00", rev) {
		t.Error("one instant in two zones makes two keys")
	}
}

func TestWithOldNames(t *testing.T) {
	s := testSet(t)
	sc, err := s.Form(Form{Kind: "expense", Language: "en", Surface: "f1", Can: []string{"submit"}})
	must(t, err)
	old, err := sc.WithOldNames()
	must(t, err)
	if !bytes.Equal(old.Messages[1], sc.Messages[1]) || old.SurfaceID != sc.SurfaceID {
		t.Error("the data changed")
	}
	if !bytes.HasPrefix(old.Messages[2], []byte(`{"version":"1.0","updateComponents":{`)) {
		t.Errorf("the parts' message starts %.40s", old.Messages[2])
	}
	var parts []map[string]json.RawMessage
	var msg map[string]json.RawMessage
	var u map[string]json.RawMessage
	must(t, json.Unmarshal(old.Messages[2], &msg))
	must(t, json.Unmarshal(msg["updateComponents"], &u))
	must(t, json.Unmarshal(u["components"], &parts))
	moved := map[string]int{}
	for _, p := range parts {
		for k := range p {
			if strings.HasPrefix(k, "@") {
				t.Errorf("the part %s still has %s", p["id"], k)
			}
			if k == "checks" || k == "catalogId" {
				moved[k]++
			}
		}
	}
	if moved["checks"] == 0 || moved["catalogId"] == 0 {
		t.Errorf("moved %v", moved)
	}
	// the calls inside keep their catalogId, as A2UI 1.0 writes it
	if !bytes.Contains(old.Messages[2], []byte(`"@call":"expense.check","args":`)) && !bytes.Contains(old.Messages[2], []byte(`"@call":"expense.check"`)) {
		t.Error("the calls changed")
	}
	tf := readTemplate(t, "expense/form.en")
	if len(parts) != len(tf.parts) {
		t.Errorf("%d parts, not %d", len(parts), len(tf.parts))
	}
}

// What a fill refuses, and with which error.
func TestFillRefuses(t *testing.T) {
	s := testSet(t)
	vs := testVectors(t)
	var inProgress, back json.RawMessage
	for _, v := range vs {
		if v.Kind != "leave" || isNull(v.Ticket) {
			continue
		}
		var tk struct{ Status string }
		must(t, json.Unmarshal(v.Ticket, &tk))
		switch {
		case tk.Status == "in_progress" && inProgress == nil:
			inProgress = v.Ticket
		case tk.Status == "with_requester" && back == nil:
			back = v.Ticket
		}
	}
	changed := func(raw json.RawMessage, key string, v any) json.RawMessage {
		var m map[string]any
		must(t, decode(raw, &m))
		m[key] = v
		b, err := marshal(m)
		must(t, err)
		return b
	}
	cases := []struct {
		name string
		fill func() (Screen, error)
		want error
	}{
		{"a kind the set does not have", func() (Screen, error) {
			return s.Form(Form{Kind: "travel", Language: "en", Surface: "f"})
		}, ErrKind},
		{"a language the kind has not", func() (Screen, error) { return s.Card(Card{Language: "fr", Ticket: inProgress}) }, ErrKind},
		{"a ticket of another kind", func() (Screen, error) { return s.Card(Card{Kind: "expense", Language: "en", Ticket: inProgress}) }, ErrKind},
		{"a list of a ticket of another kind", func() (Screen, error) {
			return s.List(List{Kind: "leave_ja", Language: "en", Tickets: []json.RawMessage{inProgress}})
		}, ErrKind},
		{"a list that names no kind", func() (Screen, error) { return s.List(List{Language: "en"}) }, ErrKind},
		{"a ticket of another version", func() (Screen, error) {
			return s.Card(Card{Language: "en", Ticket: changed(inProgress, "version", 2)})
		}, ErrVersion},
		{"a history of a ticket of another version", func() (Screen, error) {
			return s.History(History{Language: "en", Ticket: changed(inProgress, "version", 2)})
		}, ErrVersion},
		{"an action the card has not", func() (Screen, error) {
			return s.Card(Card{Language: "en", Ticket: inProgress, Can: []string{"approve", "aprove"}})
		}, ErrInput},
		{"a new request's form with no surface of its own", func() (Screen, error) {
			return s.Form(Form{Kind: "leave", Language: "en", Can: []string{"submit"}})
		}, ErrInput},
		{"resubmit on a new request's form", func() (Screen, error) {
			return s.Form(Form{Kind: "leave", Language: "en", Surface: "f", Can: []string{"resubmit"}})
		}, ErrInput},
		{"submit on a request made again", func() (Screen, error) {
			return s.Form(Form{Language: "en", Ticket: back, Can: []string{"submit"}})
		}, ErrInput},
		{"SubmitFor on a request made again", func() (Screen, error) {
			return s.Form(Form{Language: "en", Ticket: back, SubmitFor: []string{"e0001"}})
		}, ErrInput},
		{"an empty ID to submit for", func() (Screen, error) {
			return s.Form(Form{Kind: "leave", Language: "en", Surface: "f", SubmitFor: []string{"e0001", ""}})
		}, ErrInput},
		{"a ticket that is not one", func() (Screen, error) { return s.Card(Card{Language: "en", Ticket: json.RawMessage(`[1]`)}) }, ErrInput},
		{"a ticket with no form", func() (Screen, error) {
			return s.Card(Card{Language: "en", Ticket: changed(inProgress, "form", nil)})
		}, ErrInput},
		{"a card with no ticket", func() (Screen, error) { return s.Card(Card{Kind: "leave", Language: "en"}) }, ErrInput},
		{"a record with no time", func() (Screen, error) {
			return s.History(History{Language: "en", Ticket: inProgress, Records: []Record{{History: json.RawMessage(`{"did": "submitted"}`)}}})
		}, ErrInput},
		{"a record that is not one", func() (Screen, error) {
			return s.History(History{Language: "en", Ticket: inProgress, Records: []Record{{At: time.Now(), History: json.RawMessage(`"submitted"`)}}})
		}, ErrInput},
	}
	for _, c := range cases {
		if _, err := c.fill(); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, not %v", c.name, err, c.want)
		}
	}
	// a failure of the names comes back as it is
	boom := errors.New("the directory is down")
	if _, err := s.Card(Card{Language: "en", Ticket: inProgress, Names: failing{boom}}); !errors.Is(err, boom) {
		t.Errorf("a failing lookup: %v", err)
	}
}

type failing struct{ err error }

func (f failing) PeopleNames(string, []string) (map[string]string, error)         { return nil, f.err }
func (f failing) MasterNames(string, string, []string) (map[string]string, error) { return nil, f.err }
func (f failing) FileURLs([]string) (map[string]string, error)                    { return nil, f.err }

// A card that comes to show who made the request (a key of the API's ticket under /ticket, looked
// up in /people) loads, and is filled and named from the ticket, with no change to this module.
func TestTicketPlaces(t *testing.T) {
	m := memFS(t)
	for _, lang := range []string{"en", "ja"} {
		edit(t, m, "a2ui/leave/card."+lang+".json", func(v any) {
			dataOf(v)["ticket"].(map[string]any)["requester"] = nil
			for _, msg := range v.(map[string]any)["messages"].([]any) {
				u, ok := msg.(map[string]any)["updateComponents"].(map[string]any)
				if !ok {
					continue
				}
				u["components"] = append(u["components"].([]any), map[string]any{"id": "ticket.requester", "component": "Text",
					"text": map[string]any{"@call": "lookup", "catalogId": "https://forms.example.test/a2ui/catalog.json",
						"args": map[string]any{"value": map[string]any{"@path": "/ticket/requester"}, "table": map[string]any{"@path": "/people"}}}})
			}
			root := partOf(t, v, "root")
			root["children"] = append(root["children"].([]any), "ticket.requester")
		})
	}
	s, err := Load(m)
	must(t, err)
	var tk json.RawMessage
	for _, raw := range testCorpus(t).tickets["leave"] {
		if x, _ := readTicket(raw); x.m["status"] == "in_progress" {
			tk = raw
			break
		}
	}
	x, _ := readTicket(tk)
	n := newNamer()
	sc, err := s.Card(Card{Language: "en", Ticket: tk, Names: n})
	must(t, err)
	data := parse(t, sc).data
	requester := x.m["requester"].(string)
	if got := data["ticket"].(map[string]any)["requester"]; got != requester {
		t.Errorf("/ticket/requester is %v, not %s", got, requester)
	}
	if !slices.Contains(n.asked["people"], requester) {
		t.Errorf("the requester %s is not named: %v", requester, n.asked["people"])
	}
}

// TestReadPlace checks the one reading of a place, as ringi writes the places its answers name
// (DESIGN 12.4): a refusal's `at`, and the places its entries' errors name.
func TestReadPlace(t *testing.T) {
	for _, c := range []struct {
		at   string
		want []Step // nil: not a place
	}{
		{"purpose", []Step{{"purpose", -1}}},
		{"lines", []Step{{"lines", -1}}},
		{"items[1].price", []Step{{"items", 1}, {"price", -1}}},
		{"others[0].bid", []Step{{"others", 0}, {"bid", -1}}},
		{"lines[12].subtotal", []Step{{"lines", 12}, {"subtotal", -1}}},
		{"submit.form.items[1].price", []Step{{"submit", -1}, {"form", -1}, {"items", 1}, {"price", -1}}},
		{"organization.people[0]", []Step{{"organization", -1}, {"people", 0}}},
		{"event.approve.revision.round", []Step{{"event", -1}, {"approve", -1}, {"revision", -1}, {"round", -1}}},
		{"_x1", []Step{{"_x1", -1}}},
		{"", nil},
		{"items.1.price", nil}, // the row as a name
		{"items[01].price", nil},
		{"items[-1].price", nil},
		{"items[].price", nil},
		{"items[1", nil},
		{"items[1]price", nil},
		{"items[1][2]", nil},
		{"items]", nil},
		{"a..b", nil},
		{".a", nil},
		{"a.", nil},
		{"1a", nil},
		{"目的", nil},
		{"a b", nil},
	} {
		got, ok := ReadPlace(c.at)
		if ok != (c.want != nil) || !slices.Equal(got, c.want) {
			t.Errorf("ReadPlace(%q) = %v %v; want %v", c.at, got, ok, c.want)
		}
	}
	for _, c := range []struct {
		at, field, column string
		row               int
		ok                bool
	}{
		{"purpose", "purpose", "", -1, true},
		{"items[1].price", "items", "price", 1, true},
		{"items", "items", "", -1, true},
		{"items[1]", "items", "", 1, true},
		{"submit.form.purpose", "", "", -1, false}, // not a place of the form
		{"items[1].price.x", "", "", -1, false},
		{"items.price", "", "", -1, false},
		{"", "", "", -1, false},
	} {
		field, row, column, ok := Refusal{At: c.at}.Place()
		if field != c.field || row != c.row || column != c.column || ok != c.ok {
			t.Errorf("Place of %q = %q %d %q %v; want %q %d %q %v", c.at, field, row, column, ok, c.field, c.row, c.column, c.ok)
		}
		if want := c.field; c.ok && (Refusal{At: c.at}).Field() != want {
			t.Errorf("Field of %q = %q; want %q", c.at, Refusal{At: c.at}.Field(), want)
		}
	}
}
